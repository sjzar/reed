package model

import "time"

// PingResponse is the data payload for GET /v1/ping.
type PingResponse struct {
	ProcessID string `json:"processID"`
	PID       int    `json:"pid"`
	Mode      string `json:"mode"`
	Now       string `json:"now"`
}

// StatusView is the Process-centric status response for API/IPC.
type StatusView struct {
	ProcessID  string          `json:"processID"`
	PID        int             `json:"pid"`
	Mode       string          `json:"mode"`
	Status     string          `json:"status"`
	Uptime     string          `json:"uptime"`
	CreatedAt  time.Time       `json:"createdAt"`
	Listeners  []ListenerView  `json:"listeners,omitempty"`
	ActiveRuns []ActiveRunView `json:"activeRuns,omitempty"`
}

// ListenerView describes a network listener in the status response.
type ListenerView struct {
	Protocol   string `json:"protocol"`
	Address    string `json:"address"`
	RouteCount int    `json:"routeCount"`
}

// ActiveRunView describes an active Run within a Process status response.
type ActiveRunView struct {
	RunID          string                `json:"runID"`
	WorkflowSource string                `json:"workflowSource"`
	Status         string                `json:"status"`
	CreatedAt      time.Time             `json:"createdAt"`
	StartedAt      time.Time             `json:"startedAt"`
	FinishedAt     *time.Time            `json:"finishedAt,omitempty"`
	Jobs           map[string]APIJobView `json:"jobs,omitempty"`
}

// APIJobView describes a Job within an ActiveRunView.
type APIJobView struct {
	JobID   string                 `json:"jobID"`
	Status  string                 `json:"status"`
	Outputs map[string]any         `json:"outputs,omitempty"`
	Steps   map[string]APIStepView `json:"steps,omitempty"`
}

// APIStepView describes a Step within an APIJobView.
type APIStepView struct {
	StepID       string         `json:"stepID"`
	StepRunID    string         `json:"stepRunID,omitempty"`
	Status       string         `json:"status"`
	IsBackground bool           `json:"isBackground"`
	Outputs      map[string]any `json:"outputs,omitempty"`
	ErrorCode    string         `json:"errorCode,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
}
