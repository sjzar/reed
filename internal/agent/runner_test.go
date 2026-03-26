package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/memory"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/internal/tool"
)

// mockSkillProvider implements SkillProvider for tests.
type mockSkillProvider struct {
	infos map[string]skill.SkillInfo
}

func (m *mockSkillProvider) ListAndMount(_ string, ids []string) ([]skill.SkillInfo, error) {
	var result []skill.SkillInfo
	for _, id := range ids {
		if info, ok := m.infos[id]; ok {
			result = append(result, info)
		}
	}
	return result, nil
}

// --- Mock implementations ---

type mockEngineAI struct {
	client *mockEngineClient
}

func (m *mockEngineAI) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	return m.client.Responses(ctx, req)
}

func (m *mockEngineAI) ModelMetadataFor(_ string) model.ModelMetadata {
	return m.client.ModelMetadata()
}

type mockEngineClient struct {
	responses []*model.Response
	callIdx   int
}

func (m *mockEngineClient) Chat(_ context.Context, _ *model.Request) (*model.Response, error) {
	if m.callIdx >= len(m.responses) {
		return &model.Response{Content: "done", StopReason: model.StopReasonEnd}, nil
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockEngineClient) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resp, err := m.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp}, nil
}

func (m *mockEngineClient) ModelMetadata() model.ModelMetadata { return model.ModelMetadata{} }

type mockEngineMemory struct {
	content string
}

func (m *mockEngineMemory) BeforeRun(_ context.Context, _ memory.RunContext) (memory.MemoryResult, error) {
	return memory.MemoryResult{Content: m.content}, nil
}
func (m *mockEngineMemory) AfterRun(_ context.Context, _ memory.RunContext, _ []model.Message) error {
	return nil
}

func newTestRunner(client *mockEngineClient, extraTools ...tool.Tool) *Runner {
	dir := "/tmp/reed-test-engine-" + randomSuffix()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	for _, t := range extraTools {
		_ = reg.Register(t)
	}
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	return New(&mockEngineAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)
}

func randomSuffix() string {
	return "test" // simplified for tests
}

func TestEngineRunHappyPath(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "Hello! I can help you.", StopReason: model.StopReasonEnd, Usage: model.Usage{Input: 10, Output: 5, Total: 15}},
		},
	}
	engine := newTestRunner(client)

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "key1",
		Prompt: "Hello",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Output != "Hello! I can help you." {
		t.Errorf("output: got %q", resp.Output)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
	if resp.Iterations != 1 {
		t.Errorf("iterations: got %d", resp.Iterations)
	}
}

func TestEngineRunToolCall(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{
				Content:    "",
				StopReason: model.StopReasonToolUse,
				ToolCalls:  []model.ToolCall{{ID: "tc1", Name: "echo", Arguments: map[string]any{"msg": "hi"}}},
				Usage:      model.Usage{Input: 10, Output: 5, Total: 15},
			},
			{Content: "Tool returned: echoed", StopReason: model.StopReasonEnd, Usage: model.Usage{Input: 20, Output: 10, Total: 30}},
		},
	}
	engine := newTestRunner(client, &testTool{name: "echo", result: "echoed"})

	// Register a tool

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "key2",
		Prompt: "Use echo tool",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Iterations != 2 {
		t.Errorf("iterations: got %d, want 2", resp.Iterations)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
}

func TestEngineRunMaxIterations(t *testing.T) {
	// Client always returns tool calls, forcing max iterations
	client := &mockEngineClient{
		responses: []*model.Response{
			{StopReason: model.StopReasonToolUse, ToolCalls: []model.ToolCall{{ID: "tc1", Name: "echo", Arguments: map[string]any{}}}, Usage: model.Usage{Total: 5}},
			{StopReason: model.StopReasonToolUse, ToolCalls: []model.ToolCall{{ID: "tc2", Name: "echo", Arguments: map[string]any{}}}, Usage: model.Usage{Total: 5}},
			{StopReason: model.StopReasonToolUse, ToolCalls: []model.ToolCall{{ID: "tc3", Name: "echo", Arguments: map[string]any{}}}, Usage: model.Usage{Total: 5}},
		},
	}
	engine := newTestRunner(client, &testTool{name: "echo", result: "ok"})

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "key3",
		Prompt: "loop", MaxIterations: 2,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopMaxIter {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopMaxIter)
	}
}

func TestEngineRunCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := &mockEngineClient{}
	engine := newTestRunner(client)

	resp, err := engine.Run(ctx, &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "key4",
		Prompt: "hello",
	})
	if err != nil {
		// Context cancel during session acquire is acceptable
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected context error, got: %v", err)
		}
		return
	}
	if resp != nil && resp.StopReason != model.AgentStopCanceled {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
}

func TestEngineRunWithEvents(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	engine := newTestRunner(client)

	b := bus.New()
	defer b.Close()
	stepRunID := "sr_evt_001"
	sub := b.Subscribe(bus.StepOutputTopic(stepRunID), 64)
	defer sub.Unsubscribe()

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "key5",
		Prompt:    "hello",
		Bus:       b,
		StepRunID: stepRunID,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Output != "done" {
		t.Errorf("output: got %q", resp.Output)
	}
	// Drain messages — expect at least 2 (llm_start status + llm_end status)
	var msgs []bus.Message
	for {
		select {
		case msg := <-sub.Ch():
			msgs = append(msgs, msg)
		default:
			goto done
		}
	}
done:
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 bus messages, got %d", len(msgs))
	}
	// Verify message types: first and last should be "status" (llm_start/llm_end)
	if msgs[0].Type != "status" {
		t.Errorf("first message type = %q, want status", msgs[0].Type)
	}
	if msgs[len(msgs)-1].Type != "status" {
		t.Errorf("last message type = %q, want status", msgs[len(msgs)-1].Type)
	}
	// Verify payload shape
	for _, m := range msgs {
		if m.Type == "status" {
			if _, ok := m.Payload.(bus.StatusPayload); !ok {
				t.Errorf("status message payload type = %T, want bus.StatusPayload", m.Payload)
			}
		}
		if m.Type == "text" {
			if _, ok := m.Payload.(bus.TextPayload); !ok {
				t.Errorf("text message payload type = %T, want bus.TextPayload", m.Payload)
			}
		}
	}
}

// testTool is a simple tool for engine tests, implementing tool.Tool.
type testTool struct {
	name   string
	result string
}

func (t *testTool) Def() model.ToolDef {
	return model.ToolDef{Name: t.name, Description: "test tool", InputSchema: map[string]any{"type": "object"}}
}
func (t *testTool) Prepare(_ context.Context, req tool.CallRequest) (*tool.PreparedCall, error) {
	return &tool.PreparedCall{
		ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs,
		Plan: tool.ExecutionPlan{Mode: tool.ExecModeSync, Policy: tool.ParallelSafe},
	}, nil
}
func (t *testTool) Execute(_ context.Context, _ *tool.PreparedCall) (*tool.Result, error) {
	return tool.TextResult(t.result), nil
}

// --- F4: Empty tools = no tools (default-deny) ---

func TestEngineEmptyToolsDefaultDeny(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "no tools available", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	engine := newTestRunner(client, &testTool{name: "secret_tool", result: "secret"})
	// Register a tool but don't request it

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "f4",
		Prompt: "hello",
		Tools:  []string{}, // explicit empty = no tools (default-deny)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
}

// --- F6: WaitForAsyncTasks=false returns AgentStopAsync ---

func TestEngineAsyncPendingNoWait(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{
				StopReason: model.StopReasonToolUse,
				ToolCalls:  []model.ToolCall{{ID: "tc1", Name: "slow_job", Arguments: map[string]any{}}},
				Usage:      model.Usage{Total: 10},
			},
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}},
		},
	}

	dir := t.TempDir()
	idGen := &mockSessionIDGen{}
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, idGen)
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	engine := New(&mockEngineAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)

	// Pre-resolve the session to get the actual session ID, then register a pending job
	// The first call to resolve will create "test-session-0001"
	_ = sess.RegisterPendingJob(context.Background(), "test-session-0001", "async-job-1")

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace:         "test",
		AgentID:           "agent1",
		SessionKey:        "f6",
		Prompt:            "do something",
		WaitForAsyncTasks: false,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// After the second LLM call returns no tool calls, pending exists but WaitForAsyncTasks=false
	if resp.StopReason != model.AgentStopAsync {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopAsync)
	}
}

// --- F5: System prompt rebuilt fresh each run ---

func TestEngineSystemPromptRefreshed(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "turn1", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
			{Content: "turn2", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	engine := New(&scriptedAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)

	// Turn 1 with system prompt A
	_, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "f5",
		Prompt:       "hello",
		SystemPrompt: "You are assistant A",
	})
	if err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// Turn 2 with system prompt B — should use B, not A
	_, err = engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "f5",
		Prompt:       "hello again",
		SystemPrompt: "You are assistant B",
	})
	if err != nil {
		t.Fatalf("turn 2: %v", err)
	}

	// Verify the second Chat call received system prompt B
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) < 2 {
		t.Fatalf("expected 2 calls, got %d", len(client.calls))
	}
	msgs := client.calls[1].Messages
	if len(msgs) == 0 {
		t.Fatal("no messages in second call")
	}
	if msgs[0].Role != model.RoleSystem {
		t.Fatalf("first message not system: %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].TextContent(), "You are assistant B") {
		t.Errorf("system prompt not refreshed: got %q", msgs[0].TextContent())
	}
}

// --- F3: Context overflow with compressor triggers retry ---

type mockCompressor struct {
	called bool
}

func (m *mockCompressor) Compress(_ context.Context, _ []model.Message) (string, error) {
	m.called = true
	return "compacted summary", nil
}

func TestEngineOverflowCompactionRetry(t *testing.T) {
	callCount := 0
	overflowOnce := &overflowOnceClient{callCount: &callCount}

	comp := &mockCompressor{}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{}, session.WithCompressor(comp))
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	// Pre-seed session with enough messages so compaction has something to compress
	// (KeepRecentN=4 in engine, so we need >4 messages for compression to trigger)
	// Acquire with key "f3" will generate "test-session-0001"
	ctx := context.Background()
	seedMsgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "old q1"),
		model.NewTextMessage(model.RoleAssistant, "old a1"),
		model.NewTextMessage(model.RoleUser, "old q2"),
		model.NewTextMessage(model.RoleAssistant, "old a2"),
		model.NewTextMessage(model.RoleUser, "old q3"),
		model.NewTextMessage(model.RoleAssistant, "old a3"),
	}
	_ = sess.AppendMessages(ctx, "test-session-0001", seedMsgs)

	engine := New(&scriptedAI{client: overflowOnce}, sess, WrapToolService(toolSvc), nil, mem)

	resp, err := engine.Run(ctx, &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "f3",
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !comp.called {
		t.Error("compressor was not called")
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopComplete)
	}
}

// overflowOnceClient returns overflow on first call, then succeeds.
type overflowOnceClient struct {
	callCount *int
}

func (c *overflowOnceClient) Chat(_ context.Context, _ *model.Request) (*model.Response, error) {
	*c.callCount++
	if *c.callCount == 1 {
		return nil, &model.AIError{Kind: model.ErrContextOverflow, Message: "too long"}
	}
	return &model.Response{Content: "recovered", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}}, nil
}
func (c *overflowOnceClient) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	resp, err := c.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp}, nil
}

func (c *overflowOnceClient) ModelMetadata() model.ModelMetadata {
	return model.ModelMetadata{ContextWindow: 128000}
}

func TestEngine_UnknownToolID_Skipped(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd},
		},
	}
	engine := newTestRunner(client, &testTool{name: "real_tool", result: "ok"})

	// Unknown tool IDs are silently skipped via ListToolsLenient —
	// the run proceeds without them (skill-derived IDs may reference
	// provider-specific names not in the local registry).
	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "test", AgentID: "agent1", SessionKey: "unknown_tool",
		Prompt: "hello",
		Tools:  []string{"nonexistent"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "done" {
		t.Errorf("output = %q, want done", resp.Output)
	}
}

// --- Phase 3 tests ---

func TestEngineSessionIDDirect(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "hello", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	routeStore := &mockRouteStore{}
	idGen := &mockSessionIDGen{}
	sess := session.New(dir, routeStore, &mockSessionClock{}, idGen)

	// Pre-create a session via Acquire so AcquireByID can find it
	sid, rel, err := sess.Acquire(context.Background(), "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	rel()

	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}
	engine := New(&mockEngineAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent",
		SessionID: sid,
		Prompt:    "hello via session_id",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}
}

func TestEngineSessionKeyAndIDMutualExclusion(t *testing.T) {
	client := &mockEngineClient{}
	engine := newTestRunner(client)

	_, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace:  "ns",
		AgentID:    "agent",
		SessionKey: "key1",
		SessionID:  "sid1",
		Prompt:     "hello",
	})
	if err == nil {
		t.Fatal("expected error for both SessionKey and SessionID set")
	}
	if err.Error() != "only one of SessionKey and SessionID may be set" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEngineTokenBudget(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "big output", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 100}},
		},
	}
	engine := newTestRunner(client)

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "tb1",
		Prompt:    "hello",
		MaxTokens: 50,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopMaxTokens {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopMaxTokens)
	}
}

func TestEngineRepetitionDetection(t *testing.T) {
	responses := make([]*model.Response, 10)
	for i := range responses {
		responses[i] = &model.Response{
			StopReason: model.StopReasonToolUse,
			ToolCalls:  []model.ToolCall{{ID: fmt.Sprintf("tc%d", i), Name: "echo", Arguments: map[string]any{"msg": "same"}}},
			Usage:      model.Usage{Total: 5},
		}
	}
	client := &mockEngineClient{responses: responses}
	engine := newTestRunner(client, &testTool{name: "echo", result: "same output"})

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "rep1",
		Prompt:        "loop forever",
		Tools:         []string{"echo"},
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopLoop {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopLoop)
	}
	if resp.Iterations > 5 {
		t.Errorf("iterations: got %d, expected ≤5 (window=3)", resp.Iterations)
	}
}

func TestEngineRepetitionDetectionByNameAndArgs(t *testing.T) {
	// Same tool name + same args + different output should still detect repetition
	// because Name+Input hash is the same across rounds
	responses := make([]*model.Response, 10)
	for i := range responses {
		responses[i] = &model.Response{
			StopReason: model.StopReasonToolUse,
			ToolCalls:  []model.ToolCall{{ID: fmt.Sprintf("tc%d", i), Name: "fetch", Arguments: map[string]any{"url": "http://example.com"}}},
			Usage:      model.Usage{Total: 5},
		}
	}
	client := &mockEngineClient{responses: responses}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	mem := &mockEngineMemory{}

	toolSvc := tool.NewService(reg)
	eng := New(&mockEngineAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)

	callCount := 0
	_ = reg.Register(&dynamicTool{name: "fetch", fn: func() string {
		callCount++
		return fmt.Sprintf("result-%d", callCount) // different output each time
	}})

	resp, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "rep-name-args",
		Prompt:        "loop",
		Tools:         []string{"fetch"},
		MaxIterations: 10,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Different outputs but same name+args — should NOT trigger repetition
	// (the hash includes output, so different outputs = different round hashes)
	if resp.StopReason == model.AgentStopLoop {
		t.Errorf("should not detect loop when outputs differ")
	}
}

func TestEngineSkillInjection(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "used skill", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	skills := &mockSkillProvider{infos: map[string]skill.SkillInfo{
		"code-review": {ID: "code-review", Name: "code-review", Description: "Code review skill", MountDir: "/tmp/test-skills/code-review"},
	}}
	mem := &mockEngineMemory{}

	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), skills, mem)

	resp, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "skill1",
		Prompt:  "review this code",
		Skills:  []string{"code-review"},
		RunRoot: "/tmp/test-skills",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}

	// Verify the system message sent to the LLM contains the skill summary
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	msgs := client.calls[0].Messages
	found := false
	for _, msg := range msgs {
		if msg.Role == model.RoleSystem && strings.Contains(msg.TextContent(), "code-review") {
			found = true
			break
		}
	}
	if !found {
		t.Error("skill summary not found in system prompt")
	}
}

// dynamicTool returns different results each call.
type dynamicTool struct {
	name string
	fn   func() string
}

func (t *dynamicTool) Def() model.ToolDef {
	return model.ToolDef{Name: t.name, Description: "dynamic test tool", InputSchema: map[string]any{"type": "object"}}
}
func (t *dynamicTool) Prepare(_ context.Context, req tool.CallRequest) (*tool.PreparedCall, error) {
	return &tool.PreparedCall{
		ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs,
		Plan: tool.ExecutionPlan{Mode: tool.ExecModeSync, Policy: tool.ParallelSafe},
	}, nil
}
func (t *dynamicTool) Execute(_ context.Context, _ *tool.PreparedCall) (*tool.Result, error) {
	return tool.TextResult(t.fn()), nil
}

func TestEngineMemoryAfterRun(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()

	mem := &trackingMemory{}
	engine := New(&mockEngineAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	_, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "mem1",
		Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !mem.afterCalled {
		t.Error("memory.AfterRun was not called")
	}
}

type trackingMemory struct {
	afterCalled bool
}

func (m *trackingMemory) BeforeRun(_ context.Context, _ memory.RunContext) (memory.MemoryResult, error) {
	return memory.MemoryResult{}, nil
}
func (m *trackingMemory) AfterRun(_ context.Context, _ memory.RunContext, _ []model.Message) error {
	m.afterCalled = true
	return nil
}

func TestEngineRateLimitRetry(t *testing.T) {
	callCount := 0
	rateLimitOnce := &rateLimitOnceClient{callCount: &callCount}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	mem := &mockEngineMemory{}

	engine := New(&scriptedAI{client: rateLimitOnce}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	resp, err := engine.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "rl1",
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopComplete)
	}
	if *rateLimitOnce.callCount != 2 {
		t.Errorf("expected 2 calls (1 rate-limited + 1 success), got %d", *rateLimitOnce.callCount)
	}
}

type rateLimitOnceClient struct {
	callCount *int
}

func (c *rateLimitOnceClient) Responses(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
	*c.callCount++
	if *c.callCount == 1 {
		return nil, &model.AIError{Kind: model.ErrRateLimit, Message: "rate limited", RetryAfter: time.Millisecond, Retryable: true}
	}
	resp := &model.Response{Content: "recovered", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}}
	return &singleResponseStream{resp: resp}, nil
}

func (c *rateLimitOnceClient) ModelMetadata() model.ModelMetadata {
	return model.ModelMetadata{}
}

// TestWrappedAIErrorHandled verifies that errors.As correctly unwraps
// a *model.AIError wrapped via fmt.Errorf("...: %w", aiErr).
// This is a regression test for the asAIError → errors.As fix.
func TestWrappedAIErrorHandled(t *testing.T) {
	callCount := 0
	wrappedClient := &wrappedAIErrorClient{callCount: &callCount}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	mem := &mockEngineMemory{}

	eng := New(&scriptedAI{client: wrappedClient}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	resp, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "wrapped1",
		Prompt: "test",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q, want %q", resp.StopReason, model.AgentStopComplete)
	}
	if *wrappedClient.callCount != 2 {
		t.Errorf("expected 2 calls (1 wrapped rate-limit + 1 success), got %d", *wrappedClient.callCount)
	}
}

type wrappedAIErrorClient struct {
	callCount *int
}

func (c *wrappedAIErrorClient) Responses(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
	*c.callCount++
	if *c.callCount == 1 {
		aiErr := &model.AIError{Kind: model.ErrRateLimit, Message: "rate limited", RetryAfter: time.Millisecond, Retryable: true}
		return nil, fmt.Errorf("provider middleware: %w", aiErr)
	}
	resp := &model.Response{Content: "recovered", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}}
	return &singleResponseStream{resp: resp}, nil
}

func (c *wrappedAIErrorClient) ModelMetadata() model.ModelMetadata {
	return model.ModelMetadata{}
}

// --- Tool group resolution tests ---

func TestEngineNilToolsGivesCore(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&testTool{name: "core_tool", result: "ok"}, tool.GroupCore)
	_ = reg.Register(&testTool{name: "optional_tool", result: "ok"})
	mem := &mockEngineMemory{}

	eng := New(&mockEngineAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "core1",
		Prompt: "hello",
		// Tools is nil → should get core tools only
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestEngineEmptyToolsExplicitDeny(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&testTool{name: "core_tool", result: "ok"}, tool.GroupCore)
	mem := &mockEngineMemory{}

	eng := New(&mockEngineAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "deny1",
		Prompt: "hello",
		Tools:  []string{}, // explicit empty → 0 tools
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestEngineExplicitToolList(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&testTool{name: "core_tool", result: "ok"}, tool.GroupCore)
	_ = reg.Register(&testTool{name: "special_tool", result: "ok"})
	mem := &mockEngineMemory{}

	eng := New(&mockEngineAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, mem)

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "explicit1",
		Prompt: "hello",
		Tools:  []string{"special_tool"}, // only special, not core
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// --- Profile skills tests ---

// mockProfileProvider implements ProfileProvider for testing.
type mockProfileProvider struct {
	profile *ResolvedProfile
	err     error
}

func (m *mockProfileProvider) ResolveProfile(_ context.Context, _ string) (*ResolvedProfile, error) {
	return m.profile, m.err
}

func TestEngineProfileDefaultSkills(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "reviewed", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	skills := &mockSkillProvider{infos: map[string]skill.SkillInfo{
		"code-review": {ID: "code-review", Name: "code-review", Description: "Code review skill", MountDir: "/tmp/test-skills/code-review"},
	}}
	mem := &mockEngineMemory{}
	profile := &mockProfileProvider{profile: &ResolvedProfile{SkillIDs: []string{"code-review"}}}

	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), skills, mem, WithProfile(profile))

	resp, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "prof-skill1",
		Prompt:  "review this",
		Profile: "dev",
		RunRoot: "/tmp/test-skills",
		// Skills is empty → should fall back to profile skills
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.StopReason != model.AgentStopComplete {
		t.Errorf("stop reason: got %q", resp.StopReason)
	}

	// Verify skill summary appears in system prompt
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.calls) == 0 {
		t.Fatal("no calls recorded")
	}
	msgs := client.calls[0].Messages
	found := false
	for _, msg := range msgs {
		if msg.Role == model.RoleSystem && strings.Contains(msg.TextContent(), "code-review") {
			found = true
			break
		}
	}
	if !found {
		t.Error("profile skill summary not found in system prompt")
	}
}

func TestEngineProfileSkillsOverriddenByExplicit(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	skills := &mockSkillProvider{infos: map[string]skill.SkillInfo{
		"code-review": {ID: "code-review", Name: "code-review", Description: "Code review skill", MountDir: "/tmp/test-skills/code-review"},
		"other-skill": {ID: "other-skill", Name: "other-skill", Description: "Other skill", MountDir: "/tmp/test-skills/other-skill"},
	}}
	mem := &mockEngineMemory{}
	profile := &mockProfileProvider{profile: &ResolvedProfile{SkillIDs: []string{"code-review"}}}

	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), skills, mem, WithProfile(profile))

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "prof-skill2",
		Prompt:  "do stuff",
		Profile: "dev",
		Skills:  []string{"other-skill"}, // explicit → overrides profile
		RunRoot: "/tmp/test-skills",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	msgs := client.calls[0].Messages
	foundOther := false
	foundReview := false
	for _, msg := range msgs {
		text := msg.TextContent()
		if msg.Role == model.RoleSystem {
			if strings.Contains(text, "other-skill") {
				foundOther = true
			}
			if strings.Contains(text, "code-review") {
				foundReview = true
			}
		}
	}
	if !foundOther {
		t.Error("explicit skill summary not found in system prompt")
	}
	if foundReview {
		t.Error("profile skill summary should NOT appear when explicit skills are set")
	}
}

func TestEngineProfileResolveError(t *testing.T) {
	client := &mockEngineClient{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	mem := &mockEngineMemory{}
	profile := &mockProfileProvider{err: fmt.Errorf("profile not found")}

	eng := New(&mockEngineAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, mem, WithProfile(profile))

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "prof-err",
		Prompt:  "hello",
		Profile: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for failed profile resolve")
	}
	if !strings.Contains(err.Error(), "resolve profile") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Fix 1: Full profile Cwd fallback ---

// cwdCaptureTool captures the RuntimeContext.Cwd it receives during Execute.
type cwdCaptureTool struct {
	name        string
	capturedCwd string
}

func (t *cwdCaptureTool) Def() model.ToolDef {
	return model.ToolDef{Name: t.name, Description: "captures cwd", InputSchema: map[string]any{"type": "object"}}
}
func (t *cwdCaptureTool) Prepare(_ context.Context, req tool.CallRequest) (*tool.PreparedCall, error) {
	return &tool.PreparedCall{
		ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs,
		Context: req.Context,
		Plan:    tool.ExecutionPlan{Mode: tool.ExecModeSync, Policy: tool.ParallelSafe},
	}, nil
}
func (t *cwdCaptureTool) Execute(_ context.Context, call *tool.PreparedCall) (*tool.Result, error) {
	t.capturedCwd = call.Context.Cwd
	return tool.TextResult("ok"), nil
}

func TestEngineFullProfileCwdFallback(t *testing.T) {
	runRoot := t.TempDir()
	normalizedRunRoot, err := security.Canonicalize(runRoot)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	captureTool := &cwdCaptureTool{name: "capture"}
	client := &scriptedClient{
		responses: []*model.Response{
			{
				StopReason: model.StopReasonToolUse,
				ToolCalls:  []model.ToolCall{{ID: "tc1", Name: "capture", Arguments: map[string]any{}}},
				Usage:      model.Usage{Total: 10},
			},
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 5}},
		},
	}

	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	_ = reg.Register(captureTool)
	toolSvc := tool.NewService(reg)
	mem := &mockEngineMemory{}

	eng := New(&scriptedAI{client: client}, sess, WrapToolService(toolSvc), nil, mem)

	_, err = eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace:         "ns",
		AgentID:           "agent",
		SessionKey:        "cwd-fallback",
		Prompt:            "use capture",
		Tools:             []string{"capture"},
		Cwd:               "",
		RunRoot:           runRoot,
		ToolAccessProfile: "full",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if captureTool.capturedCwd != normalizedRunRoot {
		t.Errorf("expected Cwd=%q, got %q", normalizedRunRoot, captureTool.capturedCwd)
	}
}

// --- Fix 5: Inbox parse error → error message ---

func TestInboxEventsToToolMessages_MalformedPayload(t *testing.T) {
	entries := []model.SessionEntry{
		model.NewCustomSessionEntry("inbox", map[string]any{
			"payload": `not valid json at all`,
		}),
	}
	msgs := inboxEventsToMessages(entries)
	// No toolCallID can be extracted from completely invalid JSON,
	// so no synthetic tool message is injected (would break LLM API).
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages (no toolCallID), got %d", len(msgs))
	}
}

func TestInboxEventsToToolMessages_MalformedWithToolCallID(t *testing.T) {
	// Partial JSON that has toolCallID but fails full parse (missing content field type)
	entries := []model.SessionEntry{
		model.NewCustomSessionEntry("inbox", map[string]any{
			"payload": `{"toolCallID":"tc99","content":"not-an-array"}`,
		}),
	}
	msgs := inboxEventsToMessages(entries)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !msgs[0].IsError {
		t.Error("expected IsError=true")
	}
	if msgs[0].ToolCallID != "tc99" {
		t.Errorf("expected toolCallID=tc99, got %q", msgs[0].ToolCallID)
	}
}

// --- DefaultSystemPrompt fallback ---

func TestEngineDefaultSystemPromptFallback(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, &mockEngineMemory{})

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "default-prompt",
		Prompt: "hello",
		// No SystemPrompt, no Profile → should use DefaultSystemPrompt
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	msgs := client.calls[0].Messages
	if len(msgs) == 0 || msgs[0].Role != model.RoleSystem {
		t.Fatal("expected system message")
	}
	if !strings.Contains(msgs[0].TextContent(), DefaultSystemPrompt) {
		t.Errorf("expected DefaultSystemPrompt in system message, got: %q", msgs[0].TextContent())
	}
}

// --- Profile SystemPrompt priority ---

func TestEngineProfileSystemPromptPriority(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	profile := &mockProfileProvider{profile: &ResolvedProfile{SystemPrompt: "Profile prompt"}}
	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, &mockEngineMemory{}, WithProfile(profile))

	// Case 1: No request SystemPrompt → use profile's
	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "prof-prompt1",
		Prompt:  "hello",
		Profile: "dev",
	})
	if err != nil {
		t.Fatalf("case 1: %v", err)
	}
	client.mu.Lock()
	msg1 := client.calls[0].Messages[0].TextContent()
	client.mu.Unlock()
	if !strings.Contains(msg1, "Profile prompt") {
		t.Errorf("case 1: expected profile prompt, got: %q", msg1)
	}

	// Case 2: Request SystemPrompt overrides profile
	_, err = eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "prof-prompt2",
		Prompt:       "hello",
		Profile:      "dev",
		SystemPrompt: "Request prompt",
	})
	if err != nil {
		t.Fatalf("case 2: %v", err)
	}
	client.mu.Lock()
	msg2 := client.calls[1].Messages[0].TextContent()
	client.mu.Unlock()
	if !strings.Contains(msg2, "Request prompt") {
		t.Errorf("case 2: expected request prompt, got: %q", msg2)
	}
	if strings.Contains(msg2, "Profile prompt") {
		t.Error("case 2: request prompt should override profile, not include both")
	}
}

// --- Subagent minimal mode filters memory/skills ---

func TestEngineSubagentMinimalMode(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	mem := &mockEngineMemory{content: "Remember: user prefers Go"}
	skills := &mockSkillProvider{
		infos: map[string]skill.SkillInfo{
			"code-review": {ID: "code-review", Description: "Reviews code", MountDir: "/tmp/skills/code-review"},
		},
	}
	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), skills, mem)

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "minimal-mode",
		Prompt:     "do stuff",
		Skills:     []string{"code-review"},
		RunRoot:    "/tmp/test-skills",
		PromptMode: "minimal",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	sysContent := client.calls[0].Messages[0].TextContent()

	// Memory should be filtered out in minimal mode
	if strings.Contains(sysContent, "Remember: user prefers Go") {
		t.Error("minimal mode should exclude memory sections")
	}
	// Skills should be filtered out in minimal mode
	if strings.Contains(sysContent, "code-review") {
		t.Error("minimal mode should exclude skill sections")
	}
	// System info should still be present
	if !strings.Contains(sysContent, "System Information") {
		t.Error("minimal mode should include system info")
	}
}

// --- Empty toolDefs should not generate tool section ---

func TestEngineEmptyToolDefsNoToolSection(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, &mockEngineMemory{})

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "no-tools",
		Prompt: "hello",
		Tools:  []string{}, // explicit empty → no tools
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	sysContent := client.calls[0].Messages[0].TextContent()
	if strings.Contains(sysContent, "Available Tools") {
		t.Error("empty tools should not produce tool summary section")
	}
}

// --- Profile requested but provider nil → error ---

func TestEngineProfileNilProviderError(t *testing.T) {
	client := &scriptedClient{
		responses: []*model.Response{
			{Content: "ok", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	dir := t.TempDir()
	sess := session.New(dir, &mockRouteStore{}, &mockSessionClock{}, &mockSessionIDGen{})
	reg := tool.NewRegistry()
	eng := New(&scriptedAI{client: client}, sess, WrapToolService(tool.NewService(reg)), nil, &mockEngineMemory{})
	// No WithProfile() → profile provider is nil

	_, err := eng.Run(context.Background(), &model.AgentRunRequest{
		Namespace: "ns", AgentID: "agent", SessionKey: "nil-profile",
		Prompt:  "hello",
		Profile: "dev", // requesting a profile with no provider
	})
	if err == nil {
		t.Fatal("expected error when profile requested but provider is nil")
	}
	if !strings.Contains(err.Error(), "no profile provider") {
		t.Errorf("expected 'no profile provider' in error, got: %v", err)
	}
}
