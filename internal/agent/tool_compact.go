package agent

import (
	"fmt"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// defaultToolCompactMaxChars is the maximum character length for a tool result
// from a previous iteration before it gets truncated. Current-iteration results
// are never touched.
const defaultToolCompactMaxChars = 1000

// compactOldToolResults truncates tool-result messages from previous iterations
// to save context tokens. Messages at index >= boundary (current iteration) are
// left untouched. Error results (IsError) are never truncated because they are
// typically short and critical for debugging.
//
// Mutation is in-place on the Content slice — callers should be aware that the
// original message content is replaced.
func compactOldToolResults(messages []model.Message, boundary, maxChars int) {
	if boundary <= 0 || maxChars <= 0 {
		return
	}
	if boundary > len(messages) {
		boundary = len(messages)
	}
	for i := 0; i < boundary; i++ {
		msg := &messages[i]
		if msg.Role != model.RoleTool || msg.IsError {
			continue
		}
		text := msg.TextContent()
		if len(text) <= maxChars || isAlreadyCompacted(text) {
			continue
		}
		half := maxChars / 2
		truncated := text[:half] + fmt.Sprintf("\n...[truncated: %d chars total]...\n", len(text)) + text[len(text)-half:]
		msg.Content = []model.Content{{
			Type: model.ContentTypeText,
			Text: truncated,
		}}
	}
}

// toolResultTruncateLine builds the truncation marker line.
// Exported for testing.
func toolResultTruncateLine(totalChars int) string {
	return fmt.Sprintf("\n...[truncated: %d chars total]...\n", totalChars)
}

// isAlreadyCompacted checks if a tool result text contains the truncation marker.
func isAlreadyCompacted(text string) bool {
	return strings.Contains(text, "...[truncated:")
}
