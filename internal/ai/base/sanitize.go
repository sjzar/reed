package base

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// Sanitizer handles provider-specific tool name/ID constraints.
type Sanitizer interface {
	// SanitizeToolName converts an original tool name to a provider-compatible format.
	SanitizeToolName(name string) string
	// RestoreToolName restores a provider-returned tool name to the original.
	RestoreToolName(name string) string
	// NormalizeToolCallID normalizes a tool call ID.
	NormalizeToolCallID(id string) string
}

// NoopSanitizer passes through all names unchanged.
type NoopSanitizer struct{}

func (NoopSanitizer) SanitizeToolName(name string) string  { return name }
func (NoopSanitizer) RestoreToolName(name string) string   { return name }
func (NoopSanitizer) NormalizeToolCallID(id string) string { return id }

// baseSanitizer provides shared sanitizer logic parameterized by max name and ID lengths.
type baseSanitizer struct {
	nameMap    map[string]string // sanitized → original
	reverseMap map[string]string // original → sanitized
	maxNameLen int
	maxIDLen   int
}

func newBaseSanitizer(tools []model.ToolDef, maxNameLen, maxIDLen int) baseSanitizer {
	s := baseSanitizer{
		nameMap:    make(map[string]string, len(tools)),
		reverseMap: make(map[string]string, len(tools)),
		maxNameLen: maxNameLen,
		maxIDLen:   maxIDLen,
	}
	for _, t := range tools {
		sanitized := sanitizeToolName(t.Name, maxNameLen)
		// Bug 4 fix: detect name collisions and append counter suffix.
		// Trim the base name to make room for the suffix so it is never truncated,
		// guaranteeing each candidate is unique in form and preventing infinite loops
		// when the base name is already at maxNameLen.
		if _, exists := s.nameMap[sanitized]; exists {
			for counter := 2; counter < 10000; counter++ {
				suffix := fmt.Sprintf("_%d", counter)
				base := sanitized
				if len(base)+len(suffix) > maxNameLen {
					base = base[:maxNameLen-len(suffix)]
				}
				candidate := base + suffix
				if _, exists := s.nameMap[candidate]; !exists {
					sanitized = candidate
					break
				}
			}
		}
		s.nameMap[sanitized] = t.Name
		s.reverseMap[t.Name] = sanitized
	}
	return s
}

func (s *baseSanitizer) SanitizeToolName(name string) string {
	if sanitized, ok := s.reverseMap[name]; ok {
		return sanitized
	}
	return sanitizeToolName(name, s.maxNameLen)
}

func (s *baseSanitizer) RestoreToolName(name string) string {
	if original, ok := s.nameMap[name]; ok {
		return original
	}
	return name
}

func (s *baseSanitizer) NormalizeToolCallID(id string) string {
	if len(id) > s.maxIDLen {
		id = id[:s.maxIDLen]
	}
	return sanitizeID(id)
}

// OpenAISanitizer handles OpenAI's tool name constraints.
// Tool call ID: ≤40 chars, [a-zA-Z0-9_-]
// Tool name: [a-zA-Z0-9_-], max 64 chars
type OpenAISanitizer struct {
	baseSanitizer
}

// NewOpenAISanitizer creates a sanitizer pre-loaded with tool definitions.
func NewOpenAISanitizer(tools []model.ToolDef) *OpenAISanitizer {
	return &OpenAISanitizer{baseSanitizer: newBaseSanitizer(tools, 64, 40)}
}

// AnthropicSanitizer handles Anthropic's tool name constraints.
// Tool call ID: ≤64 chars, [a-zA-Z0-9_-]
type AnthropicSanitizer struct {
	baseSanitizer
}

// NewAnthropicSanitizer creates a sanitizer pre-loaded with tool definitions.
func NewAnthropicSanitizer(tools []model.ToolDef) *AnthropicSanitizer {
	return &AnthropicSanitizer{baseSanitizer: newBaseSanitizer(tools, 64, 64)}
}

var invalidNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitizeToolName replaces invalid characters and truncates to maxLen.
func sanitizeToolName(name string, maxLen int) string {
	name = strings.ReplaceAll(name, "/", "__")
	name = strings.ReplaceAll(name, ".", "_")
	name = invalidNameChars.ReplaceAllString(name, "_")
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

// sanitizeID removes invalid characters from a tool call ID.
func sanitizeID(id string) string {
	return invalidNameChars.ReplaceAllString(id, "_")
}
