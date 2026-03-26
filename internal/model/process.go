package model

import "time"

// ProcessMode defines how a Process operates.
type ProcessMode string

const (
	ProcessModeCLI      ProcessMode = "cli"
	ProcessModeService  ProcessMode = "service"
	ProcessModeSchedule ProcessMode = "schedule"
)

// ProcessStatus defines the lifecycle state of a Process.
type ProcessStatus string

const (
	ProcessStarting ProcessStatus = "STARTING"
	ProcessRunning  ProcessStatus = "RUNNING"
	ProcessStopped  ProcessStatus = "STOPPED"
	ProcessFailed   ProcessStatus = "FAILED"
)

// Process represents an OS process host — the CLI default target.
type Process struct {
	ID        string
	PID       int
	Mode      ProcessMode
	Status    ProcessStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// RunStatus defines the lifecycle state of a Run.
type RunStatus string

const (
	RunCreated   RunStatus = "CREATED"
	RunStarting  RunStatus = "STARTING"
	RunRunning   RunStatus = "RUNNING"
	RunStopping  RunStatus = "STOPPING"
	RunSucceeded RunStatus = "SUCCEEDED"
	RunFailed    RunStatus = "FAILED"
	RunCanceled  RunStatus = "CANCELED"
)

// IsTerminal returns true if the run status represents a final state.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunCanceled:
		return true
	default:
		return false
	}
}

// Run represents a single workflow execution instance within a Process.
// All Runs are memory-only; history goes to Process-level JSONL.
type Run struct {
	ID             string
	ProcessID      string
	Workflow       *Workflow
	WorkflowSource string

	Status RunStatus

	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt *time.Time
}

// StepStatus defines the lifecycle state of a StepRun.
type StepStatus string

const (
	StepPending   StepStatus = "PENDING"
	StepRunning   StepStatus = "RUNNING"
	StepSucceeded StepStatus = "SUCCEEDED"
	StepFailed    StepStatus = "FAILED"
	StepCanceled  StepStatus = "CANCELED"
	StepSkipped   StepStatus = "SKIPPED"
)

// IsTerminal returns true if the step status represents a final state.
func (s StepStatus) IsTerminal() bool {
	switch s {
	case StepSucceeded, StepFailed, StepCanceled, StepSkipped:
		return true
	default:
		return false
	}
}

// StepRun represents a step-level execution instance within a Run.
type StepRun struct {
	ID         string
	RunID      string
	JobID      string
	StepID     string
	Background bool

	Status       StepStatus
	Outputs      map[string]any
	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
}

// ToolAccessMode controls file-system boundary for workers.
type ToolAccessMode string

const (
	ToolAccessWorkDir ToolAccessMode = "workdir"
	ToolAccessFull    ToolAccessMode = "full"
)
