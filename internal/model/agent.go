package model

// AgentRunRequest is the external request to the Agent Engine.
// It carries session routing, profile/tool/skill selection, model params, and input.
type AgentRunRequest struct {
	// Session routing
	Namespace  string `json:"namespace"`
	AgentID    string `json:"agentID"`
	SessionKey string `json:"sessionKey,omitempty"`
	SessionID  string `json:"sessionID,omitempty"` // mutually exclusive with SessionKey

	// Template and capabilities
	Profile string   `json:"profile,omitempty"`
	Tools   []string `json:"tools,omitempty"`
	Skills  []string `json:"skills,omitempty"`

	// Model parameters
	Model         string         `json:"model,omitempty"`
	MaxTokens     int            `json:"maxTokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	TopP          *float64       `json:"topP,omitempty"`
	ThinkingLevel string         `json:"thinkingLevel,omitempty"`
	ExtraParams   map[string]any `json:"extraParams,omitempty"`
	Schema        map[string]any `json:"schema,omitempty"`

	// Input
	SystemPrompt string   `json:"systemPrompt,omitempty"`
	Prompt       string   `json:"prompt"`
	Media        []string `json:"media,omitempty"`

	// Loop control
	MaxIterations int `json:"maxIterations,omitempty"`
	TimeoutMs     int `json:"timeoutMs,omitempty"`

	// Async wait strategy
	WaitForAsyncTasks bool `json:"waitForAsyncTasks,omitempty"`

	// Bus is the event bus for publishing step output (text chunks, status).
	// Type is *bus.Bus at runtime; declared as any because model is a leaf package.
	Bus any `json:"-"`

	// StepRunID identifies the step run for constructing bus output topics.
	StepRunID string `json:"-"`

	// RunRoot is the absolute path to the run temp dir; empty if not in workflow context.
	// Used by the agent engine for skill mounting.
	RunRoot string `json:"-"`

	// Env carries workflow-injected env vars. Passed through to tool execution. Not serialized.
	Env map[string]string `json:"-"`

	// ToolAccessProfile controls file-system boundary: "workdir" | "full". Not serialized.
	ToolAccessProfile string `json:"-"`

	// Cwd is the working directory for tool path resolution. Not serialized.
	Cwd string `json:"-"`

	// PromptMode controls system prompt verbosity: "full" (default), "minimal", "none". Not serialized.
	PromptMode string `json:"-"`

	// Depth tracks subagent nesting level (0 = top-level agent). Not serialized.
	Depth int `json:"-"`
}

// AgentStopReason identifies why the Agent Engine stopped.
type AgentStopReason string

const (
	AgentStopComplete       AgentStopReason = "complete"
	AgentStopMaxIter        AgentStopReason = "max_iterations"
	AgentStopMaxTokens      AgentStopReason = "max_tokens"
	AgentStopCanceled       AgentStopReason = "canceled"
	AgentStopLoop           AgentStopReason = "loop_detected"
	AgentStopAsync          AgentStopReason = "async_pending"
	AgentStopRetryExhausted AgentStopReason = "retry_exhausted"
)

// AgentRunResponse is the result of an Agent Engine run.
type AgentRunResponse struct {
	Output     string          `json:"output"`
	Messages   []Message       `json:"messages"`
	TotalUsage Usage           `json:"totalUsage"`
	Iterations int             `json:"iterations"`
	StopReason AgentStopReason `json:"stopReason"`
}
