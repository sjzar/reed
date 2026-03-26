package base

import (
	"encoding/json"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// ParseToolCallArguments parses a JSON string into a map.
// Returns nil on parse failure (for bad JSON self-repair).
func ParseToolCallArguments(raw string) map[string]any {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

// MarshalToolCallArguments marshals arguments to a JSON string.
func MarshalToolCallArguments(args map[string]any) string {
	if args == nil {
		return "{}"
	}
	data, _ := json.Marshal(args)
	return string(data)
}

// ToolCallAccumulator accumulates partial tool call deltas into complete ToolCalls.
type ToolCallAccumulator struct {
	calls   []model.ToolCall
	argBufs map[int]*strings.Builder
}

// NewToolCallAccumulator creates a ready-to-use accumulator.
func NewToolCallAccumulator() *ToolCallAccumulator {
	return &ToolCallAccumulator{argBufs: make(map[int]*strings.Builder)}
}

// StartCall registers a new tool call at the given index.
func (a *ToolCallAccumulator) StartCall(idx int, id, name string) {
	for len(a.calls) <= idx {
		a.calls = append(a.calls, model.ToolCall{})
	}
	if id != "" {
		a.calls[idx].ID = id
	}
	if name != "" {
		a.calls[idx].Name = name
	}
}

// AppendArgs appends a JSON fragment to the argument buffer at idx.
func (a *ToolCallAccumulator) AppendArgs(idx int, fragment string) {
	if a.argBufs[idx] == nil {
		a.argBufs[idx] = &strings.Builder{}
	}
	a.argBufs[idx].WriteString(fragment)
}

// Finalize parses accumulated argument buffers and returns the complete tool calls.
// Only tool calls with a non-empty ID are included.
func (a *ToolCallAccumulator) Finalize() []model.ToolCall {
	for idx := range a.calls {
		if b, ok := a.argBufs[idx]; ok {
			raw := b.String()
			a.calls[idx].RawJSON = raw
			a.calls[idx].Arguments = ParseToolCallArguments(raw)
		}
	}
	// Filter out empty placeholders
	out := make([]model.ToolCall, 0, len(a.calls))
	for _, tc := range a.calls {
		if tc.ID != "" {
			out = append(out, tc)
		}
	}
	return out
}
