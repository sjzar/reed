package agent

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestCompactOldToolResults(t *testing.T) {
	longText := strings.Repeat("x", 2000)

	tests := []struct {
		name     string
		messages []model.Message
		boundary int
		maxChars int
		wantLen  []int // expected len of each message's text content (-1 = unchanged)
	}{
		{
			name:     "no messages",
			messages: nil,
			boundary: 0,
			maxChars: 1000,
			wantLen:  nil,
		},
		{
			name: "boundary zero skips all",
			messages: []model.Message{
				{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
			},
			boundary: 0,
			maxChars: 1000,
			wantLen:  []int{2000},
		},
		{
			name: "truncates tool result before boundary",
			messages: []model.Message{
				{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
				{Role: model.RoleAssistant, Content: []model.Content{{Type: model.ContentTypeText, Text: "ok"}}},
			},
			boundary: 2,
			maxChars: 1000,
		},
		{
			name: "skips error results",
			messages: []model.Message{
				{Role: model.RoleTool, IsError: true, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
			},
			boundary: 1,
			maxChars: 1000,
			wantLen:  []int{2000},
		},
		{
			name: "skips non-tool messages",
			messages: []model.Message{
				{Role: model.RoleUser, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
			},
			boundary: 1,
			maxChars: 1000,
			wantLen:  []int{2000},
		},
		{
			name: "short result unchanged",
			messages: []model.Message{
				{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: "short"}}},
			},
			boundary: 1,
			maxChars: 1000,
			wantLen:  []int{5},
		},
		{
			name: "messages at/after boundary untouched",
			messages: []model.Message{
				{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
				{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
			},
			boundary: 1,
			maxChars: 1000,
			wantLen:  []int{-1, 2000}, // first truncated, second untouched
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to verify in-place mutation
			orig := make([]model.Message, len(tt.messages))
			copy(orig, tt.messages)

			compactOldToolResults(tt.messages, tt.boundary, tt.maxChars)

			for i, msg := range tt.messages {
				text := msg.TextContent()
				if i < len(tt.wantLen) && tt.wantLen[i] >= 0 {
					if len(text) != tt.wantLen[i] {
						t.Errorf("message[%d] len = %d, want %d", i, len(text), tt.wantLen[i])
					}
				}
			}
		})
	}
}

func TestCompactOldToolResults_TruncationFormat(t *testing.T) {
	longText := strings.Repeat("A", 500) + strings.Repeat("B", 500) + strings.Repeat("C", 500)
	messages := []model.Message{
		{Role: model.RoleTool, Content: []model.Content{{Type: model.ContentTypeText, Text: longText}}},
	}
	compactOldToolResults(messages, 1, 1000)

	text := messages[0].TextContent()
	if !strings.HasPrefix(text, strings.Repeat("A", 500)) {
		t.Error("truncated text should preserve head")
	}
	if !strings.HasSuffix(text, strings.Repeat("C", 500)) {
		t.Error("truncated text should preserve tail")
	}
	if !strings.Contains(text, "...[truncated:") {
		t.Error("truncated text should contain marker")
	}
	if len(text) < 1000 {
		t.Errorf("truncated text too short: %d", len(text))
	}
}
