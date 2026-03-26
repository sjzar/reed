package agent

import (
	"context"
	"fmt"
	"slices"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/tool"
)

// MaxSubAgentDepth is the maximum nesting depth for subagent spawning.
// Depth 0 = top-level agent, depth 1 = first subagent, etc.
const MaxSubAgentDepth = 3

// AgentRunner abstracts the agent engine's Run method.
type AgentRunner interface {
	Run(ctx context.Context, req *model.AgentRunRequest) (*model.AgentRunResponse, error)
}

// SubAgentRunConfig holds default values for subagent runs.
type SubAgentRunConfig struct {
	Namespace     string
	Model         string
	Profile       string
	Tools         []string
	Skills        []string
	SystemPrompt  string
	MaxIterations int
	TimeoutMs     int
}

// engineSubAgentRunner adapts AgentRunner to tool.SubAgentRunner.
type engineSubAgentRunner struct {
	runner AgentRunner
	cfg    SubAgentRunConfig
}

// NewSubAgentRunner creates a tool.SubAgentRunner backed by an AgentRunner.
func NewSubAgentRunner(r AgentRunner, cfg SubAgentRunConfig) tool.SubAgentRunner {
	return &engineSubAgentRunner{runner: r, cfg: cfg}
}

func (e *engineSubAgentRunner) RunSubAgent(ctx context.Context, req tool.SubAgentRequest) (*model.AgentRunResponse, error) {
	if e.runner == nil {
		return nil, fmt.Errorf("agent runner not configured")
	}
	if req.Depth >= MaxSubAgentDepth {
		return nil, fmt.Errorf("subagent depth limit exceeded (max %d)", MaxSubAgentDepth)
	}
	var envCopy map[string]string
	if len(req.Env) > 0 {
		envCopy = make(map[string]string, len(req.Env))
		for k, v := range req.Env {
			envCopy[k] = v
		}
	}

	// Filter out spawn_subagent from child tool list to prevent recursive spawning.
	tools := slices.Clone(e.cfg.Tools)
	tools = slices.DeleteFunc(tools, func(t string) bool {
		return t == "spawn_subagent"
	})

	runReq := &model.AgentRunRequest{
		Namespace:         e.cfg.Namespace,
		AgentID:           req.AgentID,
		Model:             e.cfg.Model,
		Profile:           e.cfg.Profile,
		Tools:             tools,
		Skills:            slices.Clone(e.cfg.Skills),
		SystemPrompt:      e.cfg.SystemPrompt,
		Prompt:            req.Prompt,
		MaxIterations:     e.cfg.MaxIterations,
		TimeoutMs:         e.cfg.TimeoutMs,
		WaitForAsyncTasks: true,
		RunRoot:           req.RunRoot,
		Env:               envCopy,
		PromptMode:        PromptModeMinimal.String(),
		Depth:             req.Depth + 1,
	}
	resp, err := e.runner.Run(ctx, runReq)
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.StopReason == model.AgentStopAsync {
		return nil, fmt.Errorf("subagent returned async_pending: nested async is not allowed")
	}
	return resp, nil
}
