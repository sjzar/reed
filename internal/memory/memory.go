// Package memory provides the long-term memory interface for agent runs.
//
// Memory scope is determined by the 3D session key: namespace/agentID/sessionKey.
// Each unique combination gets its own MEMORY.md file on disk. Memory injection
// (BeforeRun) is always non-fatal — a missing or unreadable file returns empty
// content. Memory extraction (AfterRun) is best-effort with a 60s timeout.
package memory

import (
	"context"

	"github.com/sjzar/reed/internal/model"
)

// RunContext carries identity info for memory scoping.
// The scope key is derived from Namespace/AgentID/SessionKey (matching session routing).
type RunContext struct {
	Namespace  string
	AgentID    string
	SessionKey string
}

// MemoryResult is the structured output from BeforeRun.
type MemoryResult struct {
	// Content is the curated MEMORY.md text, ready for prompt injection.
	// Empty string means no memory exists yet.
	Content string
}

// Provider is the memory lifecycle interface invoked before and after agent runs.
type Provider interface {
	// BeforeRun loads relevant memories for prompt injection.
	// Returns empty MemoryResult (not error) when no memory exists.
	BeforeRun(ctx context.Context, rc RunContext) (MemoryResult, error)

	// AfterRun extracts and persists memories from the completed run.
	// Only extracts when user message count >= threshold (multi-turn conversations).
	AfterRun(ctx context.Context, rc RunContext, messages []model.Message) error
}

// LLMExtractor extracts memorable facts from a conversation.
type LLMExtractor interface {
	// ExtractFacts extracts key facts worth preserving across sessions.
	// messages is the conversation history; may be nil when called for
	// memory consolidation (compressing existing memory only).
	// existingMemory is the current MEMORY.md content (may be empty).
	// Returns extracted facts as text, or empty string if nothing worth saving.
	ExtractFacts(ctx context.Context, messages []model.Message, existingMemory string) (string, error)
}
