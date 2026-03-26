package mcp

import (
	"context"
	"fmt"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

// RunResolver resolves trigger params into a RunRequest.
type RunResolver interface {
	ResolveRunRequest(ctx context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error)
}

// RunSubmitter submits runs and waits for their completion.
type RunSubmitter interface {
	Submit(req *model.RunRequest) (*engine.RunHandle, error)
	WaitRun(ctx context.Context, runID string) (*engine.RunView, error)
}

// TriggerHandler handles MCP tool call triggers.
type TriggerHandler struct {
	resolver  RunResolver
	submitter RunSubmitter
	wf        *model.Workflow
	wfSource  string
	workDir   string
}

// NewTriggerHandler creates a handler for MCP-triggered runs.
func NewTriggerHandler(resolver RunResolver, submitter RunSubmitter, wf *model.Workflow, wfSource string, workDir string) *TriggerHandler {
	return &TriggerHandler{
		resolver:  resolver,
		submitter: submitter,
		wf:        wf,
		wfSource:  wfSource,
		workDir:   workDir,
	}
}

// HandleToolCall processes an MCP tool call, triggering a Run and returning outputs.
func (h *TriggerHandler) HandleToolCall(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	// 1. Find matching MCPTool definition
	var tool *model.MCPTool
	if h.wf.On.Service != nil {
		for i := range h.wf.On.Service.MCP {
			if h.wf.On.Service.MCP[i].Name == toolName {
				tool = &h.wf.On.Service.MCP[i]
				break
			}
		}
	}
	if tool == nil {
		return nil, fmt.Errorf("unknown MCP tool %q", toolName)
	}

	// 2. Build TriggerParams
	params := model.TriggerParams{
		TriggerType: model.TriggerMCP,
		TriggerMeta: map[string]any{"tool_name": toolName},
		InputValues: arguments,
	}
	if len(tool.RunJobs) > 0 {
		params.RunJobs = tool.RunJobs
	}
	if tool.Inputs != nil {
		params.Inputs = tool.Inputs
	}
	if tool.Outputs != nil {
		params.Outputs = tool.Outputs
	}

	// 3. Resolve RunRequest
	req, err := h.resolver.ResolveRunRequest(ctx, h.wf, h.wfSource, params)
	if err != nil {
		return nil, fmt.Errorf("resolve run request: %w", err)
	}
	req.WorkDir = h.workDir

	// 4. Submit Run and wait
	result, err := h.submitter.Submit(req)
	if err != nil {
		return nil, fmt.Errorf("submit run: %w", err)
	}

	view, err := h.submitter.WaitRun(ctx, result.ID())
	if err != nil {
		return nil, fmt.Errorf("run execution: %w", err)
	}

	if view.Status != model.RunSucceeded {
		return nil, fmt.Errorf("run finished with status %s", view.Status)
	}

	return view.Outputs, nil
}
