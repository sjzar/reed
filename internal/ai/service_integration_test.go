package ai

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/model"
)

// integrationHandler is a configurable test handler for integration tests.
type integrationHandler struct {
	responsesFn func(ctx context.Context, req *model.Request) (model.ResponseStream, error)
}

func (h *integrationHandler) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	if h.responsesFn != nil {
		return h.responsesFn(ctx, req)
	}
	return &integrationStream{resp: &model.Response{Content: "ok"}}, nil
}

type integrationStream struct {
	resp *model.Response
	done bool
}

func (s *integrationStream) Next(_ context.Context) (model.StreamEvent, error) {
	if s.done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
	}
	s.done = true
	if s.resp.Content != "" {
		return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: s.resp.Content}, nil
	}
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *integrationStream) Close() error              { return nil }
func (s *integrationStream) Response() *model.Response { return s.resp }

func integrationFactory(h base.RoundTripper) base.HandlerFactory {
	return func(_ conf.ProviderConfig) (base.RoundTripper, error) {
		return h, nil
	}
}

// TestIntegration_FailoverChain tests the full failover pipeline:
// provider 1 fails → penalty → provider 2 succeeds.
func TestIntegration_FailoverChain(t *testing.T) {
	callCount := 0
	var calledProviders []string

	h := &integrationHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		callCount++
		if callCount == 1 {
			calledProviders = append(calledProviders, "p1")
			return nil, &model.AIError{Kind: model.ErrServerError, StatusCode: 503, Message: "down"}
		}
		calledProviders = append(calledProviders, "p2")
		return &integrationStream{resp: &model.Response{Content: "from-p2"}}, nil
	}}

	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stream, err := s.Responses(context.Background(), &model.Request{Model: "m1", AgentID: "test"})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp.Content != "from-p2" {
		t.Errorf("Content: got %q, want from-p2", resp.Content)
	}
	if callCount != 2 {
		t.Errorf("callCount: got %d, want 2", callCount)
	}

	// Verify p1 was penalized
	if !s.penalty.isPenalized("p1", "m1") {
		t.Error("p1 should be penalized after 503")
	}
}

// TestIntegration_MiddlewareChain tests clone → timeout → handler pipeline.
// Verifies that the original request is not mutated (clone isolation).
func TestIntegration_MiddlewareChain(t *testing.T) {
	var receivedReq *model.Request
	h := &integrationHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		receivedReq = req
		return &integrationStream{resp: &model.Response{Content: "ok"}}, nil
	}}

	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "p1", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{
				ID:        "m1",
				Thinking:  boolPtr(false),
				MaxTokens: 4096,
			}},
		}},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	originalMsg := model.Message{
		Role: model.RoleAssistant,
		Content: []model.Content{
			{Type: model.ContentTypeThinking, Text: "thinking", Signature: "sig"},
			{Type: model.ContentTypeText, Text: "hello"},
		},
	}
	req := &model.Request{
		Model:     "m1",
		AgentID:   "test",
		Thinking:  "high",
		MaxTokens: 8192,
		Messages:  []model.Message{originalMsg},
	}

	stream, err := s.Responses(context.Background(), req)
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	_, _ = model.DrainStream(context.Background(), stream)

	// Verify handler received the request
	if receivedReq == nil {
		t.Fatal("handler did not receive request")
	}

	// Verify original request was NOT mutated (clone middleware)
	if req.Thinking == "" {
		t.Error("Original request Thinking should not be mutated")
	}
	if req.MaxTokens != 8192 {
		t.Error("Original request MaxTokens should not be mutated")
	}
	if req.Messages[0].Content[0].Type != model.ContentTypeThinking {
		t.Error("Original message Content should not be mutated")
	}
}

// TestIntegration_ContextWindowFilter tests that large requests skip small models.
func TestIntegration_ContextWindowFilter(t *testing.T) {
	var calledModel string
	h := &integrationHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		calledModel = req.Model
		return &integrationStream{resp: &model.Response{Content: "ok"}}, nil
	}}

	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1",
				Models: []conf.ModelConfig{{ID: "small", ContextWindow: 8000}}},
			{ID: "p2", Type: "mock", Key: "k2",
				Models: []conf.ModelConfig{{ID: "big", ContextWindow: 200000}}},
		},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stream, err := s.Responses(context.Background(), &model.Request{
		Model:           "small,big",
		AgentID:         "test",
		EstimatedTokens: 100000,
	})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	_, _ = model.DrainStream(context.Background(), stream)

	// Should skip "small" and use "big"
	if calledModel != "big" {
		t.Errorf("calledModel: got %q, want big", calledModel)
	}
}

// TestIntegration_PenaltySkipsProvider tests penalty box skipping.
func TestIntegration_PenaltySkipsProvider(t *testing.T) {
	var calledModels []string
	h := &integrationHandler{responsesFn: func(_ context.Context, req *model.Request) (model.ResponseStream, error) {
		calledModels = append(calledModels, req.Model)
		return &integrationStream{resp: &model.Response{Content: "ok"}}, nil
	}}

	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{
			{ID: "p1", Type: "mock", Key: "k1", Models: []conf.ModelConfig{{ID: "m1"}}},
			{ID: "p2", Type: "mock", Key: "k2", Models: []conf.ModelConfig{{ID: "m1"}}},
		},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Penalize p1
	s.penalty.penalize("p1", "m1", time.Hour)

	stream, err := s.Responses(context.Background(), &model.Request{Model: "m1", AgentID: "test"})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}
	_, _ = model.DrainStream(context.Background(), stream)

	if len(calledModels) != 1 {
		t.Errorf("calledModels: got %v, want 1 call", calledModels)
	}
}

// TestIntegration_ServiceResponses tests the stream-first entry point end-to-end.
func TestIntegration_ServiceResponses(t *testing.T) {
	h := &integrationHandler{responsesFn: func(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
		return &integrationStream{resp: &model.Response{Content: "streamed"}}, nil
	}}

	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "p1", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{ID: "m1"}},
		}},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	stream, err := s.Responses(context.Background(), &model.Request{Model: "m1"})
	if err != nil {
		t.Fatalf("Responses: %v", err)
	}

	resp, err := model.DrainStream(context.Background(), stream)
	if err != nil {
		t.Fatalf("DrainStream: %v", err)
	}
	if resp.Content != "streamed" {
		t.Errorf("Content: got %q, want streamed", resp.Content)
	}
}

// TestIntegration_ModelMetadataFor tests metadata resolution.
func TestIntegration_ModelMetadataFor(t *testing.T) {
	h := &integrationHandler{}
	cfg := conf.ModelsConfig{
		Providers: []conf.ProviderConfig{{
			ID: "p1", Type: "mock", Key: "k1",
			Models: []conf.ModelConfig{{
				ID:            "m1",
				ContextWindow: 128000,
				MaxTokens:     4096,
				Thinking:      boolPtr(true),
			}},
		}},
	}
	extras := map[base.HandlerType]base.HandlerFactory{"mock": integrationFactory(h)}
	s, err := New(cfg, extras)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	meta := s.ModelMetadataFor("m1")
	if meta.ContextWindow != 128000 {
		t.Errorf("ContextWindow: got %d", meta.ContextWindow)
	}
	if meta.MaxTokens != 4096 {
		t.Errorf("MaxTokens: got %d", meta.MaxTokens)
	}
	if !model.BoolVal(meta.Thinking) {
		t.Error("Thinking should be true")
	}
}
