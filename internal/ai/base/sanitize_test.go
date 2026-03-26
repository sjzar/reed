package base

import (
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestSanitizeToolName(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"simple", "read_file", 64, "read_file"},
		{"with slash", "server/tool", 64, "server__tool"},
		{"with dot", "my.tool.name", 64, "my_tool_name"},
		{"special chars", "tool@v2!beta", 64, "tool_v2_beta"},
		{"truncate", "a_very_long_tool_name", 10, "a_very_lon"},
		{"already clean", "clean-name_123", 64, "clean-name_123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeToolName(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("sanitizeToolName(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestOpenAISanitizer_RoundTrip(t *testing.T) {
	tools := []model.ToolDef{
		{Name: "server/read_file"},
		{Name: "my.tool.name"},
		{Name: "simple_tool"},
	}
	s := NewOpenAISanitizer(tools)

	for _, tool := range tools {
		sanitized := s.SanitizeToolName(tool.Name)
		restored := s.RestoreToolName(sanitized)
		if restored != tool.Name {
			t.Errorf("round-trip failed: %q → %q → %q", tool.Name, sanitized, restored)
		}
	}
}

func TestAnthropicSanitizer_RoundTrip(t *testing.T) {
	tools := []model.ToolDef{
		{Name: "server/read_file"},
		{Name: "complex.tool@v2"},
	}
	s := NewAnthropicSanitizer(tools)

	for _, tool := range tools {
		sanitized := s.SanitizeToolName(tool.Name)
		restored := s.RestoreToolName(sanitized)
		if restored != tool.Name {
			t.Errorf("round-trip failed: %q → %q → %q", tool.Name, sanitized, restored)
		}
	}
}

func TestOpenAISanitizer_NormalizeToolCallID(t *testing.T) {
	s := NewOpenAISanitizer(nil)

	id := s.NormalizeToolCallID("call_abc123")
	if id != "call_abc123" {
		t.Errorf("got %q, want %q", id, "call_abc123")
	}

	longID := "call_" + string(make([]byte, 50))
	normalized := s.NormalizeToolCallID(longID)
	if len(normalized) > 40 {
		t.Errorf("ID too long: %d chars", len(normalized))
	}
}

func TestNoopSanitizer(t *testing.T) {
	s := NoopSanitizer{}
	if s.SanitizeToolName("test") != "test" {
		t.Error("SanitizeToolName should pass through")
	}
	if s.RestoreToolName("test") != "test" {
		t.Error("RestoreToolName should pass through")
	}
	if s.NormalizeToolCallID("id") != "id" {
		t.Error("NormalizeToolCallID should pass through")
	}
}

// Bug 4: name collision detection
func TestOpenAISanitizer_NameCollision(t *testing.T) {
	// Two tools with different names that sanitize to the same string
	tools := []model.ToolDef{
		{Name: "server/read.file"},  // → server__read_file
		{Name: "server__read_file"}, // → server__read_file (collision!)
	}
	s := NewOpenAISanitizer(tools)

	san1 := s.SanitizeToolName("server/read.file")
	san2 := s.SanitizeToolName("server__read_file")

	if san1 == san2 {
		t.Errorf("collision not resolved: both mapped to %q", san1)
	}

	// Round-trip should still work
	if s.RestoreToolName(san1) != "server/read.file" {
		t.Errorf("restore 1: got %q", s.RestoreToolName(san1))
	}
	if s.RestoreToolName(san2) != "server__read_file" {
		t.Errorf("restore 2: got %q", s.RestoreToolName(san2))
	}
}

// Regression: collision with names already at maxNameLen caused infinite loop
// because appending "_2" then truncating back to maxLen produced the same name.
func TestOpenAISanitizer_NameCollisionAtMaxLen(t *testing.T) {
	// Create a 64-char base name
	longName := strings.Repeat("a", 64)
	// Two different original names that both sanitize to the same 64-char string
	tools := []model.ToolDef{
		{Name: longName},
		{Name: longName + "_extra"}, // sanitizeToolName truncates to 64 → same as longName
	}
	s := NewOpenAISanitizer(tools)

	san1 := s.SanitizeToolName(longName)
	san2 := s.SanitizeToolName(longName + "_extra")

	if san1 == san2 {
		t.Errorf("collision not resolved: both mapped to %q", san1)
	}
	if len(san1) > 64 {
		t.Errorf("san1 too long: %d chars", len(san1))
	}
	if len(san2) > 64 {
		t.Errorf("san2 too long: %d chars", len(san2))
	}

	// Round-trip
	if s.RestoreToolName(san1) != longName {
		t.Errorf("restore 1: got %q", s.RestoreToolName(san1))
	}
	if s.RestoreToolName(san2) != longName+"_extra" {
		t.Errorf("restore 2: got %q", s.RestoreToolName(san2))
	}
}
