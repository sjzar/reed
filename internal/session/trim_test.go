package session

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestTrimMessages_NoTrim(t *testing.T) {
	msgs := []model.Message{
		model.NewTextMessage(model.RoleSystem, "sys"),
		model.NewTextMessage(model.RoleUser, "hi"),
		model.NewTextMessage(model.RoleAssistant, "hello"),
	}
	result := TrimMessages(msgs, 100000)
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
}

func TestTrimMessages_ZeroBudget(t *testing.T) {
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "hi"),
	}
	result := TrimMessages(msgs, 0)
	if len(result) != 1 {
		t.Errorf("zero budget should return original, got %d", len(result))
	}
}

func TestTrimMessages_PreservesSystemMessage(t *testing.T) {
	msgs := []model.Message{
		model.NewTextMessage(model.RoleSystem, "system prompt"),
		model.NewTextMessage(model.RoleUser, "old message"),
		model.NewTextMessage(model.RoleAssistant, "old reply"),
		model.NewTextMessage(model.RoleUser, "new message"),
		model.NewTextMessage(model.RoleAssistant, "new reply"),
	}
	// Use a small budget that can only fit system + last 2 messages
	// "system prompt" ~4 tokens, "new message" ~3, "new reply" ~3
	result := TrimMessages(msgs, 12)
	if len(result) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(result))
	}
	if result[0].Role != model.RoleSystem {
		t.Error("first message should be system")
	}
	if result[0].TextContent() != "system prompt" {
		t.Errorf("system content: got %q", result[0].TextContent())
	}
}

func TestTrimMessages_TrimsOldMessages(t *testing.T) {
	msgs := []model.Message{
		model.NewTextMessage(model.RoleSystem, "s"),
		model.NewTextMessage(model.RoleUser, strings.Repeat("old ", 100)),
		model.NewTextMessage(model.RoleAssistant, strings.Repeat("old reply ", 100)),
		model.NewTextMessage(model.RoleUser, "recent"),
		model.NewTextMessage(model.RoleAssistant, "reply"),
	}
	// Budget enough for system + last 2 but not the old ones
	result := TrimMessages(msgs, 15)
	if result[0].Role != model.RoleSystem {
		t.Error("first message should be system")
	}
	// Should have trimmed the old messages
	if len(result) >= 5 {
		t.Errorf("expected trimming, got %d messages", len(result))
	}
	// Last message should be the most recent
	last := result[len(result)-1]
	if last.TextContent() != "reply" {
		t.Errorf("last message: got %q, want %q", last.TextContent(), "reply")
	}
}

func TestTrimMessages_NoSystemMessage(t *testing.T) {
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, strings.Repeat("old ", 200)),
		model.NewTextMessage(model.RoleAssistant, strings.Repeat("old ", 200)),
		model.NewTextMessage(model.RoleUser, "new"),
	}
	result := TrimMessages(msgs, 10)
	if len(result) == 0 {
		t.Fatal("expected at least 1 message")
	}
	last := result[len(result)-1]
	if last.TextContent() != "new" {
		t.Errorf("last message: got %q, want %q", last.TextContent(), "new")
	}
}

func TestTrimMessages_Empty(t *testing.T) {
	result := TrimMessages(nil, 100)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}
