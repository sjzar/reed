package model

import "time"

// EventType identifies the kind of agent lifecycle event.
type EventType string

const (
	EventLLMStart         EventType = "llm_start"
	EventLLMEnd           EventType = "llm_end"
	EventTextChunk        EventType = "text_chunk"
	EventToolStart        EventType = "tool_start"
	EventToolLog          EventType = "tool_log"
	EventToolEnd          EventType = "tool_end"
	EventToolDispatch     EventType = "tool_dispatch"
	EventToolComplete     EventType = "tool_complete"
	EventToolCancel       EventType = "tool_cancel"
	EventStatus           EventType = "status"
	EventSubAgentSpawn    EventType = "subagent_spawn"
	EventSubAgentComplete EventType = "subagent_complete"
	EventError            EventType = "error"
)

// Event is a structured lifecycle event emitted during agent execution.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data,omitempty"`
}

// LLMStartData is emitted when an LLM call begins.
type LLMStartData struct {
	Iteration int    `json:"iteration"`
	Model     string `json:"model"`
	Messages  int    `json:"messages"`
	Tools     int    `json:"tools"`
}

// LLMEndData is emitted when an LLM call completes.
type LLMEndData struct {
	StopReason StopReason `json:"stopReason"`
	ToolCalls  int        `json:"toolCalls"`
	Usage      Usage      `json:"usage"`
}

// ToolStartData is emitted when a tool execution begins.
type ToolStartData struct {
	ToolCallID string         `json:"toolCallID"`
	ToolName   string         `json:"toolName"`
	Arguments  map[string]any `json:"arguments,omitempty"`
}

// ToolEndData is emitted when a tool execution completes.
type ToolEndData struct {
	ToolCallID string        `json:"toolCallID"`
	ToolName   string        `json:"toolName"`
	Result     string        `json:"result,omitempty"`
	IsError    bool          `json:"isError,omitempty"`
	Duration   time.Duration `json:"duration"`
}

// SubAgentSpawnData is emitted when a subagent is spawned.
type SubAgentSpawnData struct {
	ID      string `json:"id"`
	AgentID string `json:"agentID"`
	Prompt  string `json:"prompt"`
}

// SubAgentCompleteData is emitted when a subagent completes.
type SubAgentCompleteData struct {
	ID     string `json:"id"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ToolDispatchData is emitted when an async tool job is dispatched.
type ToolDispatchData struct {
	ToolCallID string `json:"toolCallID"`
	ToolName   string `json:"toolName"`
	JobID      string `json:"jobID"`
}

// ToolCompleteData is emitted when an async tool job completes.
type ToolCompleteData struct {
	JobID      string        `json:"jobID"`
	ToolCallID string        `json:"toolCallID"`
	ToolName   string        `json:"toolName"`
	IsError    bool          `json:"isError,omitempty"`
	Duration   time.Duration `json:"duration"`
}

// ToolCancelData is emitted when a tool cancel request is accepted.
type ToolCancelData struct {
	JobID    string `json:"jobID"`
	ToolName string `json:"toolName"`
}

// TextChunkData is emitted for streaming text deltas.
type TextChunkData struct {
	Delta string `json:"delta"`
}

// StatusData is emitted for status changes.
type StatusData struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// ErrorData is emitted when an error occurs.
type ErrorData struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}
