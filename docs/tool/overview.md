---
title: Tool System
summary: Tool registry, execution service, concurrency policies, path access, built-in tools
read_when: implementing or modifying tools, understanding tool execution flow
---

# Tool System

Package: `internal/tool`

## Tool Interface

Every tool implements three methods:

- `Def() model.ToolDef` -- returns name, description, and JSON Schema for the LLM.
- `Prepare(ctx, CallRequest) (*PreparedCall, error)` -- parses raw JSON args into a typed struct and declares an `ExecutionPlan`.
- `Execute(ctx, *PreparedCall) (*Result, error)` -- runs the tool logic.

Helper constructors: `TextResult(string)` and `ErrorResult(string)` create single-block results. `MarshalArgs(model.ToolCall)` converts a ToolCall to raw JSON bytes (priority: `RawJSON` > `Arguments` > empty object).

## Service (ExecBatch)

`Service` is the central execution entry point. `ExecBatch(ctx, BatchRequest) BatchResponse` processes a slice of `CallRequest` items:

1. Normalizes `ToolCallID` if empty; injects batch-level defaults (Env, Context).
2. Looks up each tool in the `Registry`.
3. Calls `Prepare` to parse args and obtain an `ExecutionPlan`.
4. Dispatches sync calls concurrently (goroutine per call with `sync.WaitGroup`), or routes async calls via `dispatchAsync`.
5. Applies concurrency policy before calling `Execute`.
6. Runs `sanitizeResult` on every result (see Output Truncation).

Constructor: `NewService(reg *Registry, opts ...ServiceOption)`. Options: `WithSession`, `WithEmitter`, `WithEmitterFunc`, `WithIDGen`.

### Event Emission

`Service` emits `model.Event` via an `EventEmitter` (or a bare `func(model.Event)` via `WithEmitterFunc`). Events are emitted at tool-call start and end.

## Execution Modes

- **ExecModeSync** (default) -- blocks until complete.
- **ExecModeAsync** -- detaches from the request. Returns a `jobID` ack immediately. The result is written to the session inbox when done. Requires a `SessionAsyncBridge` (methods: `RegisterPendingJob`, `FinishPendingJob`, `AppendInbox`).

Async job lifecycle: `JobQueued` -> `JobRunning` -> `JobCompleted`/`JobFailed`. `JobCancelling` is a transient state set by `KillJob`; deferred cleanup always overwrites it with `JobCompleted` or `JobFailed`. Jobs are tracked in `Service.jobs` (`sync.Map`) and deleted after completion.

Job management methods:

- `KillJob(jobID)` -- cancels the job context, waits up to 3 s for graceful shutdown.
- `ListJobs(sessionID)` -- returns active jobs, optionally filtered by session.
- `HasJobs(sessionID)` -- true if non-terminal jobs exist for the session.
- `WaitJobs(ctx, sessionID)` -- blocks until all non-terminal jobs for the session complete.

## Concurrency Policies

Set via `ExecutionPlan.Policy`:

| Policy | Behavior |
|---|---|
| `ParallelSafe` | No locking. Used by `bash`, `ls`, `search`, MCP tools. |
| `GlobalSerial` | At most one tool executes at a time (capacity-1 channel). |
| `Scoped` | Keyed read/write locks via `LockScheduler`. Used by `read` (LockRead), `write`/`edit` (LockWrite). |

`LockScheduler` prevents deadlocks by sorting lock keys, merging duplicates, and promoting read+write on the same key to write. Lock keys use the `"fs:<path>"` format (via `LockKey`).

## Default Timeout

`DefaultTimeout = 30s`. Overridden per-tool via `ExecutionPlan.Timeout`. The bash tool defaults to `120s`.

## Registry

`Registry` is thread-safe (`sync.RWMutex`). Tools are registered with `Register(tools ...Tool)` or `RegisterWithGroup(t, group)`.

Tool groups control LLM exposure:

- `GroupCore` -- included by default.
- `GroupOptional` -- must be explicitly requested. This is the default for tools that don't implement the `GroupedTool` interface.
- `GroupMCP` -- from MCP servers.

Listing methods:

- `CoreToolIDs()` -- sorted names of all `GroupCore` tools.
- `ListTools(ids)` -- strict: returns error if any ID is unknown.
- `ListToolsLenient(ids)` -- lenient: silently skips unknowns (used for skills).
- `All()` -- all tools sorted alphabetically.

## Path Resolution and Access Control

`RuntimeContext` carries execution context: `Set` (bool), `Cwd`, `RunRoot`, `OS`.

Path resolution: `ResolvePath(cwd, rawPath)` resolves relative paths against cwd and canonicalizes via `security.Canonicalize` (symlink-aware). Returns an error for empty paths or relative paths with empty cwd. Helper functions `resolveFSPath`, `checkReadAccess`, `checkWriteAccess`, and `formatLinesWithNumbers` are in `path_access.go`.

Access control is enforced by a `security.Guard` stored in `context.Context`. Each file-system tool calls `checkReadAccess` or `checkWriteAccess`, which retrieves the guard via `security.FromContext(ctx)`. If no security context is present, access is denied (fail-closed). The tool package itself does not define access profiles -- that responsibility belongs to the `internal/security` package.

`LockKey(resolved)` returns `"fs:<path>"` for scoped locking.

## Output Truncation

Two levels of truncation:

**Per-tool truncation** -- tools apply limits internally using `pkg/truncate`:

- `truncate.Head(text, maxLines, maxBytes)` -- keeps first N lines (used by `read`).
- `truncate.Tail(text, maxLines, maxBytes)` -- keeps last N lines (used by `bash`).
- Both snap to complete lines and preserve UTF-8 boundaries.
- Defaults: `DefaultMaxLines=2000`, `DefaultMaxBytes=50KB`, `DefaultLSLimit=1000` (in `tool_ls.go`). The search tool uses its own internal hard cap `searchLimit=1000` (unexported).

**Service-level sanitization** -- `sanitizeResult` (in `sanitize.go`) runs on every result after execution:

1. Returns `ErrorResult` if result is nil.
2. Detects binary content (null byte in first 512 bytes).
3. Applies `MaxOutputBytes=100KB` and `MaxOutputLines=2000` via `truncate.Tail`.
4. Annotates the result with context when truncated.

## MCP Adapter

`WrapMCPTools(ctx, pool)` converts all tools from a `ToolPool` into `Tool` instances. The `ToolPool` interface requires `ListAllTools` and `CallTool` methods.

Each `mcpTool` uses `ParallelSafe` policy with `ExecModeSync`. Tool names may use `__` separator (`"{serverID}__{toolName}"`); `ParseToolName(name)` splits on the first `__` if present, otherwise returns empty serverID. `WrapMCPTools` preserves whatever `def.Name` the pool returns and parses the serverID/toolName from it.

The adapter converts MCP result content: text and image blocks are passed through; all other content types are replaced with a placeholder (`[non-text content: {type}]`).

## Built-in Tools

Registered via `RegisterBuiltins(reg *Registry, opts ...BuiltinOption)`. Functional options: `WithSubAgentRunner(r)`.

### Core Tools (GroupCore)

| Tool | File | Key Behaviors |
|---|---|---|
| `read` | `tool_read.go` | Read file contents with optional offset/limit/raw. Detects binary files (images → media content blocks, PDFs → media blocks, other binary → size/type summary; oversized media → text-only fallback). Offset is 0-based input, output shows 1-based line numbers. Applies `truncate.Head`. Policy: `Scoped` + `LockRead`. |
| `write` | `tool_write.go` | Write file contents. Creates parent directories via `MkdirAll`. Preserves existing file permissions. Policy: `Scoped` + `LockWrite`. |
| `edit` | `tool_edit.go` | String replacement with `old_string`/`new_string`/`replace_all`. Checks both read and write access. Normalizes BOM and CRLF before matching (preserves original encoding on write). Rejects empty `old_string`. Requires unique match unless `replace_all=true`; returns error for no-op replacements. Returns context diff (3 lines around match). Policy: `Scoped` + `LockWrite`. |
| `ls` | `tool_ls.go` | List directory entries alphabetically. Defaults path to `RuntimeContext.Cwd`. Appends `/` to directory names. Limits output to 1000 entries. Policy: `ParallelSafe`. |
| `search` | `tool_search.go` | Unified file search and content grep. Three modes: (1) pattern + glob → content search with regex, (2) glob only → file listing, (3) `files_only=true` → matching file paths only. Uses ripgrep (`rg`) when available, falls back to pure Go. Hard cap: 1000 results. Skips ignored directories (`.git`, `node_modules`, `vendor`, etc.). Options: `literal`, `ignore_case`, `context` lines. Go fallback appends `[warning]` lines for scan failures. Policy: `ParallelSafe`. |
| `bash` | `tool_bash.go` | Execute shell commands via `bash -c`. Resolves workdir from `workdir` arg or `RuntimeContext.Cwd`, checks write access on the resolved directory. Supports `background` flag → `ExecModeAsync`. Default timeout `120s`. Optionally uses RTK (token-optimized CLI proxy) when available. Applies `TruncateTail`. Policy: `ParallelSafe`. |

### Optional Tools (GroupOptional)

| Tool | File | Key Behaviors |
|---|---|---|
| `spawn_subagent` | `tool_subagent.go` | Spawn a sub-agent with `agent_id` and `prompt`. Always `ExecModeAsync` + `ParallelSafe`. If no `SubAgentRunner` is configured, returns an error on execute. |

## .gitignore Support

When ripgrep is available, `.gitignore` handling is delegated to `rg` (which natively supports nested `.gitignore` files). For the pure Go fallback, a lightweight `.gitignore` matcher (`search_gitignore.go`) reads the root-level `.gitignore` only (no nested `.gitignore` files). Patterns support negation (`!`), directory-only (`/`), anchored patterns, and doublestar globbing. Last matching pattern wins. The Go fallback also unconditionally skips hardcoded directories (`.git`, `node_modules`, `__pycache__`, `vendor`, etc.).
