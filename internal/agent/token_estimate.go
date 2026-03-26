package agent

import (
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/token"
)

// MediaTokenWeights holds per-media-type token weights for estimation.
// Future: model-specific weight tables can override DefaultMediaWeights.
type MediaTokenWeights struct {
	Image    int
	Document int
}

// DefaultMediaWeights are the default token weights for media content blocks.
var DefaultMediaWeights = MediaTokenWeights{
	Image:    1000,
	Document: 2000,
}

// estimateRequestTokens estimates the prompt token count for the current request.
// It scans messages in reverse for the last assistant message with Usage data,
// using its Input as a base and only estimating newly added messages since then.
func estimateRequestTokens(messages []model.Message) int {
	return estimateRequestTokensWithWeights(messages, DefaultMediaWeights)
}

// estimateRequestTokensWithWeights is the weight-parameterized version of estimateRequestTokens.
func estimateRequestTokensWithWeights(messages []model.Message, w MediaTokenWeights) int {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role == model.RoleAssistant && m.Usage != nil && m.Usage.Input > 0 {
			var newTokens int
			for _, nm := range messages[i+1:] {
				newTokens += estimateMessageTokensWithWeights(&nm, w)
			}
			return m.Usage.Input + newTokens
		}
	}
	var total int
	for _, m := range messages {
		total += estimateMessageTokensWithWeights(&m, w)
	}
	return total
}

// estimateMessageTokens estimates tokens for a single message,
// adding fixed weights for image and document content blocks.
func estimateMessageTokens(m *model.Message) int {
	return estimateMessageTokensWithWeights(m, DefaultMediaWeights)
}

// estimateMessageTokensWithWeights is the weight-parameterized version of estimateMessageTokens.
func estimateMessageTokensWithWeights(m *model.Message, w MediaTokenWeights) int {
	tokens := token.Estimate(m.TextContent())
	for _, c := range m.Content {
		switch c.Type {
		case model.ContentTypeImage:
			tokens += w.Image
		case model.ContentTypeDocument:
			tokens += w.Document
		}
	}
	return tokens
}
