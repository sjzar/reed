// Package tool provides the unified tool registry, execution service, and scheduling for agent tool calls.
package tool

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// RuntimeContext carries path and environment info for tool execution.
type RuntimeContext struct {
	// Set marks whether this context was explicitly configured.
	Set        bool
	Cwd        string
	RunRoot    string
	OS         string
	AgentDepth int // nesting depth for subagent spawning (0 = top-level)
}

// CallRequest is the input for a single tool call.
type CallRequest struct {
	ToolCallID string
	Name       string
	RawArgs    []byte
	Env        map[string]string
	Context    RuntimeContext
}

// ToolGroup classifies a tool's exposure policy.
type ToolGroup string

const (
	// GroupCore tools are included by default when no tools are explicitly requested.
	GroupCore ToolGroup = "core"
	// GroupOptional tools must be explicitly requested. This is the default for ungrouped tools.
	GroupOptional ToolGroup = "optional"
	// GroupMCP tools come from MCP servers and must be explicitly requested.
	GroupMCP ToolGroup = "mcp"
)

// GroupedTool is an optional interface. Tools that don't implement it
// are treated as GroupOptional (NOT core — core must be explicit).
type GroupedTool interface {
	Group() ToolGroup
}

// CallResult is the output of a single tool call, ready for the LLM.
type CallResult struct {
	ToolCallID string
	ToolName   string
	Content    []model.Content
	IsError    bool
	DurationMs int64 // execution wall-clock time in milliseconds
}

// Result is the tool-internal execution result before wrapping into CallResult.
type Result struct {
	Content []model.Content
	IsError bool
}

// PreparedCall is the output of Tool.Prepare — parsed args + execution plan.
type PreparedCall struct {
	ToolCallID string
	Name       string
	RawArgs    []byte
	Parsed     any
	Plan       ExecutionPlan
	Env        map[string]string
	Context    RuntimeContext
}

// ExecutionPlan describes how the service should schedule a tool call.
type ExecutionPlan struct {
	Mode    ExecMode
	Timeout time.Duration
	Policy  ConcurrencyPolicy
	Locks   []ResourceLock
}

// ExecMode determines whether a tool call runs synchronously or asynchronously.
type ExecMode int

const (
	ExecModeSync  ExecMode = iota // blocks the request
	ExecModeAsync                 // detaches from the request lifecycle
)

// ConcurrencyPolicy determines how the service schedules concurrent tool calls.
type ConcurrencyPolicy int

const (
	GlobalSerial ConcurrencyPolicy = iota // one at a time, globally
	ParallelSafe                          // no locking needed
	Scoped                                // uses ResourceLock keys
)

// ResourceLock describes a lock requirement for scoped concurrency.
type ResourceLock struct {
	Key  string
	Mode LockMode
}

// LockMode is the type of lock (read or write).
type LockMode int

const (
	LockRead LockMode = iota
	LockWrite
)

// Tool is the unified interface for all executable tools.
type Tool interface {
	Def() model.ToolDef
	Prepare(ctx context.Context, req CallRequest) (*PreparedCall, error)
	Execute(ctx context.Context, call *PreparedCall) (*Result, error)
}

// JobStatus tracks the lifecycle of an async job.
type JobStatus string

const (
	JobQueued     JobStatus = "queued"
	JobRunning    JobStatus = "running"
	JobCancelling JobStatus = "cancelling"
	JobCompleted  JobStatus = "completed"
	JobFailed     JobStatus = "failed"
)

// TextResult creates a successful Result with a single text content block.
func TextResult(text string) *Result {
	return &Result{
		Content: []model.Content{{Type: model.ContentTypeText, Text: text}},
	}
}

// ErrorResult creates an error Result with a single text content block.
func ErrorResult(msg string) *Result {
	return &Result{
		Content: []model.Content{{Type: model.ContentTypeText, Text: msg}},
		IsError: true,
	}
}

// MarshalArgs converts a model.ToolCall into raw JSON bytes.
// Priority: RawJSON > Arguments > empty object.
func MarshalArgs(tc model.ToolCall) ([]byte, error) {
	if tc.RawJSON != "" {
		return []byte(tc.RawJSON), nil
	}
	if tc.Arguments != nil {
		return json.Marshal(tc.Arguments)
	}
	return []byte("{}"), nil
}

// DefaultTimeout is the default per-tool execution timeout.
const DefaultTimeout = 30 * time.Second
