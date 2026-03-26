package ai

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

func boolPtr(b bool) *bool { return &b }

// stubHandler is a minimal base.RoundTripper for testing.
type stubHandler struct {
	responsesFn func(ctx context.Context, req *model.Request) (model.ResponseStream, error)
}

func (s *stubHandler) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	if s.responsesFn != nil {
		return s.responsesFn(ctx, req)
	}
	return &stubStream{resp: &model.Response{Content: "ok"}}, nil
}

type stubStream struct {
	resp *model.Response
	done bool
}

func (s *stubStream) Next(_ context.Context) (model.StreamEvent, error) {
	if s.done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
	}
	s.done = true
	if s.resp.Content != "" {
		return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: s.resp.Content}, nil
	}
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *stubStream) Close() error              { return nil }
func (s *stubStream) Response() *model.Response { return s.resp }

func stubFactory(h base.RoundTripper) base.HandlerFactory {
	return func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return h, nil
	}
}

func newTestService(t *testing.T, cfg conf.ModelsConfig, h base.RoundTripper) *Service {
	t.Helper()
	extras := map[base.HandlerType]base.HandlerFactory{"mock": stubFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// chatViaService is a test helper that calls Responses+DrainStream.
func chatViaService(t *testing.T, s *Service, modelRef string) *model.Response {
	t.Helper()
	stream, err := s.Responses(context.Background(), &model.Request{Model: modelRef})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	return resp
}

func TestService_DirectLookup(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "gpt-4o", ForwardID: "gpt-4o-2024-08-06"}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	resp := chatViaService(t, s, "openai/gpt-4o")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_BareModelMultiProvider(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "gpt-4o"}}},
			{ID: "p2", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "gpt-4o"}}},
		},
	}
	s := newTestService(t, cfg, &stubHandler{})
	// Both providers serve gpt-4o — should resolve and work
	resp := chatViaService(t, s, "gpt-4o")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_CommaFailoverList(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "openai", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "gpt-4o"}}},
			{ID: "anthropic", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "claude-3"}}},
		},
	}
	s := newTestService(t, cfg, &stubHandler{})
	resp := chatViaService(t, s, "gpt-4o,claude-3")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_DefaultFallback(t *testing.T) {
	cfg := conf.ModelsConfig{
		Default: "gpt-4o",
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	// Empty model → uses default
	resp := chatViaService(t, s, "")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_Failover_OnServerError(t *testing.T) {
	callCount := 0
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		if callCount == 1 {
			return nil, &model.AIError{Kind: model.ErrServerError, StatusCode: 503, Message: "unavailable"}
		}
		return &stubStream{resp: &model.Response{Content: "from-p2"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	s := newTestService(t, cfg, h)
	resp := chatViaService(t, s, "m1")
	if resp.Content != "from-p2" {
		t.Errorf("Content: got %q, want from-p2", resp.Content)
	}
	if callCount != 2 {
		t.Errorf("callCount: got %d, want 2", callCount)
	}
}

func TestService_HardStop_OnBadRequest(t *testing.T) {
	h := &stubHandler{responsesFn: func(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
		return nil, &model.AIError{Kind: model.ErrOther, StatusCode: 400, Message: "bad request"}
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	s := newTestService(t, cfg, h)
	_, err := s.Responses(context.Background(), &model.Request{Model: "m1"})
	if err == nil {
		t.Fatal("expected error for 400")
	}
}

func TestService_HardStop_OnContextOverflow(t *testing.T) {
	h := &stubHandler{responsesFn: func(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
		return nil, &model.AIError{Kind: model.ErrContextOverflow, Message: "context overflow"}
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	s := newTestService(t, cfg, h)
	_, err := s.Responses(context.Background(), &model.Request{Model: "m1"})
	if err == nil {
		t.Fatal("expected error for context overflow")
	}
}

func TestPenaltyBox_Basic(t *testing.T) {
	pb := newPenaltyBox()
	now := time.Now()
	pb.now = func() time.Time { return now }

	if pb.isPenalized("p1", "m1") {
		t.Error("should not be penalized initially")
	}

	pb.penalize("p1", "m1", 60*time.Second)
	if !pb.isPenalized("p1", "m1") {
		t.Error("should be penalized after penalize()")
	}

	// Advance past TTL
	pb.now = func() time.Time { return now.Add(61 * time.Second) }
	if pb.isPenalized("p1", "m1") {
		t.Error("should not be penalized after TTL expires")
	}
}

func TestService_PenaltySkipsProvider(t *testing.T) {
	callCount := 0
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	s := newTestService(t, cfg, h)
	// Penalize p1
	s.penalty.penalize("p1", "m1", time.Hour)
	resp := chatViaService(t, s, "m1")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
	// Should have only called once (p2), skipping p1
	if callCount != 1 {
		t.Errorf("callCount: got %d, want 1", callCount)
	}
}

func TestService_DisabledProvider(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "disabled", Type: "mock", Key: "k1", Disabled: true,
				Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "active", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	s := newTestService(t, cfg, &stubHandler{})
	// Should only resolve to active provider
	resp := chatViaService(t, s, "m1")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_ForwardID(t *testing.T) {
	var gotModel string
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		gotModel = req.Model
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "fast", ForwardID: "gpt-4o-mini-2024-07-18"}},
		}},
	}
	s := newTestService(t, cfg, h)
	chatViaService(t, s, "fast")
	if gotModel != "gpt-4o-mini-2024-07-18" {
		t.Errorf("forwarded model: got %q, want gpt-4o-mini-2024-07-18", gotModel)
	}
}

func TestService_UnknownProvider(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	_, err := s.Responses(context.Background(), &model.Request{Model: "nonexistent/gpt-4o"})
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestService_UnknownBareModel_ReturnsError(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	_, err := s.Responses(context.Background(), &model.Request{Model: "gpt-4-turbo"})
	if err == nil {
		t.Fatal("expected error for unknown bare model")
	}
}

func TestService_EnvFallback(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	defer func() {}()

	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "gpt-4o")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_EnvFallback_ArbitraryModel(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})

	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Arbitrary model not in KnownModelIDs should still resolve via wildcard
	resp := chatViaService(t, s, "ft:gpt-4o:my-org:custom:id")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_ExplicitConfig_RejectsUnknownModel(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	_, err := s.Responses(context.Background(), &model.Request{Model: "ft:gpt-4o:my-org:custom:id"})
	if err == nil {
		t.Fatal("expected error for unknown model with explicit config")
	}
}

func TestService_WithExtras(t *testing.T) {
	extras := map[base.HandlerType]base.HandlerFactory{
		"custom": func(_ conf.ProviderConfig) (base.RoundTripper, error) {
			return &stubHandler{}, nil
		},
	}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "custom-provider", Type: "custom", Key: "k1",
			Models: []conf.ModelConfig{{ID: "custom-model"}},
		}},
	}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "custom-model")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_DirectLookup_ModelNotFound(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "azure", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "gpt-5.2"}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	_, err := s.Responses(context.Background(), &model.Request{Model: "azure/gpt-5.4"})
	if err == nil {
		t.Fatal("expected error for model not found in provider")
	}
}

func TestService_ModelMetadata(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{
				ID: "gpt-4o", ContextWindow: 128000, MaxTokens: 4096,
				Thinking: boolPtr(true), Vision: boolPtr(true), Name: "GPT-4o",
			}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	meta := s.ModelMetadataFor("gpt-4o")
	if meta.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d, want 128000", meta.ContextWindow)
	}
	if meta.MaxTokens != 4096 {
		t.Errorf("MaxTokens: got %d, want 4096", meta.MaxTokens)
	}
	if !model.BoolVal(meta.Thinking) {
		t.Error("Thinking: got false, want true")
	}
	if meta.Name != "GPT-4o" {
		t.Errorf("Name: got %q, want GPT-4o", meta.Name)
	}
}

func TestService_ContextWindowSkipsSmallModel(t *testing.T) {
	callCount := 0
	var calledModels []string
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		calledModels = append(calledModels, req.Model)
		if callCount == 1 {
			return nil, &model.AIError{Kind: model.ErrServerError, StatusCode: 503, Message: "down"}
		}
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "big", ContextWindow: 128000}}},
			{ID: "p2", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "small", ContextWindow: 8000}}},
			{ID: "p3", Type: "mock", Key: "k3",
				Models: []conf.ModelConfig{{ID: "also-big", ContextWindow: 200000}}},
		},
	}
	s := newTestService(t, cfg, h)
	stream, err := s.Responses(context.Background(), &model.Request{Model: "big,small,also-big", EstimatedTokens: 100000})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
	if callCount != 2 {
		t.Errorf("callCount: got %d, want 2", callCount)
	}
	if len(calledModels) != 2 || calledModels[1] != "also-big" {
		t.Errorf("calledModels: got %v, want [big also-big]", calledModels)
	}
}

func TestService_ContextWindowNoFilterWithoutEstimate(t *testing.T) {
	callCount := 0
	var calledModels []string
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		calledModels = append(calledModels, req.Model)
		if callCount == 1 {
			return nil, &model.AIError{Kind: model.ErrServerError, StatusCode: 503, Message: "down"}
		}
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "big", ContextWindow: 128000}}},
			{ID: "p2", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "small", ContextWindow: 8000}}},
		},
	}
	s := newTestService(t, cfg, h)
	stream, err := s.Responses(context.Background(), &model.Request{Model: "big,small"})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
	if callCount != 2 {
		t.Errorf("callCount: got %d, want 2", callCount)
	}
	if len(calledModels) != 2 || calledModels[1] != "small" {
		t.Errorf("calledModels: got %v, want [big small]", calledModels)
	}
}

func TestService_ContextWindowSmallEstimateFitsSmallModel(t *testing.T) {
	callCount := 0
	var calledModels []string
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		calledModels = append(calledModels, req.Model)
		if callCount == 1 {
			return nil, &model.AIError{Kind: model.ErrServerError, StatusCode: 503, Message: "down"}
		}
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "big", ContextWindow: 128000}}},
			{ID: "p2", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "small", ContextWindow: 8000}}},
		},
	}
	s := newTestService(t, cfg, h)
	stream, err := s.Responses(context.Background(), &model.Request{Model: "big,small", EstimatedTokens: 2000})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
	if callCount != 2 {
		t.Errorf("callCount: got %d, want 2", callCount)
	}
	if len(calledModels) != 2 || calledModels[1] != "small" {
		t.Errorf("calledModels: got %v, want [big small]", calledModels)
	}
}

// capturingFactory returns a HandlerFactory that records the ProviderConfig it receives.
func capturingFactory(h base.RoundTripper, captured *conf.ProviderConfig) base.HandlerFactory {
	return func(pc conf.ProviderConfig) (base.RoundTripper, error) {
		*captured = pc
		return h, nil
	}
}

func TestService_ExplicitProvider_EmptyKey_EnvInjected(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	var got conf.ProviderConfig
	extras := map[base.HandlerType]base.HandlerFactory{
		base.HandlerTypeOpenAI: capturingFactory(&stubHandler{}, &got),
	}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: string(base.HandlerTypeOpenAI),
			Models: []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	_, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got.Key != "sk-from-env" {
		t.Errorf("Key: got %q, want %q", got.Key, "sk-from-env")
	}
}

func TestService_ExplicitProvider_CustomBaseURL_NoKeyInjection(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	var got conf.ProviderConfig
	extras := map[base.HandlerType]base.HandlerFactory{
		base.HandlerTypeOpenAI: capturingFactory(&stubHandler{}, &got),
	}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "my-proxy", Type: string(base.HandlerTypeOpenAI),
			BaseURL: "https://my-proxy.example.com/v1",
			Models:  []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	_, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got.Key != "" {
		t.Errorf("Key should NOT be injected for non-official BaseURL, got %q", got.Key)
	}
}

func TestService_SingleEnvFallback_UnknownBareModel_Passthrough(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "ft:gpt-4o:my-org:custom:id")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_MultiEnvFallback_ClaudeRoutesToAnthropic(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	base.RegisterHandler(base.HandlerTypeAnthropic, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "claude-3-5-sonnet-20241022")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_MultiEnvFallback_GptRoutesToOpenAI(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	base.RegisterHandler(base.HandlerTypeAnthropic, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "gpt-4o-2024-08-06")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_MultiEnvFallback_UnknownPrefix_AmbiguityError(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	base.RegisterHandler(base.HandlerTypeAnthropic, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = s.Responses(context.Background(), &model.Request{Model: "llama-3.1-70b"})
	if err == nil {
		t.Fatal("expected ambiguity error for unknown model prefix with multiple wildcards")
	}
	if !strings.Contains(err.Error(), "provider/model") {
		t.Errorf("error should suggest provider/model syntax, got: %v", err)
	}
}

func TestService_EnvFallback_ProviderSlashModel_Passthrough(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeAnthropic, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp := chatViaService(t, s, "anthropic/some-future-model")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
}

func TestService_WildcardCandidate_HasCapabilityMetadata(t *testing.T) {
	base.RegisterHandler(base.HandlerTypeAnthropic, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return &stubHandler{}, nil
	})
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	meta := s.ModelMetadataFor("claude-sonnet-4.6")
	if meta.ContextWindow == 0 {
		t.Error("ContextWindow should be populated from LookupModel")
	}
	if meta.Streaming == nil || !*meta.Streaming {
		t.Error("Streaming should be true from LookupModel")
	}
}

func TestService_ExplicitConfig_StillRejectsUnknownModel(t *testing.T) {
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	s := newTestService(t, cfg, &stubHandler{})
	_, err := s.Responses(context.Background(), &model.Request{Model: "ft:gpt-4o:my-org:custom:id"})
	if err == nil {
		t.Fatal("expected error for unknown model with explicit (non-wildcard) config")
	}
}

func TestService_New_DoesNotMutateCaller(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env")
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "openai", Type: "mock",
			Models: []conf.ModelConfig{{ID: "gpt-4o"}},
		}},
	}
	original := cfg.Providers[0].Key // should be ""
	extras := map[base.HandlerType]base.HandlerFactory{"mock": stubFactory(&stubHandler{})}
	_, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cfg.Providers[0].Key != original {
		t.Errorf("New() mutated caller's config: Key changed from %q to %q", original, cfg.Providers[0].Key)
	}
}

func TestService_UnknownWildcardModel_NoPolyfill(t *testing.T) {
	var gotModel string
	h := &stubHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		gotModel = req.Model
		return &stubStream{resp: &model.Response{Content: "ok"}}, nil
	}}
	base.RegisterHandler(base.HandlerTypeOpenAI, func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return h, nil
	})
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")
	s, err := New(conf.ModelsConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	meta := s.ModelMetadataFor("ft:custom-model:org:name:id")
	if meta.Streaming != nil {
		t.Errorf("Streaming should be nil for unknown model, got %v", *meta.Streaming)
	}
	if meta.Thinking != nil {
		t.Errorf("Thinking should be nil for unknown model, got %v", *meta.Thinking)
	}
	resp := chatViaService(t, s, "ft:custom-model:org:name:id")
	if resp.Content != "ok" {
		t.Errorf("Content: got %q", resp.Content)
	}
	if gotModel != "ft:custom-model:org:name:id" {
		t.Errorf("forwarded model: got %q", gotModel)
	}
}
