package worker

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/sjzar/reed/internal/agent"
	"github.com/sjzar/reed/internal/ai"
	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/internal/tool"
)

// mockHandler is a test double for base.RoundTripper.
type mockHandler struct {
	responses []*model.Response
	calls     int
}

func (m *mockHandler) Responses(_ context.Context, _ *model.Request) (model.ResponseStream, error) {
	idx := m.calls
	m.calls++
	var resp *model.Response
	if idx < len(m.responses) {
		resp = m.responses[idx]
	} else {
		resp = &model.Response{Content: "done", StopReason: model.StopReasonEnd}
	}
	return &mockWorkerStream{resp: resp}, nil
}

type mockWorkerStream struct {
	resp *model.Response
	done bool
}

func (s *mockWorkerStream) Next(_ context.Context) (model.StreamEvent, error) {
	if s.done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
	}
	s.done = true
	if s.resp.Content != "" {
		return model.StreamEvent{Type: model.StreamEventTextDelta, Delta: s.resp.Content}, nil
	}
	return model.StreamEvent{Type: model.StreamEventDone}, io.EOF
}

func (s *mockWorkerStream) Close() error              { return nil }
func (s *mockWorkerStream) Response() *model.Response { return s.resp }

// newMockAIService creates an ai.Service backed by a mock RoundTripper.
func newMockAIService(h base.RoundTripper) *ai.Service {
	extras := map[base.HandlerType]base.HandlerFactory{
		"mock": func(_ conf.ProviderConfig) (base.RoundTripper, error) {
			return h, nil
		},
	}
	cfg := conf.ModelsConfig{
		Default: "test-model",
		Providers: []conf.ProviderConfig{
			{
				ID:   "mock",
				Type: "mock",
				Key:  "fake",
				Models: []conf.ModelConfig{
					{ID: "test-model", Name: "Test Model"},
				},
			},
		},
	}
	s, err := ai.New(cfg, extras)
	if err != nil {
		panic(fmt.Sprintf("newMockAIService: %v", err))
	}
	return s
}

// newTestRunner creates an agent.Runner with the given AI service.
func newTestRunner(t *testing.T, aiSvc *ai.Service) *agent.Runner {
	t.Helper()
	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	toolSvc := tool.NewService(reg)
	return agent.New(aiSvc, sessSvc, agent.WrapToolService(toolSvc), nil, nil)
}

func TestAgentWorker_Execute_MissingAgent(t *testing.T) {
	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{},
		},
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "nonexistent"},
	})
	if result.Status != model.StepFailed {
		t.Errorf("status: got %v, want %v", result.Status, model.StepFailed)
	}
}

func TestAgentWorker_Execute_MissingAgentKey(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "Task completed!", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 50}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{},
		},
		runner: eng,
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"prompt": "Fix the bug"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, want %v (error: %s)", result.Status, model.StepSucceeded, result.ErrorMessage)
	}
}

func TestAgentWorker_Execute_Success(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "Task completed!", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 50}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model", SystemPrompt: "You are a coder."},
			},
			Skills: map[string]model.SkillSpec{},
		},
		runner: eng,
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "Fix the bug"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, want %v (error: %s)", result.Status, model.StepSucceeded, result.ErrorMessage)
	}
	if result.Outputs["output"] != "Task completed!" {
		t.Errorf("output: got %v", result.Outputs["output"])
	}
	if result.Outputs["stop_reason"] != "complete" {
		t.Errorf("stop_reason: got %v", result.Outputs["stop_reason"])
	}
}

func TestRouter_WithAgentWorker(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "Done!", StopReason: model.StopReasonEnd},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	wf := &model.Workflow{
		Agents: map[string]model.AgentSpec{
			"helper": {Model: "mock/test-model", SystemPrompt: "Help"},
		},
		Skills: map[string]model.SkillSpec{},
	}
	router := NewRouter()
	router.Register("agent", &AgentWorker{workflow: wf, runner: eng})
	result := router.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "helper", "prompt": "Hi"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("agent: status %v, error: %s", result.Status, result.ErrorMessage)
	}
	result = router.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_2", JobID: "j1", StepID: "s2",
		Uses: "shell",
		With: map[string]any{"run": "echo hello"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("shell: status %v, error: %s", result.Status, result.ErrorMessage)
	}
	result = router.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_3", JobID: "j1", StepID: "s3",
		Uses: "unknown",
	})
	if result.Status != model.StepFailed {
		t.Errorf("unknown: status %v, want %v", result.Status, model.StepFailed)
	}
}

func TestFactory_NewRouter(t *testing.T) {
	wf := &model.Workflow{
		Agents: map[string]model.AgentSpec{"test": {Model: "mock/test"}},
		Skills: map[string]model.SkillSpec{},
	}
	aiSvc := newMockAIService(&mockHandler{})
	eng := newTestRunner(t, aiSvc)
	factory := NewFactory(wf, eng, nil, nil)
	router := factory.NewRouter()
	result := router.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "test", "prompt": "hi"},
	})
	if result.ErrorMessage == `unknown uses: "agent"` {
		t.Error("agent worker should be registered")
	}
}

func TestAgentWorker_NamespaceFromWorkflowSource(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Source: "my-workflow.yaml",
			Name:   "my-workflow",
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	// No with.namespace — should derive from workflow.Source
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "test"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}
}

func TestAgentWorker_NamespaceOverride(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Source: "my-workflow.yaml",
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	// Explicit with.namespace should override workflow.Source
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "test", "namespace": "custom-ns"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}
}

func TestAgentWorker_Execute_WithSkills(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "reviewed!", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)

	// Create engine with nil skill provider
	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"reviewer": {Model: "mock/test-model", Skills: []string{"review"}},
			},
			Skills: map[string]model.SkillSpec{
				"review": {Uses: "./review"},
			},
		},
		runner: eng,
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "reviewer", "prompt": "review this"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}
}

// --- Tool resolution tests ---

// simpleTool is a minimal tool for worker tests.
type simpleTool struct {
	toolName string
}

func (s *simpleTool) Def() model.ToolDef {
	return model.ToolDef{Name: s.toolName, Description: "test", InputSchema: map[string]any{"type": "object"}}
}
func (s *simpleTool) Prepare(_ context.Context, req tool.CallRequest) (*tool.PreparedCall, error) {
	return &tool.PreparedCall{
		ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs,
		Plan: tool.ExecutionPlan{Mode: tool.ExecModeSync, Policy: tool.ParallelSafe},
	}, nil
}
func (s *simpleTool) Execute(_ context.Context, _ *tool.PreparedCall) (*tool.Result, error) {
	return tool.TextResult("ok"), nil
}

func TestAgentWorker_SkillToolsAdditiveToCore(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)

	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&simpleTool{toolName: "core_read"}, tool.GroupCore)
	_ = reg.Register(&simpleTool{toolName: "skill_search"})
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model", Skills: []string{"web"}},
			},
			Skills: map[string]model.SkillSpec{
				"web": {Uses: "./web"},
			},
		},
		runner:      eng,
		coreToolIDs: reg.CoreToolIDs(),
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "search"},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

func TestAgentWorker_NoSkillToolsFallsBackToNil(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)

	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&simpleTool{toolName: "core_read"}, tool.GroupCore)
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
			Skills: map[string]model.SkillSpec{},
		},
		runner:      eng,
		coreToolIDs: reg.CoreToolIDs(),
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "hello"},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

func TestAgentWorker_WithToolsOverridesAll(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)

	// Create engine with the tool registered so ListTools can resolve it
	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&simpleTool{toolName: "core_read"}, tool.GroupCore)
	_ = reg.Register(&simpleTool{toolName: "only_this"})
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model", Skills: []string{"web"}},
			},
			Skills: map[string]model.SkillSpec{
				"web": {Uses: "./web"},
			},
		},
		runner:      eng,
		coreToolIDs: []string{"core_read"},
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{
			"agent":  "coder",
			"prompt": "hello",
			"tools":  []any{"only_this"},
		},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

func TestAgentWorker_MalformedWithToolsFails(t *testing.T) {
	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{
			"agent":  "coder",
			"prompt": "hello",
			"tools":  123,
		},
	})
	if result2.Status != model.StepFailed {
		t.Errorf("expected StepFailed for malformed tools, got %v", result2.Status)
	}
	if result2.ErrorMessage != "agent worker: 'tools' must be a list of strings" {
		t.Errorf("unexpected error: %s", result2.ErrorMessage)
	}
}

func TestAgentWorker_NamespaceAppPlusWorkDir(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			App:    "myapp",
			Source: "workflow.yaml",
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses:    "agent",
		WorkDir: "/tmp/work",
		With:    map[string]any{"agent": "coder", "prompt": "test"},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

func TestAgentWorker_NamespaceWorkDirOnly(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses:    "agent",
		WorkDir: "/tmp/work",
		With:    map[string]any{"agent": "coder", "prompt": "test"},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

func TestAgentWorker_NamespaceWithOverride(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			App:    "myapp",
			Source: "workflow.yaml",
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	result2 := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses:    "agent",
		WorkDir: "/tmp/work",
		With:    map[string]any{"agent": "coder", "prompt": "test", "namespace": "custom-ns"},
	})
	if result2.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result2.Status, result2.ErrorMessage)
	}
}

// --- Deterministic tool ordering tests ---

// capturingHandler records the model.Request for inspection.
type capturingHandler struct {
	responses []*model.Response
	calls     int
	lastReq   *model.Request
}

func (m *capturingHandler) Responses(_ context.Context, req *model.Request) (model.ResponseStream, error) {
	m.lastReq = req
	idx := m.calls
	m.calls++
	var resp *model.Response
	if idx < len(m.responses) {
		resp = m.responses[idx]
	} else {
		resp = &model.Response{Content: "done", StopReason: model.StopReasonEnd}
	}
	return &mockWorkerStream{resp: resp}, nil
}

// mockWorkerSkillSvc implements workerSkillProvider for tests.
type mockWorkerSkillSvc struct {
	skills map[string]*skill.ResolvedSkill
}

func (m *mockWorkerSkillSvc) Get(id string) (*skill.ResolvedSkill, bool) {
	if m.skills == nil {
		return nil, false
	}
	s, ok := m.skills[id]
	return s, ok
}

func TestAgentWorker_MergedToolsSorted(t *testing.T) {
	handler := &capturingHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(handler)

	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	_ = reg.RegisterWithGroup(&simpleTool{toolName: "write"}, tool.GroupCore)
	_ = reg.RegisterWithGroup(&simpleTool{toolName: "read"}, tool.GroupCore)
	_ = reg.Register(&simpleTool{toolName: "search"})
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model", Skills: []string{"web"}},
			},
			Skills: map[string]model.SkillSpec{
				"web": {Uses: "./web"},
			},
		},
		runner:      eng,
		coreToolIDs: reg.CoreToolIDs(),
		skillSvc: &mockWorkerSkillSvc{skills: map[string]*skill.ResolvedSkill{
			"web": {ID: "web", Meta: skill.SkillMeta{AllowedTools: []string{"search"}}},
		}},
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "test"},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}

	// Verify the tools sent to engine were sorted
	if handler.lastReq == nil {
		t.Fatal("no request captured")
	}
	toolNames := make([]string, len(handler.lastReq.Tools))
	for i, td := range handler.lastReq.Tools {
		toolNames[i] = td.Name
	}
	expected := []string{"read", "search", "write"}
	if len(toolNames) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(toolNames), toolNames)
	}
	for i, want := range expected {
		if toolNames[i] != want {
			t.Errorf("tool[%d]: got %q, want %q", i, toolNames[i], want)
		}
	}
}

func TestAgentWorker_EnvDeepCopied(t *testing.T) {
	mockClient := &mockHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(mockClient)
	eng := newTestRunner(t, aiSvc)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
		},
		runner: eng,
	}
	env := map[string]string{
		"REED_RUN_TEMP_DIR":  "/tmp/run123",
		"REED_RUN_SKILL_DIR": "/tmp/run123/skills",
	}
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{"agent": "coder", "prompt": "test"},
		Env:  env,
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}
	// Verify original env was not mutated (deep copy check)
	if env["REED_RUN_SKILL_DIR"] != "/tmp/run123/skills" {
		t.Error("original env was mutated")
	}
}

func TestAgentWorker_ExplicitToolsPreserveOrder(t *testing.T) {
	handler := &capturingHandler{
		responses: []*model.Response{
			{Content: "done", StopReason: model.StopReasonEnd, Usage: model.Usage{Total: 10}},
		},
	}
	aiSvc := newMockAIService(handler)

	sessSvc := session.New(t.TempDir(), nil, nil, nil)
	reg := tool.NewRegistry()
	_ = reg.Register(&simpleTool{toolName: "write"})
	_ = reg.Register(&simpleTool{toolName: "read"})
	eng := agent.New(aiSvc, sessSvc, agent.WrapToolService(tool.NewService(reg)), nil, nil)

	w := &AgentWorker{
		workflow: &model.Workflow{
			Agents: map[string]model.AgentSpec{
				"coder": {Model: "mock/test-model"},
			},
			Skills: map[string]model.SkillSpec{},
		},
		runner: eng,
	}
	// Explicit tools: write, read (not alphabetical)
	result := w.Execute(context.Background(), engine.StepPayload{
		StepRunID: "sr_1", JobID: "j1", StepID: "s1",
		Uses: "agent",
		With: map[string]any{
			"agent":  "coder",
			"prompt": "test",
			"tools":  []any{"write", "read"},
		},
	})
	if result.Status != model.StepSucceeded {
		t.Errorf("status: got %v, error: %s", result.Status, result.ErrorMessage)
	}

	// Verify order is preserved (write before read)
	if handler.lastReq == nil {
		t.Fatal("no request captured")
	}
	toolNames := make([]string, len(handler.lastReq.Tools))
	for i, td := range handler.lastReq.Tools {
		toolNames[i] = td.Name
	}
	expected := []string{"write", "read"}
	if len(toolNames) != len(expected) {
		t.Fatalf("expected %d tools, got %d: %v", len(expected), len(toolNames), toolNames)
	}
	for i, want := range expected {
		if toolNames[i] != want {
			t.Errorf("tool[%d]: got %q, want %q (explicit order should be preserved)", i, toolNames[i], want)
		}
	}
}
