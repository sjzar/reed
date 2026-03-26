package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

// mockSubAgentRunner is a test double for SubAgentRunner.
type mockSubAgentRunner struct {
	resp    *model.AgentRunResponse
	err     error
	lastReq SubAgentRequest
}

func (m *mockSubAgentRunner) RunSubAgent(_ context.Context, req SubAgentRequest) (*model.AgentRunResponse, error) {
	m.lastReq = req
	return m.resp, m.err
}

func TestSubAgentTool_Prepare(t *testing.T) {
	st := NewSubAgentTool(nil)
	raw, _ := json.Marshal(map[string]string{"agent_id": "a1", "prompt": "do stuff"})
	pc, err := st.Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "spawn_subagent", RawArgs: raw,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if pc.Plan.Mode != ExecModeAsync {
		t.Errorf("mode: got %v, want Async", pc.Plan.Mode)
	}
	if pc.Plan.Policy != ParallelSafe {
		t.Errorf("policy: got %v, want ParallelSafe", pc.Plan.Policy)
	}
}

func TestSubAgentTool_Execute_NilRunner(t *testing.T) {
	st := NewSubAgentTool(nil)
	raw, _ := json.Marshal(map[string]string{"agent_id": "a1", "prompt": "hi"})
	pc, _ := st.Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "spawn_subagent", RawArgs: raw,
	})
	res, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for nil runner")
	}
}

func TestSubAgentTool_Execute_Success(t *testing.T) {
	runner := &mockSubAgentRunner{
		resp: &model.AgentRunResponse{Output: "done"},
	}
	st := NewSubAgentTool(runner)
	raw, _ := json.Marshal(map[string]string{"agent_id": "agent-x", "prompt": "task"})
	pc, _ := st.Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "spawn_subagent", RawArgs: raw,
	})
	res, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Error("unexpected error result")
	}
	if runner.lastReq.AgentID != "agent-x" {
		t.Errorf("AgentID: got %q, want %q", runner.lastReq.AgentID, "agent-x")
	}
	if runner.lastReq.Prompt != "task" {
		t.Errorf("Prompt: got %q, want %q", runner.lastReq.Prompt, "task")
	}
	if len(res.Content) == 0 {
		t.Fatal("expected content in result")
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &result); err != nil {
		t.Fatalf("expected JSON output, got parse error: %v", err)
	}
	if result["output"] != "done" {
		t.Errorf("output: got %v, want 'done'", result["output"])
	}
	if _, ok := result["iterations"]; !ok {
		t.Error("expected iterations field in JSON output")
	}
	if _, ok := result["stopReason"]; !ok {
		t.Error("expected stopReason field in JSON output")
	}
	if _, ok := result["totalTokens"]; !ok {
		t.Error("expected totalTokens field in JSON output")
	}
}

func TestSubAgentTool_Execute_NilResponse(t *testing.T) {
	runner := &mockSubAgentRunner{resp: nil}
	st := NewSubAgentTool(runner)
	raw, _ := json.Marshal(map[string]string{"agent_id": "a1", "prompt": "hi"})
	pc, _ := st.Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "spawn_subagent", RawArgs: raw,
	})
	res, err := st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("expected error result for nil response")
	}
}

func TestSubAgentTool_RunRoot_FromContext(t *testing.T) {
	runner := &mockSubAgentRunner{
		resp: &model.AgentRunResponse{Output: "ok"},
	}
	st := NewSubAgentTool(runner)
	raw, _ := json.Marshal(map[string]string{"agent_id": "a1", "prompt": "hi"})
	pc, err := st.Prepare(context.Background(), CallRequest{
		ToolCallID: "tc1", Name: "spawn_subagent", RawArgs: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	pc.Context = RuntimeContext{Set: true, RunRoot: "/tmp/test"}
	_, err = st.Execute(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	if runner.lastReq.RunRoot != "/tmp/test" {
		t.Errorf("RunRoot: got %q, want %q", runner.lastReq.RunRoot, "/tmp/test")
	}
}
