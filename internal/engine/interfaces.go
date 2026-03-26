package engine

import (
	"context"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
)

// Worker is the blackbox executor interface.
// It receives a fully-rendered StepPayload and returns a Result.
// Worker must never reverse-depend on engine; it has zero knowledge
// of DAG, upstream/downstream, or workflow context.
type Worker interface {
	Execute(ctx context.Context, payload StepPayload) StepRunResult
}

// StepPayload is the fully-rendered step execution context sent to a Worker.
// All expressions have been evaluated by the owner loop before dispatch.
type StepPayload struct {
	StepRunID  string
	JobID      string
	StepID     string
	Uses       string
	With       map[string]any
	Env        map[string]string
	WorkDir    string
	Shell      string
	Background bool
	Timeout    int                  // seconds; 0 = use default
	ToolAccess model.ToolAccessMode // from workflow.ToolAccess

	// RenderedAgentModel and RenderedAgentSystemPrompt hold expression-evaluated
	// agent spec fields. Set by dispatch when the step uses an agent; empty otherwise.
	RenderedAgentModel        string
	RenderedAgentSystemPrompt string

	// Bus is the event bus for publishing step output (text chunks, status).
	// Workers publish to bus.StepOutputTopic(StepRunID). nil = discard events.
	Bus *bus.Bus
}

// StepRunResult is the outcome returned by a Worker after execution.
type StepRunResult struct {
	StepRunID    string
	JobID        string
	StepID       string
	Status       model.StepStatus
	Outputs      map[string]any
	ErrorCode    string
	ErrorMessage string
}
