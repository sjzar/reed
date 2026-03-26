package base

import "strings"

// ValidThinkingLevels is the set of all valid Thinking values.
var ValidThinkingLevels = map[string]bool{
	"":         true,
	"none":     true,
	"minimal":  true,
	"low":      true,
	"medium":   true,
	"high":     true,
	"xhigh":    true,
	"adaptive": true,
}

// NormalizeThinking validates and normalizes a Thinking level string.
// Invalid values degrade to empty string (thinking disabled).
func NormalizeThinking(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if ValidThinkingLevels[level] {
		return level
	}
	return ""
}
