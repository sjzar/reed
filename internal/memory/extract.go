package memory

import (
	"strings"

	"github.com/sjzar/reed/internal/model"
)

// Package-level defaults.
const (
	defaultMaxBytes            = 2000 // MEMORY.md size cap in bytes
	defaultExtractionThreshold = 3    // min user messages to trigger extraction
	memoryFileName             = "MEMORY.md"
	lockFileName               = ".lock"
)

// ExtractionSystemPrompt is the canonical system prompt for memory extraction.
// Used by LLMExtractor implementations.
const ExtractionSystemPrompt = `You are a memory extraction system. Analyze the conversation and extract key facts worth preserving across sessions.

## What to extract
- User preferences and working style
- Important decisions and their rationale
- Project conventions and patterns
- Recurring themes or requirements

## What NOT to extract
- Temporary state or in-progress work details
- Specific debug steps or error messages
- Content that can be derived from code or git history
- Completed task details

## Instructions
Output ONLY new or updated facts, one per line, prefixed with "- ".
If nothing is worth saving, output exactly: NONE
Do not repeat facts already in existing memory unless they need updating.`

// countUserMessages returns the number of user-role messages.
func countUserMessages(messages []model.Message) int {
	n := 0
	for _, m := range messages {
		if m.Role == model.RoleUser {
			n++
		}
	}
	return n
}

// hasAssistantMessage returns true if at least one assistant message exists.
func hasAssistantMessage(messages []model.Message) bool {
	for _, m := range messages {
		if m.Role == model.RoleAssistant {
			return true
		}
	}
	return false
}

// shouldExtract checks whether the conversation qualifies for memory extraction.
func shouldExtract(messages []model.Message) bool {
	return countUserMessages(messages) >= defaultExtractionThreshold && hasAssistantMessage(messages)
}

// mergeMemory combines existing MEMORY.md content with newly extracted facts.
// Returns the merged text.
func mergeMemory(existing, extracted string) string {
	extracted = strings.TrimSpace(extracted)
	if extracted == "" || strings.EqualFold(extracted, "NONE") {
		return existing
	}

	if existing == "" {
		return extracted
	}

	return existing + "\n" + extracted
}
