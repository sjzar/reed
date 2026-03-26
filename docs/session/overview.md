---
title: Session System
summary: Session routing, JSONL persistence, compaction, inbox, trimming, pending jobs, ParentID tracking
read_when: working with session management, debugging session state, understanding context compaction
---

# Session System

Package: `internal/session`

## Overview

The session service manages LLM conversation state: route resolution, serial guarding, JSONL persistence, compaction-aware loading, LLM-driven compaction, async inbox, pending job tracking, and ParentID chain management.

Architectural boundary: session owns durable context (summary + recent messages). It does NOT manage the current run's dynamic system prompt.

## Session Interface

All operations are accessed through the `Session` interface:

```go
type Session interface {
    FindSessionID(ctx context.Context, namespace, agentID, sessionKey string) (string, error)
    Acquire(ctx context.Context, namespace, agentID, sessionKey string) (sessionID string, release func(), err error)
    AcquireByID(ctx context.Context, sessionID string) (release func(), err error)
    Load(ctx context.Context, sessionID string) ([]model.Message, error)
    LoadContext(ctx context.Context, sessionID string) ([]model.Message, error)
    Compact(ctx context.Context, sessionID string, opts CompactOptions) ([]model.Message, error)
    AppendMessages(ctx context.Context, sessionID string, msgs []model.Message) error
    RegisterPendingJob(ctx context.Context, sessionID, jobID string) error
    FinishPendingJob(ctx context.Context, sessionID, jobID string) error
    HasPendingJobs(sessionID string) bool
    WaitPendingJobs(ctx context.Context, sessionID string) error
    AppendInbox(ctx context.Context, sessionID string, entry model.SessionEntry) error
    FetchAndClearInbox(ctx context.Context, sessionID string) ([]model.SessionEntry, error)
}
```

## Service

Constructor: `New(sessionDir, routeStore, clock, idGen, opts...)`.

Functional options:

- `WithInbox(dir)` — enable inbox sidecar file storage.
- `WithCompressor(c)` — set `LLMCompressor` for `Compact()`.
- `WithCacheTTL(ttl)` — set cache expiry (default 45 seconds).
- `WithLogger(l)` — set zerolog logger.

Default cache TTL is 45 seconds. Clock and IDGenerator default to real system clock and UUID generator if nil.

## Session Routing (3D Key)

Routes map `(namespace, agentID, sessionKey)` to a `sessionID`. The route is persisted as a `model.SessionRouteRow`:

```go
type SessionRouteRow struct {
    Namespace        string
    AgentID          string
    SessionKey       string
    CurrentSessionID string
    UpdatedAt        time.Time
}
```

### Route Operations

| Method | Route Store | TTL Check | Updates `UpdatedAt` | Lock Key |
|--------|:-----------:|:---------:|:-------------------:|----------|
| `FindSessionID` | required | no | no | none |
| `Acquire` (non-empty key) | required | yes | yes | `namespace/agentID/sessionKey` |
| `Acquire` (empty key) | not used | no | no | none (random ID, no-op release) |
| `AcquireByID` | required | no | no | `sid:<sessionID>` |

`FindSessionID(ctx, namespace, agentID, sessionKey)` performs a read-only route lookup. Returns error if `sessionKey` is empty or route store is not configured. Does **not** enforce TTL or acquire any lock.

`Acquire(ctx, namespace, agentID, sessionKey)` resolves or creates a route and acquires a per-key serial lock.

- If `sessionKey` is empty: returns a random session ID with a no-op release function (no routing, no lock).
- If `sessionKey` is non-empty: acquires the per-key serial lock (key = `namespace/agentID/sessionKey`), then calls `resolveRoute()` which enforces TTL and refreshes `UpdatedAt`.

`AcquireByID(ctx, sessionID)` reverse-looks up the session ID via the route store and acquires a serial lock keyed by `sid:<sessionID>`. Note: this lock key is **different** from the route key used by `Acquire`, so `AcquireByID` does not serialize with `Acquire` for the same session.

### 4am TTL Logic

TTL is enforced only during route resolution inside `Acquire()` — not on read-only lookups. The boundary is 4:00 AM local time (`logicalDayOffset = 4 * time.Hour`).

The `isExpired` method subtracts 4 hours from both `updatedAt` and `now`, then compares calendar dates. If the dates differ, the session is expired and a new session ID is created. If not expired, the existing route's `UpdatedAt` timestamp is refreshed.

### Serial Guard

Each lock key gets its own buffered channel (`chan struct{}` with buffer size 1). `acquireLock(key)` blocks until `ch <- struct{}{}` succeeds or the context is canceled. The returned `release` function drains the channel.

`Acquire` uses the route key (`namespace/agentID/sessionKey`), guaranteeing at-most-one-active-run per route. `AcquireByID` uses `sid:<sessionID>` as a separate lock namespace. Different keys are fully concurrent — no cross-key blocking.

## JSONL Persistence

Each session is stored as `<sessionDir>/<sessionID>.jsonl`. Every line is a `model.SessionEntry`:

```go
type SessionEntry struct {
    Type      string         `json:"type"`       // "session", "message", "compaction", "custom"
    ID        string         `json:"id"`         // Auto-generated UUID
    ParentID  string         `json:"parentID,omitempty"`
    Timestamp int64          `json:"timestamp"`  // Unix milliseconds
    Meta      map[string]any `json:"meta,omitempty"`

    // type == "message"
    Message    *Message `json:"message,omitempty"`
    StopReason string   `json:"stopReason,omitempty"`
    IsError    bool     `json:"isError,omitempty"`

    // type == "custom"
    CustomType string         `json:"customType,omitempty"`
    Data       map[string]any `json:"data,omitempty"`

    // type == "compaction"
    Cursor       string `json:"cursor,omitempty"`
    Summary      string `json:"summary,omitempty"`
    TokensBefore int    `json:"tokensBefore,omitempty"`
}
```

Entry types:

| Type | Constant | Description |
|------|----------|-------------|
| `session` | `SessionEntrySession` | Session-level events |
| `message` | `SessionEntryMessage` | Conversation message with optional ParentID |
| `compaction` | `SessionEntryCompaction` | Compaction event with cursor + summary |
| `custom` | `SessionEntryCustom` | Application-specific events (customType + data) |

Writers are cached per session (`bufio.Writer` pool with corresponding `*os.File` handles). Unparseable JSONL lines are skipped with a warning log rather than causing a hard failure.

The `Close()` method flushes all buffered writers and closes all file handles. Writers are only released via `Close()` — they remain open for the lifetime of the service otherwise.

## Loading

| Method | Returns | Compaction-Aware | Cached | Filters System Msgs | Compressor Required | Empty Session |
|--------|---------|:----------------:|:------:|:-------------------:|:-------------------:|---------------|
| `Load` | Raw full history | no | no | no | no | `nil, nil` |
| `LoadContext` | Post-compaction view | yes | yes (45s) | yes | no | `nil, nil` |
| `Compact` | Compacted view | yes | invalidates | yes | **yes** (errors if nil) | `[], nil` |

### Load

`Load(ctx, sessionID)` returns the raw full message history. Walks all entries, returns every `SessionEntryMessage` as a `model.Message`. Compaction entries are ignored. No caching.

### LoadContext

`LoadContext(ctx, sessionID)` returns the compaction-aware, cached view used for LLM context assembly. Returns `nil, nil` for an empty session (no entries).

1. Check 45-second cache. If hit, return cached copy.
2. On cache miss: scan all entries for the last `SessionEntryCompaction`.
3. If compaction exists: prepend summary as a system message, then append all messages after the cursor.
4. If cursor is not found in entries: warn and fall back to full history (prevents data loss on corrupted compaction records).
5. **Filter out persisted system messages** — the engine always prepends a fresh system prompt each run, so persisted system messages are excluded to prevent duplicates.
6. Cache result with defensive copy (caller mutations do not corrupt the cache).

## Compaction

`Compact(ctx, sessionID, opts)` performs LLM-driven context compaction. **Requires a configured `LLMCompressor`** — returns error immediately if compressor is nil.

`CompactOptions`:

```go
type CompactOptions struct {
    KeepRecentN int  // Default 2 if zero
}
```

Process:

1. Load all entries, find post-cursor messages (messages after the last compaction, or all messages if no prior compaction).
2. If no post-cursor messages: return `[summary_system_msg]` if prior summary exists, otherwise `[]` (empty slice).
3. Split: `toCompress = postCursorMsgs[:len-keepN]`, `kept = postCursorMsgs[len-keepN:]`.
4. If nothing to compress (all messages within keepN): return `[summary_system_msg] + all_post_cursor_msgs` if prior summary exists, otherwise `postCursorMsgs` as-is.
5. If compressing:
   - Prepend any existing prior summary to the compressor input for continuity.
   - Call `LLMCompressor.Compress(ctx, messages)` to generate a summary.
   - Set cursor to the last message entry ID of the compressed portion.
   - Persist a `SessionEntryCompaction` record with cursor, summary, and estimated token count.
   - Invalidate cache.
6. Return `[summary_system_msg] + kept_messages`.

Multiple compactions are supported incrementally — each new compaction references only the latest `SessionEntryCompaction` cursor.

## ParentID & Tool Call Tracking

`AppendMessages` assigns a `ParentID` to each persisted entry, forming a causal chain.

### Default Chain

Each message's `ParentID` defaults to the current leaf ID (the last message entry ID in the session). After persisting, the new entry becomes the leaf.

### Tool Result Override

Tool result messages (`role == "tool"` with a `ToolCallID`) get special handling: their `ParentID` is set to the assistant entry ID that originally issued the tool call, looked up via an in-memory `toolCallMap`.

```
toolCallMap: { toolCallID → assistantEntryID }
```

When an assistant message with `ToolCalls` is persisted, each tool call ID is recorded in the map. When a matching tool result arrives, the map is consulted to set the correct `ParentID`.

### State Recovery

On first `AppendMessages` call for a session, `initSessionState()` scans the full JSONL to rebuild the leaf ID and tool call map. Unmatched tool calls (assistant issued but no tool result yet) are preserved.

## Inbox

Async event delivery via per-session sidecar files (`<inboxDir>/<sessionID>.inbox.jsonl`).

- `AppendInbox(ctx, sessionID, entry)` — append a `model.SessionEntry` to the inbox file. Unparseable lines in inbox files are also skipped with a warning.
- `FetchAndClearInbox(ctx, sessionID)` — read all entries and delete the file.

If `WithInbox("")` was not configured, both methods silently no-op.

## Pending Jobs

Tracks async job completion per session:

- `RegisterPendingJob(sessionID, jobID)` — add to the pending set. Returns error if job ID is already registered (duplicate rejection).
- `FinishPendingJob(sessionID, jobID)` — remove from pending set; returns error if job ID is unknown. If last job, closes the notification channel.
- `HasPendingJobs(sessionID)` — check if any pending.
- `WaitPendingJobs(ctx, sessionID)` — blocks on a per-session notification channel until all jobs complete or the context is canceled. Multiple concurrent waiters on the same session are all unblocked together.

## Caching

`LoadContext` results are cached per session ID.

- **TTL**: 45 seconds (configurable via `WithCacheTTL`).
- **Defensive copy**: Cache stores a copy; caller mutations do not corrupt cached data.
- **Invalidation**: Explicit on `Compact()` (deletes cache entry). Implicit via TTL expiry on next `LoadContext()`.
- **Incremental update**: `AppendMessages()` appends new non-system messages to the cached slice (matching `LoadContext`'s system message filter).

## Trimming

`TrimMessages(messages, maxTokens)` trims history to fit a token budget.

Strategy:

1. Always preserve the first system message (if present), regardless of budget.
2. Walk remaining messages from the tail, accumulating token costs.
3. Keep as many trailing messages as fit within the remaining budget.
4. Return `[system_msg] + tail_messages`.

Token estimation via `token.Estimate`.

## Concurrency Model

All public methods are safe for concurrent use. The service uses separate mutexes for each concern:

| Mutex | Protects |
|-------|----------|
| `writersMu` | Per-session `bufio.Writer` and `*os.File` maps |
| `cacheMu` | Cache entries (RWMutex) |
| `locksMu` | Serial lock channel map |
| `inboxMu` | Inbox file operations |
| `pendingMu` | Pending jobs map and notification channels |
| `leafMu` | Leaf IDs and tool call map |

Per-key serial locks (via `acquireLock`) ensure at-most-one concurrent holder per lock key. `Acquire` and `AcquireByID` use different lock namespaces (see Route Operations table above).

## Dependencies

- `RouteStore` interface: `Upsert`, `Find`, `FindBySessionID`, `Delete`. Implemented by `db.SessionRouteRepo`.
- `LLMCompressor` interface: `Compress(ctx, messages) (string, error)`.
- `Clock` interface: `Now() time.Time`.
- `IDGenerator` interface: `NewSessionID() string`.
- `model`: `SessionEntry`, `SessionRouteRow`, `Message`, role constants.
- `util`: `EstimateTokens()` for token budgeting.
