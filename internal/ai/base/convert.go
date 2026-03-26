package base

import "github.com/sjzar/reed/internal/model"

// MapStopReason maps provider-specific stop reasons to model.StopReason.
func MapStopReason(reason string) model.StopReason {
	switch reason {
	case "tool_calls", "tool_use":
		return model.StopReasonToolUse
	case "length", "max_tokens":
		return model.StopReasonMaxTokens
	default:
		return model.StopReasonEnd
	}
}
