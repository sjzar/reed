package agent

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/model"
)

// inboxEventsToMessages converts inbox SessionEntry records into messages for the
// agent loop. It handles two kinds of entries:
//   - SessionEntryMessage: direct message injections (e.g. steer via bus)
//   - SessionEntryCustom: async tool results (original inbox format)
func inboxEventsToMessages(events []model.SessionEntry) []model.Message {
	var msgs []model.Message
	for _, entry := range events {
		// Direct message entries (e.g. steer injections via bus subscriber).
		if entry.Type == model.SessionEntryMessage && entry.Message != nil {
			msgs = append(msgs, *entry.Message)
			continue
		}

		if entry.Data == nil {
			continue
		}
		payload, _ := entry.Data["payload"].(string)
		if payload == "" {
			continue
		}

		var parsed struct {
			ToolCallID string          `json:"toolCallID"`
			ToolName   string          `json:"toolName"`
			IsError    bool            `json:"isError"`
			Content    []model.Content `json:"content"`
			Text       string          `json:"text"`       // backward compat
			DurationMs *float64        `json:"durationMs"` // async tool execution time
		}
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			// Log truncated payload for debugging (may contain secrets, so limit size).
			truncated := payload
			if len(truncated) > 200 {
				truncated = truncated[:200] + "..."
			}
			// Try to extract toolCallID from raw JSON for correlation
			var raw map[string]json.RawMessage
			toolCallID := ""
			if json.Unmarshal([]byte(payload), &raw) == nil {
				if tcID, ok := raw["toolCallID"]; ok {
					_ = json.Unmarshal(tcID, &toolCallID)
				}
			}
			log.Warn().Err(err).Str("toolCallID", toolCallID).Str("payload", truncated).Msg("inbox payload parse error")
			if toolCallID != "" {
				msgs = append(msgs, model.Message{
					Role:       model.RoleTool,
					Content:    model.TextContent(fmt.Sprintf("inbox parse error: %s", err)),
					ToolCallID: toolCallID,
					IsError:    true,
				})
			}
			continue
		}

		var content []model.Content
		switch {
		case len(parsed.Content) > 0:
			content = parsed.Content
		case parsed.Text != "":
			// Old format fallback
			content = model.TextContent(parsed.Text)
		default:
			content = model.TextContent("(empty result)")
		}

		msg := model.Message{
			Role:       model.RoleTool,
			Content:    content,
			ToolCallID: parsed.ToolCallID,
			ToolName:   parsed.ToolName,
			IsError:    parsed.IsError,
		}
		if parsed.DurationMs != nil {
			msg.Meta = map[string]any{
				"durationMs": int64(*parsed.DurationMs),
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
