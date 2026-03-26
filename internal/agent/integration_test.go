package agent

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/tool"
)

// singleResponseStream wraps a Response as a single-event ResponseStream (for test mocks).
type singleResponseStream struct {
	resp *model.Response
	done bool
}

func (s *singleResponseStream) Next(_ context.Context) (model.StreamEvent, error) {
	if s.done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
	}
	s.done = true
	if s.resp.Content != "" {
		return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: s.resp.Content}, nil
	}
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *singleResponseStream) Close() error              { return nil }
func (s *singleResponseStream) Response() *model.Response { return s.resp }

// --- Session mock helpers for tests ---

// mockRouteStore is an in-memory RouteStore for testing.
type mockRouteStore struct {
	mu   sync.Mutex
	data map[string]*model.SessionRouteRow
}

func (s *mockRouteStore) Upsert(_ context.Context, row *model.SessionRouteRow) error {
	if s.data == nil {
		s.data = make(map[string]*model.SessionRouteRow)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := row.Namespace + "|" + row.AgentID + "|" + row.SessionKey
	s.data[key] = row
	return nil
}

func (s *mockRouteStore) Find(_ context.Context, ns, agent, sk string) (*model.SessionRouteRow, error) {
	if s.data == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ns + "|" + agent + "|" + sk
	r, ok := s.data[key]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (s *mockRouteStore) Delete(_ context.Context, ns, agent, sk string) error {
	return nil
}

func (s *mockRouteStore) FindBySessionID(_ context.Context, sessionID string) (*model.SessionRouteRow, error) {
	if s.data == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data {
		if r.CurrentSessionID == sessionID {
			return r, nil
		}
	}
	return nil, nil
}

type mockSessionClock struct{}

func (c *mockSessionClock) Now() time.Time { return time.Now() }

type mockSessionIDGen struct {
	mu    sync.Mutex
	count int
}

func (g *mockSessionIDGen) NewSessionID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.count++
	return fmt.Sprintf("test-session-%04d", g.count)
}

// --- Integration test helpers ---

// scriptedClient is a mock LLM client that returns pre-scripted responses.
type scriptedClient struct {
	mu        sync.Mutex
	responses []*model.Response
	idx       int
	calls     []model.Request // recorded requests
}

func (s *scriptedClient) Chat(_ context.Context, req *model.Request) (*model.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, *req)
	if s.idx >= len(s.responses) {
		return &model.Response{Content: "fallback", StopReason: model.StopReasonEnd}, nil
	}
	resp := s.responses[s.idx]
	s.idx++
	return resp, nil
}

func (s *scriptedClient) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resp, err := s.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp}, nil
}

func (s *scriptedClient) ModelMetadata() model.ModelMetadata {
	return model.ModelMetadata{ContextWindow: 128000}
}

// scriptedAI implements AIService by delegating to a mock LLM client.
type scriptedAI struct{ client scriptedAIClient }

// scriptedAIClient is a local interface for the mock clients used in integration tests.
type scriptedAIClient interface {
	Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error)
	ModelMetadata() model.ModelMetadata
}

func (s *scriptedAI) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	return s.client.Responses(ctx, req)
}

func (s *scriptedAI) ModelMetadataFor(_ string) model.ModelMetadata {
	return s.client.ModelMetadata()
}

func newIntegrationRunner(t *testing.T, client *scriptedClient, extraTools ...tool.Tool) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{}, session.WithInbox(dir))
	reg := tool.NewRegistry()
	for _, tt := range extraTools {
		_ = reg.Register(tt)
	}
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	e := New(&scriptedAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)
	return e, dir
}

// TestIntegrationMultiTurnConversation tests a multi-turn conversation with session persistence.
func TestIntegrationMultiTurnConversation(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "Hi! How can I help?", StopReason: model.StopReasonEnd, Usage: model.Usage{Input: 10, Output: 5, Total: 15}},
		},
	}
	engine, _ := newIntegrationRunner(t, client)

	// First turn
	resp1, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "conv1",
		Prompt: "Hello",
	})
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if resp1.Output != "Hi! How can I help?" {
		t.Errorf("turn 1 output: got %q", resp1.Output)
	}

	// Second turn — session should have history
	client.responses = append(client.responses, &model.Response{
		Content: "Sure, Go is great!", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 20},
	})
	resp2, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "conv1",
		Prompt: "Tell me about Go",
	})
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	if resp2.Output != "Sure, Go is great!" {
		t.Errorf("turn 2 output: got %q", resp2.Output)
	}
}

// TestIntegrationToolCallLoop tests the tool call → result → LLM cycle.
func TestIntegrationToolCallLoop(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{
				StopReason: model.StopReasonToolUse,
				ToolCalls:  []model.ToolCall{{ID: "tc1", Name: "calculator", Arguments: map[string]any{"expr": "2+2"}}},
				Usage:      model.Usage{Total: 10},
			},
			{Content: "The answer is 4", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 15}},
		},
	}
	engine, _ := newIntegrationRunner(t, client, &testTool{name: "calculator", result: "4"})

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "calc1",
		Prompt: "What is 2+2?",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Output != "The answer is 4" {
		t.Errorf("output: got %q", resp.Output)
	}
	if resp.Iterations != 2 {
		t.Errorf("iterations: got %d, want 2", resp.Iterations)
	}
}

// TestIntegrationSerialExecution tests that same-session requests are serialized.
func TestIntegrationSerialExecution(t *testing.T) {
	var concurrent int64
	var maxConcurrent int64

	// Override Chat to track concurrency
	slowClient := &concurrencyTrackingClient{
		concurrent:    &concurrent,
		maxConcurrent: &maxConcurrent,
		delay:         10 * time.Millisecond,
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	engine := New(&scriptedAI{client: slowClient}, sess, WrapToolService(toolSvc), nil, mem)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = engine.Run(context.Background(), &model.AgentRunRequest{
				Namespace: "ns", AgentID: "agent", SessionKey: "serial1",
				Prompt: "test",
			})
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&maxConcurrent) > 1 {
		t.Errorf("expected serial execution, max concurrent: %d", maxConcurrent)
	}
}

// concurrencyTrackingClient tracks concurrent Chat calls.
type concurrencyTrackingClient struct {
	concurrent    *int64
	maxConcurrent *int64
	delay         time.Duration
}

func (c *concurrencyTrackingClient) Chat(_ context.Context, _ *model.Request) (*model.Response, error) {
	val := atomic.AddInt64(c.concurrent, 1)
	for {
		old := atomic.LoadInt64(c.maxConcurrent)
		if val <= old || atomic.CompareAndSwapInt64(c.maxConcurrent, old, val) {
			break
		}
	}
	time.Sleep(c.delay)
	atomic.AddInt64(c.concurrent, -1)
	return &model.Response{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}}, nil
}

func (c *concurrencyTrackingClient) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp}, nil
}

func (c *concurrencyTrackingClient) ModelMetadata() model.ModelMetadata { return model.ModelMetadata{} }

// TestIntegrationContextOverflow tests context overflow handling.
func TestIntegrationContextOverflow(t *testing.T) {
	overflowClient := &overflowClient{fallbackResp: &model.Response{
		Content: "recovered", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10},
	}}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}
	engine := New(&scriptedAI{client: overflowClient}, sess, WrapToolService(toolSvc), nil, mem)

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "overflow1",
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Should get max_tokens stop reason since we return context overflow
	if resp.StopReason != model.AgentStopMaxTokens {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopMaxTokens)
	}
}

type overflowClient struct {
	fallbackResp *model.Response
}

func (c *overflowClient) Chat(_ context.Context, _ *model.Request) (*model.Response, error) {
	return nil, &model.AIError{Kind: model.ErrContextOverflow, Message: "context too long"}
}

func (c *overflowClient) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp}, nil
}

func (c *overflowClient) ModelMetadata() model.ModelMetadata { return model.ModelMetadata{} }
