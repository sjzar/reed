// Package session manages the lifecycle of LLM conversation sessions.
//
// Core responsibilities:
//   - Route resolution: (namespace, agentID, sessionKey) → sessionID, 4am TTL
//   - Serial guard: per-session-key at-most-one-active-run mutex
//   - JSONL persistence: append-only SessionEntry records (messages + compaction events)
//   - Compaction-aware loading: LoadContext = post-last-compaction view; Load = raw full history
//   - LLM-driven compaction: Compact summarises older messages via LLMCompressor
//   - Inbox: async event delivery via per-session sidecar file
//   - Pending jobs: async tool call tracking with waiter notification
//
// Architecture boundary: session owns durable context (summary + recent messages).
// It does NOT generate or manage the current run's dynamic system prompt.
// The JSONL persistence format is strictly SessionEntry — no bare-message compat.
package session

import (
	"context"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// Session is the public interface for session lifecycle management.
// All session operations — routing, persistence, compaction, inbox, pending jobs —
// are accessed through this interface.
type Session interface {
	// FindSessionID performs a read-only lookup for an existing session route.
	FindSessionID(ctx context.Context, namespace, agentID, sessionKey string) (string, error)

	// Acquire resolves or creates a session route and acquires a per-key serial lock.
	// If sessionKey is empty, a random session ID is returned with a no-op release func.
	// The caller must call release() when done.
	Acquire(ctx context.Context, namespace, agentID, sessionKey string) (sessionID string, release func(), err error)

	// AcquireByID reverse-looks up a session_id via the route store,
	// validates it exists, and acquires a serial lock on that session.
	// Returns a release function. The caller must call release() when done.
	// Requires the session to have an active route (created via Acquire with a non-empty
	// sessionKey). Sessions created with an empty sessionKey have no route and cannot
	// be acquired by ID. Rolled-over sessions (past 4am TTL) are also unreachable.
	AcquireByID(ctx context.Context, sessionID string) (release func(), err error)

	// Load returns the full raw message history for a session (compaction-blind).
	// Every persisted message is returned regardless of compaction state.
	// Compaction entries are ignored. Useful for debugging and export.
	Load(ctx context.Context, sessionID string) ([]model.Message, error)

	// LoadContext returns the session-owned durable context (summary + recent messages).
	// This is the compaction-aware, cached view that agents use for building LLM context.
	// If compaction exists: [summary_system_msg] + non-system messages after cursor.
	// If no compaction exists: all non-system messages.
	// System messages from prior runs are filtered out because the engine always
	// prepends a fresh system prompt on each run.
	// Does NOT include the current run's dynamic system prompt.
	LoadContext(ctx context.Context, sessionID string) ([]model.Message, error)

	// Compact performs LLM-driven compaction on durable session history only.
	// Messages older than the last KeepRecentN are compressed into a summary.
	// Returns summary + recent messages. Does not include dynamic system prompt.
	// Returns empty slice (not nil) when no messages exist.
	Compact(ctx context.Context, sessionID string, opts CompactOptions) ([]model.Message, error)

	// AppendMessages persists a batch of messages to the session JSONL.
	AppendMessages(ctx context.Context, sessionID string, msgs []model.Message) error

	// RegisterPendingJob marks a job as pending for the given session.
	RegisterPendingJob(ctx context.Context, sessionID, jobID string) error

	// FinishPendingJob marks a job as complete and notifies waiters.
	FinishPendingJob(ctx context.Context, sessionID, jobID string) error

	// HasPendingJobs returns true if the session has pending jobs.
	HasPendingJobs(sessionID string) bool

	// WaitPendingJobs blocks until all pending jobs for the session complete or ctx is canceled.
	WaitPendingJobs(ctx context.Context, sessionID string) error

	// AppendInbox writes an event to the session's inbox file.
	AppendInbox(ctx context.Context, sessionID string, entry model.SessionEntry) error

	// FetchAndClearInbox reads all inbox events and clears the inbox file.
	FetchAndClearInbox(ctx context.Context, sessionID string) ([]model.SessionEntry, error)
}

// CompactOptions controls compaction behavior.
type CompactOptions struct {
	// KeepRecentN is the number of recent messages to preserve (not compressed).
	// Zero value falls back to defaultKeepRecentN (2).
	KeepRecentN int
}

// LLMCompressor generates a summary of messages for context compaction.
type LLMCompressor interface {
	Compress(ctx context.Context, messages []model.Message) (string, error)
}

// RouteStore defines the persistence interface for session routes.
// Implemented by db.SessionRouteRepo.
//
// Not-found contract: both Find and FindBySessionID return (nil, nil)
// when no matching route exists. Callers must check the returned pointer.
type RouteStore interface {
	Upsert(ctx context.Context, row *model.SessionRouteRow) error
	// Find returns (nil, nil) when no matching route exists.
	Find(ctx context.Context, namespace, agentID, sessionKey string) (*model.SessionRouteRow, error)
	// FindBySessionID returns (nil, nil) when no matching route exists.
	FindBySessionID(ctx context.Context, sessionID string) (*model.SessionRouteRow, error)
	Delete(ctx context.Context, namespace, agentID, sessionKey string) error
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

// IDGenerator abstracts session ID generation for testability.
type IDGenerator interface {
	NewSessionID() string
}
