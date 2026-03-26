package model

import "time"

// TriggerType identifies which trigger initiated a Run.
type TriggerType string

const (
	TriggerCLI      TriggerType = "cli"
	TriggerHTTP     TriggerType = "http"
	TriggerMCP      TriggerType = "mcp"
	TriggerSchedule TriggerType = "schedule"
)

// TriggerParams holds the raw parameters from a trigger source.
// Each trigger type (CLI/HTTP/MCP/Schedule) populates the relevant fields.
type TriggerParams struct {
	TriggerType TriggerType
	TriggerMeta map[string]any
	RunJobs     []string             // trigger-level run_jobs override
	Inputs      map[string]InputSpec // trigger-level inputs spec (nil = use workflow-level)
	InputValues map[string]any       // actual input values from the trigger
	Outputs     map[string]string    // trigger-level outputs (nil = use workflow-level)
	Env         map[string]string    // trigger-level additional env
}

// RunRequest is the unified Run descriptor produced by all trigger types
// (CLI/HTTP/MCP/Schedule) and consumed by Engine.StartRun.
type RunRequest struct {
	Workflow       *Workflow
	WorkflowSource string

	TriggerType TriggerType
	TriggerMeta map[string]any // trace-only metadata (e.g. path, method, tool_name, cron)

	Inputs  map[string]any    // resolved input values (after defaults + validation)
	Outputs map[string]string // effective outputs mapping
	RunJobs []string          // DAG subgraph entry points; empty = all jobs
	Env     map[string]string // merged env (workflow base + trigger overlay)
	Secrets map[string]string // resolved secret values

	WorkDir string        // project working directory; fallback for steps without explicit workdir
	Timeout time.Duration // run-level timeout; 0 = no timeout
}
