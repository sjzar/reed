package model

import (
	"time"

	"github.com/google/uuid"
)

// SessionEntry type constants.
const (
	SessionEntryMessage    = "message"
	SessionEntryCompaction = "compaction"
	SessionEntryCustom     = "custom"
)

// SessionEntry is the unified persistence format for session JSONL files.
// Every line in a session file is a SessionEntry, enabling event-sourcing style
// compaction and custom event injection.
type SessionEntry struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	ParentID  string         `json:"parentID,omitempty"`
	Timestamp int64          `json:"timestamp"`
	Meta      map[string]any `json:"meta,omitempty"` // runtime metadata (agentID, model, iteration, etc.)

	// type == "message"
	Message    *Message `json:"message,omitempty"`
	StopReason string   `json:"stopReason,omitempty"`
	IsError    bool     `json:"isError,omitempty"`

	// type == "custom"
	CustomType string         `json:"customType,omitempty"`
	Data       map[string]any `json:"data,omitempty"` // payload for custom entries

	// type == "compaction"
	Cursor       string `json:"cursor,omitempty"`
	Summary      string `json:"summary,omitempty"`
	TokensBefore int    `json:"tokensBefore,omitempty"`
	TokensAfter  int    `json:"tokensAfter,omitempty"`
}

// NewMessageSessionEntry creates a SessionEntry wrapping a conversation message.
// Usage is stored inside msg.Usage (not duplicated at the entry level).
func NewMessageSessionEntry(msg Message, parentID string) SessionEntry {
	return SessionEntry{
		Type:      SessionEntryMessage,
		ID:        uuid.New().String(),
		ParentID:  parentID,
		Timestamp: time.Now().UnixMilli(),
		Message:   &msg,
	}
}

// NewCompactionSessionEntry creates a SessionEntry recording a compaction event.
func NewCompactionSessionEntry(cursor, summary string, tokensBefore, tokensAfter int) SessionEntry {
	return SessionEntry{
		Type:         SessionEntryCompaction,
		ID:           uuid.New().String(),
		Timestamp:    time.Now().UnixMilli(),
		Cursor:       cursor,
		Summary:      summary,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}
}

// NewCustomSessionEntry creates a SessionEntry for application-specific events.
func NewCustomSessionEntry(customType string, data map[string]any) SessionEntry {
	return SessionEntry{
		Type:       SessionEntryCustom,
		ID:         uuid.New().String(),
		Timestamp:  time.Now().UnixMilli(),
		CustomType: customType,
		Data:       data,
	}
}
