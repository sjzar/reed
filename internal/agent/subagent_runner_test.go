package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/tool"
)

// fakeAgentRunner captures the request and returns a canned response.
type fakeAgentRunner struct {
	resp    *model.AgentRunResponse
	err     error
	lastReq *model.AgentRunRequest
}

func (f *fakeAgentRunner) Run(_ context.Context, req *model.AgentRunRequest) (*model.AgentRunResponse, error) {
	f.lastReq = req
	return f.resp, f.err
}

func TestSubAgentRunner_BasicMapping(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{Output: "ok", StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{
		Namespace: "ns", Model: "m1",
	})
	resp, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{
		AgentID: "agent-1", Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("RunSubAgent: %v", err)
	}
	if resp.Output != "ok" {
		t.Errorf("output: got %q, want %q", resp.Output, "ok")
	}
	if fake.lastReq.AgentID != "agent-1" {
		t.Errorf("AgentID: got %q", fake.lastReq.AgentID)
	}
	if fake.lastReq.Prompt != "hello" {
		t.Errorf("Prompt: got %q", fake.lastReq.Prompt)
	}
	if fake.lastReq.Namespace != "ns" {
		t.Errorf("Namespace: got %q", fake.lastReq.Namespace)
	}
	if !fake.lastReq.WaitForAsyncTasks {
		t.Error("WaitForAsyncTasks must be true")
	}
	if fake.lastReq.SessionID != "" {
		t.Errorf("SessionID should be empty, got %q", fake.lastReq.SessionID)
	}
	if fake.lastReq.SessionKey != "" {
		t.Errorf("SessionKey should be empty, got %q", fake.lastReq.SessionKey)
	}
	if fake.lastReq.PromptMode != PromptModeMinimal.String() {
		t.Errorf("PromptMode: got %q, want %q", fake.lastReq.PromptMode, PromptModeMinimal.String())
	}
}

func TestSubAgentRunner_ToolsNilPreserved(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{Tools: nil})
	_, _ = runner.RunSubAgent(context.Background(), tool.SubAgentRequest{AgentID: "a", Prompt: "p"})
	if fake.lastReq.Tools != nil {
		t.Error("nil Tools should remain nil")
	}
}

func TestSubAgentRunner_ToolsEmptyPreserved(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{Tools: []string{}})
	_, _ = runner.RunSubAgent(context.Background(), tool.SubAgentRequest{AgentID: "a", Prompt: "p"})
	if fake.lastReq.Tools == nil {
		t.Error("empty Tools should remain non-nil empty slice")
	}
	if len(fake.lastReq.Tools) != 0 {
		t.Errorf("expected empty slice, got %v", fake.lastReq.Tools)
	}
}

func TestSubAgentRunner_StopAsyncError(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{StopReason: model.AgentStopAsync}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{})
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{AgentID: "a", Prompt: "p"})
	if err == nil {
		t.Fatal("expected error for AgentStopAsync")
	}
}

func TestSubAgentRunner_NilRunner(t *testing.T) {
	runner := &engineSubAgentRunner{runner: nil, cfg: SubAgentRunConfig{}}
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{AgentID: "a", Prompt: "p"})
	if err == nil {
		t.Fatal("expected error for nil runner")
	}
}

func TestSubAgentRunner_RunnerError(t *testing.T) {
	fake := &fakeAgentRunner{err: fmt.Errorf("boom")}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{})
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{AgentID: "a", Prompt: "p"})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected 'boom' error, got %v", err)
	}
}

func TestSubAgentRunner_EnvPropagation(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{Output: "ok", StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{Namespace: "ns"})
	env := map[string]string{
		"REED_RUN_TEMP_DIR":  "/tmp/run123",
		"REED_RUN_SKILL_DIR": "/tmp/run123/skills",
	}
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{
		AgentID: "agent-1", Prompt: "hello",
		Env:     env,
		RunRoot: "/tmp/run123",
	})
	if err != nil {
		t.Fatalf("RunSubAgent: %v", err)
	}
	if fake.lastReq.RunRoot != "/tmp/run123" {
		t.Errorf("RunRoot: got %q, want %q", fake.lastReq.RunRoot, "/tmp/run123")
	}
	if fake.lastReq.Env == nil {
		t.Fatal("Env should not be nil")
	}
	if fake.lastReq.Env["REED_RUN_SKILL_DIR"] != "/tmp/run123/skills" {
		t.Errorf("Env[REED_RUN_SKILL_DIR]: got %q", fake.lastReq.Env["REED_RUN_SKILL_DIR"])
	}
	// Verify deep copy — mutating original should not affect request
	env["REED_RUN_SKILL_DIR"] = "mutated"
	if fake.lastReq.Env["REED_RUN_SKILL_DIR"] != "/tmp/run123/skills" {
		t.Error("Env was not deep-copied")
	}
}

func TestSubAgentRunner_DepthLimitExceeded(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{Output: "ok", StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{})
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{
		AgentID: "a", Prompt: "p", Depth: MaxSubAgentDepth,
	})
	if err == nil {
		t.Fatal("expected error when depth >= MaxSubAgentDepth")
	}
	if fake.lastReq != nil {
		t.Error("runner.Run should not have been called")
	}
}

func TestSubAgentRunner_DepthPropagation(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{Output: "ok", StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{})
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{
		AgentID: "a", Prompt: "p", Depth: 1,
	})
	if err != nil {
		t.Fatalf("RunSubAgent: %v", err)
	}
	if fake.lastReq.Depth != 2 {
		t.Errorf("Depth: got %d, want 2", fake.lastReq.Depth)
	}
}

func TestSubAgentRunner_SpawnSubagentFiltered(t *testing.T) {
	fake := &fakeAgentRunner{resp: &model.AgentRunResponse{Output: "ok", StopReason: model.AgentStopComplete}}
	runner := NewSubAgentRunner(fake, SubAgentRunConfig{
		Tools: []string{"bash", "read", "spawn_subagent", "edit"},
	})
	_, err := runner.RunSubAgent(context.Background(), tool.SubAgentRequest{
		AgentID: "a", Prompt: "p",
	})
	if err != nil {
		t.Fatalf("RunSubAgent: %v", err)
	}
	for _, toolName := range fake.lastReq.Tools {
		if toolName == "spawn_subagent" {
			t.Error("spawn_subagent should be filtered from child tool list")
		}
	}
	if len(fake.lastReq.Tools) != 3 {
		t.Errorf("expected 3 tools after filtering, got %d: %v", len(fake.lastReq.Tools), fake.lastReq.Tools)
	}
}
