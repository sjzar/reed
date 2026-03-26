package model

import (
	"context"
	"io"
)

// Thinking preserves the LLM's chain-of-thought / reasoning process.
// Use Content blocks with Type="thinking" instead. Kept for Response compatibility.
type Thinking struct {
	Content   string `json:"content"`             // thinking text
	Signature string `json:"signature,omitempty"` // provider-specific signature (Anthropic cache)
}

// ToolExample documents an example invocation for a tool definition.
type ToolExample struct {
	Input  map[string]any `json:"input"`
	Output string         `json:"output,omitempty"` // brief expected output
	Note   string         `json:"note,omitempty"`   // when to use this pattern
}

// ToolDef is a tool definition exposed to the LLM.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`        // JSON Schema
	Summary     string         `json:"summary,omitempty"`  // short one-line summary for system prompt (falls back to first line of Description)
	Examples    []ToolExample  `json:"examples,omitempty"` // example invocations for LLM guidance
}

// Request is the request sent to an LLM.
type Request struct {
	Target          string // target model reference for routing/failover
	Model           string
	AgentID         string // agent ID for routing/failover context
	Messages        []Message
	Tools           []ToolDef
	MaxTokens       int
	Temperature     *float64
	TopP            *float64
	Stop            []string
	EstimatedTokens int            // caller-estimated prompt token count (used for failover context window check)
	Schema          map[string]any // JSON Schema for structured output (response_format)
	Thinking        string         // thinking level: "none","minimal","low","medium","high","xhigh","adaptive"; empty = disabled
	TimeoutMs       int            // per-request timeout in milliseconds (0 = no timeout)
	ExtraParams     map[string]any // provider-specific extra parameters; not yet consumed by any handler
}

// Response is the complete response from an LLM.
type Response struct {
	Content    string     // text content
	Thinking   *Thinking  // chain-of-thought (if any)
	ToolCalls  []ToolCall // tool calls requested by the LLM (IDs and names normalized)
	StopReason StopReason // reason the LLM stopped generating
	Usage      Usage      // token usage
}

// StopReason identifies why the LLM stopped generating.
type StopReason string

const (
	StopReasonEnd       StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

// ResponseStream is a streaming response iterator.
type ResponseStream interface {
	// Next returns the next event. Returns io.EOF when the stream ends.
	Next(ctx context.Context) (StreamEvent, error)
	// Close releases resources.
	Close() error
	// Response returns the complete response after the stream ends.
	Response() *Response
}

// StreamEvent is a single event in a streaming response.
type StreamEvent struct {
	Type     StreamEventType
	Delta    string    // text delta
	ToolCall *ToolCall // tool call delta (partial)
}

// StreamEventType identifies the kind of stream event.
type StreamEventType string

const (
	StreamEventTextDelta     StreamEventType = "text_delta"
	StreamEventToolCallDelta StreamEventType = "toolcall_delta"
	StreamEventThinkingDelta StreamEventType = "thinking_delta"
	StreamEventDone          StreamEventType = "done"
)

// DrainStream consumes a ResponseStream and returns the final Response.
func DrainStream(ctx context.Context, stream ResponseStream) (*Response, error) {
	defer stream.Close()
	for {
		_, err := stream.Next(ctx)
		if err != nil {
			if err == io.EOF {
				return stream.Response(), nil
			}
			return nil, err
		}
	}
}

// ToolResult is the result of a tool invocation (e.g. from MCP).
type ToolResult struct {
	Content []Content
	IsError bool
}

// ContentSize returns the total size of all content blocks in bytes.
func (r *ToolResult) ContentSize() int {
	total := 0
	for _, b := range r.Content {
		total += len(b.Text) + len(b.MediaURI)
	}
	return total
}

// Truncate truncates the content to fit within maxBytes.
// Text blocks are truncated in place; media blocks (image/document) are dropped
// entirely when the budget is exhausted since they can't be meaningfully truncated.
func (r *ToolResult) Truncate(maxBytes int) {
	remaining := maxBytes
	kept := 0
	for i := range r.Content {
		if remaining <= 0 {
			break
		}
		blockSize := len(r.Content[i].Text) + len(r.Content[i].MediaURI)
		if r.Content[i].MediaURI != "" {
			// Media blocks: keep whole or drop entirely
			if blockSize <= remaining {
				r.Content[kept] = r.Content[i]
				kept++
				remaining -= blockSize
			}
			// else: drop (skip)
		} else {
			// Text blocks: truncate if needed
			if len(r.Content[i].Text) > remaining {
				r.Content[i].Text = r.Content[i].Text[:remaining]
				r.Content[kept] = r.Content[i]
				kept++
				remaining = 0
			} else {
				r.Content[kept] = r.Content[i]
				kept++
				remaining -= len(r.Content[i].Text)
			}
		}
	}
	r.Content = r.Content[:kept]
}

// ModelMetadata carries model capabilities info.
// Defined in model package so both ai and agent can reference it without cycles.
// Bool fields use *bool tri-state: nil = unspecified (use fallback), true/false = explicit.
type ModelMetadata struct {
	Name          string
	Thinking      *bool
	Vision        *bool
	Streaming     *bool
	ContextWindow int
	MaxTokens     int
}

// BoolPtr returns a pointer to a bool value.
func BoolPtr(b bool) *bool { return &b }

// BoolVal returns the value of a *bool, defaulting to false if nil.
func BoolVal(b *bool) bool { return b != nil && *b }
