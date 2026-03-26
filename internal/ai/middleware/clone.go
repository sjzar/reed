// Package middleware provides composable RoundTripper middleware for the AI pipeline.
package middleware

import (
	"context"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/jsonutil"
)

// Clone wraps a RoundTripper, deep-copying the request so the original is never modified.
// This is critical for failover retries where the same request is sent to multiple providers.
type Clone struct {
	next base.RoundTripper
}

// NewClone creates a Clone middleware.
func NewClone(next base.RoundTripper) *Clone {
	return &Clone{next: next}
}

func (c *Clone) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	return c.next.Responses(ctx, deepCopyRequest(req))
}

// deepCopyRequest creates a deep copy of a Request.
func deepCopyRequest(req *model.Request) *model.Request {
	clone := *req

	// Deep-copy Messages slice using Message.Clone() which handles
	// Content, ToolCalls, Usage, and Meta. Then deep-copy nested maps
	// in ToolCall Arguments that Clone() only shallow-copies.
	if len(req.Messages) > 0 {
		clone.Messages = make([]model.Message, len(req.Messages))
		for i, m := range req.Messages {
			clone.Messages[i] = m.Clone()
			for j, tc := range clone.Messages[i].ToolCalls {
				if len(tc.Arguments) > 0 {
					clone.Messages[i].ToolCalls[j].Arguments = jsonutil.DeepClone(tc.Arguments).(map[string]any)
				}
			}
		}
	}

	// Deep-copy Tools
	if len(req.Tools) > 0 {
		clone.Tools = make([]model.ToolDef, len(req.Tools))
		copy(clone.Tools, req.Tools)
		for i, t := range clone.Tools {
			if len(t.InputSchema) > 0 {
				clone.Tools[i].InputSchema = jsonutil.DeepClone(t.InputSchema).(map[string]any)
			}
			if len(t.Examples) > 0 {
				examples := make([]model.ToolExample, len(t.Examples))
				for j, ex := range t.Examples {
					examples[j] = ex
					if len(ex.Input) > 0 {
						examples[j].Input = jsonutil.DeepClone(ex.Input).(map[string]any)
					}
				}
				clone.Tools[i].Examples = examples
			}
		}
	}

	// Deep-copy Stop
	if len(req.Stop) > 0 {
		clone.Stop = make([]string, len(req.Stop))
		copy(clone.Stop, req.Stop)
	}

	// Deep-copy Schema
	if len(req.Schema) > 0 {
		clone.Schema = jsonutil.DeepClone(req.Schema).(map[string]any)
	}

	// Deep-copy ExtraParams
	if len(req.ExtraParams) > 0 {
		clone.ExtraParams = jsonutil.DeepClone(req.ExtraParams).(map[string]any)
	}

	return &clone
}
