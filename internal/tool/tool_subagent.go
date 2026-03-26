package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sjzar/reed/internal/model"
)

// subAgentTool spawns a subagent as an async job.
type subAgentTool struct {
	runner SubAgentRunner
}

// NewSubAgentTool creates a spawn_subagent tool.
// runner may be nil; Execute returns an error if called without a runner.
func NewSubAgentTool(runner SubAgentRunner) Tool {
	return &subAgentTool{runner: runner}
}

func (t *subAgentTool) Group() ToolGroup { return GroupOptional }

func (t *subAgentTool) Def() model.ToolDef {
	return model.ToolDef{
		Name: "spawn_subagent",
		Description: "Spawn a subagent to handle an independent subtask asynchronously. The subagent runs in its own context and returns a JSON result with output, iterations, stopReason, and totalTokens.\n" +
			"Use when: delegating independent subtasks that don't need your immediate attention (e.g. research, parallel investigation).\n" +
			"Don't use when: the subtask depends on your current context or needs your direct coordination.",
		Summary: "Spawn a subagent for an independent subtask.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent_id": map[string]any{"type": "string", "description": "The agent ID to spawn"},
				"prompt":   map[string]any{"type": "string", "description": "The prompt for the subagent"},
			},
			"required": []string{"agent_id", "prompt"},
		},
	}
}

type subAgentParams struct {
	AgentID string `json:"agent_id"`
	Prompt  string `json:"prompt"`
}

func (t *subAgentTool) Prepare(_ context.Context, req CallRequest) (*PreparedCall, error) {
	var p subAgentParams
	if err := json.Unmarshal(req.RawArgs, &p); err != nil {
		return nil, fmt.Errorf("parse subagent args: %w", err)
	}
	if p.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	if p.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	return &PreparedCall{
		ToolCallID: req.ToolCallID,
		Name:       req.Name,
		RawArgs:    req.RawArgs,
		Parsed:     p,
		Plan: ExecutionPlan{
			Mode:   ExecModeAsync,
			Policy: ParallelSafe,
		},
	}, nil
}

func (t *subAgentTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	if t.runner == nil {
		return ErrorResult("tool unavailable: subagent runner not configured"), nil
	}
	p, ok := call.Parsed.(subAgentParams)
	if !ok {
		return nil, fmt.Errorf("internal: unexpected parsed type %T", call.Parsed)
	}
	resp, err := t.runner.RunSubAgent(ctx, SubAgentRequest{
		AgentID: p.AgentID,
		Prompt:  p.Prompt,
		Env:     call.Env,
		RunRoot: call.Context.RunRoot,
		Depth:   call.Context.AgentDepth,
	})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return ErrorResult("subagent returned nil response"), nil
	}
	result := map[string]any{
		"output":      resp.Output,
		"iterations":  resp.Iterations,
		"stopReason":  string(resp.StopReason),
		"totalTokens": resp.TotalUsage.Total,
	}
	data, _ := json.Marshal(result)
	return TextResult(string(data)), nil
}
