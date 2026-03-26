package session

import (
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/token"
)

// TrimMessages trims message history to fit within a token budget.
// Strategy: always preserve the first system message, then keep messages
// from the tail until the budget is exhausted.
func TrimMessages(messages []model.Message, maxTokens int) []model.Message {
	if maxTokens <= 0 || len(messages) == 0 {
		return messages
	}

	// Separate first system message if present
	var systemMsg *model.Message
	rest := messages
	if messages[0].Role == model.RoleSystem {
		systemMsg = &messages[0]
		rest = messages[1:]
	}

	// Calculate system message cost
	used := 0
	if systemMsg != nil {
		used = token.Estimate(systemMsg.TextContent())
		if used >= maxTokens {
			// Budget can't even fit the system message — return just it
			return []model.Message{*systemMsg}
		}
	}

	budget := maxTokens - used

	// Walk from tail, accumulate token costs
	costs := make([]int, len(rest))
	for i, msg := range rest {
		costs[i] = token.Estimate(msg.TextContent())
	}

	// Find the cutoff index: keep as many trailing messages as fit
	accumulated := 0
	cutoff := len(rest)
	for i := len(rest) - 1; i >= 0; i-- {
		accumulated += costs[i]
		if accumulated > budget {
			cutoff = i + 1
			break
		}
		cutoff = i
	}

	kept := rest[cutoff:]
	if systemMsg != nil {
		result := make([]model.Message, 0, 1+len(kept))
		result = append(result, *systemMsg)
		result = append(result, kept...)
		return result
	}
	return kept
}
