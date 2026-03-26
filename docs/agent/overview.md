---
title: Agent Runner Overview
summary: Agent Runner architecture -- Run() flow, resolve phase, conversation loop, context pressure management, security model, event bus, and sub-agent delegation.
read_when: Working on agent execution, debugging LLM interactions, or understanding the agent loop.
---

## Runner Struct

`Runner` (`internal/agent/runner.go`) orchestrates agent execution. Dependencies:

| Field | Interface | Purpose |
|-------|-----------|---------|
| `ai` | `AIService` | LLM calls (`Responses`, `ModelMetadataFor`) |
| `session` | `SessionProvider` | Session acquire/load/append/compact/inbox |
| `tools` | `ToolExecutor` | Tool execution and listing |
| `skills` | `SkillProvider` | Skill mounting (`ListAndMount`) |
| `memory` | `memory.Provider` | Memory inject/persist (`BeforeRun`/`AfterRun`) |
| `profile` | `ProfileProvider` | Named profile resolution (optional) |
| `media` | `MediaService` | Media upload/deflation and URI validation (optional) |

Constructor: `New(ai, session, tools, skills, memory, ...opts)`.

Options: `WithMedia(MediaService)`, `WithProfile(ProfileProvider)`.

## Run() Method

`Run(ctx, *model.AgentRunRequest) (*model.AgentRunResponse, error)` executes: resolve → loop → persist.

1. Apply `TimeoutMs` if set (context deadline).
2. `resolve(ctx, req)` → `resolvedConfig` (all inputs fully resolved).
3. Store Security Guard in context via `security.WithChecker()`.
4. `loop(ctx, cfg, state)` → conversation loop.
5. `afterRun(ctx, cfg, state)` → persist memory (best-effort, 60s timeout, survives context cancel via `context.WithoutCancel`).

## Resolve Phase

`resolve()` (`runner_resolve.go`) produces a `resolvedConfig`:

1. **Session acquire**: `SessionKey` vs `SessionID` (mutually exclusive). Returns `sessionID` + `release` func.
2. **Profile cascade**: `req > profile` for model, system_prompt, max_iterations, temperature, thinking_level. Only `SystemPrompt` falls back to `DefaultSystemPrompt` in this phase; `Model` and `MaxIterations` may remain empty/zero here — their defaults are applied later (model by AI service routing, max iterations in the loop).
3. **Tool definitions**: nil=core defaults, empty=no tools, [ids]=exactly those. Profile provides fallback. Uses lenient resolution for skill-derived tool IDs.
4. **Skills**: `ListAndMount` into run temp dir. Failures are warnings (skills are advisory). Requires non-empty `RunRoot`.
5. **Memory**: `BeforeRun` fetches memories. Failures are warnings.
6. **Model metadata**: Queries `ModelMetadataFor` to obtain `ContextWindow` for proactive compaction and prompt budgeting. Skipped when model is empty.
7. **Prompt assembly**: `buildPrompt(opts)` creates layered `Prompt` struct. Note: sysinfo uses raw `req.Cwd`, which may differ from the canonicalized Cwd in RuntimeContext below.
8. **RuntimeContext**: Builds `tool.RuntimeContext` with Cwd (canonicalized), RunRoot, OS. Cwd fallback: `req.Cwd` → `req.RunRoot` → `os.Getwd()`.
9. **Security Guard**: Creates or forks access control boundary (see Security Model below).
10. **Media**: Resolves `req.Media` entries (`media://` URIs, `data:` URIs). Only processed when `e.media != nil`; silently skipped otherwise. Rejects raw local file paths — local→media conversion must happen at CLI/HTTP boundary.

### resolvedConfig

All fields the loop needs, fully resolved. The loop never reads from the raw `AgentRunRequest`:

| Group | Fields |
|-------|--------|
| Session | `SessionID`, `Release` |
| Memory | `MemoryRC` (RunContext for AfterRun) |
| Model params | `Model`, `SystemPrompt`, `ThinkingLevel`, `MaxTokens`, `MaxIterations`, `Temperature`, `TopP`, `Schema`, `ExtraParams` |
| Capabilities | `ToolDefs`, `ToolCtx` (RuntimeContext) |
| Prompt | `Prompt` (assembled), `PromptMode` |
| User input | `UserPrompt`, `UserContent` (images/documents) |
| Pass-through | `AgentID`, `Env`, `WaitForAsyncTasks` |
| Metadata | `ContextWindow` |
| Security | `Guard` (*security.Guard) |
| Bus | `Bus` (`*bus.Bus`), `StepRunID` |

## Conversation Loop

`loop()` (`runner_loop.go`):

1. Load session context (post-compaction view via `LoadContext`).
2. Build system prompt (rebuilt fresh each run, never persisted in reusable form). Appended best-effort for audit trail (write errors ignored); `LoadContext` filters out system messages on reload.
3. Build user message (text + image/document content blocks), append best-effort.
4. Resolve bus for event streaming; start bus subscriber for steer messages.
5. Iterate up to `maxIterations` (default applied in loop when `cfg.MaxIterations <= 0`):
   - Context check: return `AgentStopCanceled` if ctx cancelled.
   - Micro-compact old tool results (if >70% context used).
   - Proactive compaction (if >90% context used, with cooldown).
   - `callLLM()` → stream response, handle rate limits and context overflow.
   - Build assistant message, append best-effort, increment iteration.
   - Token budget check (`MaxTokens`): return `AgentStopMaxTokens` if exceeded.
   - If no tool calls: `drainInboxOrComplete()`.
   - If tool calls: `executeTools()`, then drain inbox for steer messages, update tool boundary.
6. If loop exhausted: return `AgentStopMaxIter`.

## callLLM

Retries internally (up to `maxLLMRetries = 8`) for rate limits and context overflow. On `ErrContextOverflow`, calls `compactAndRebuild()` (KeepRecentN: 4) and retries. On `ErrRateLimit`, waits `RetryAfter` duration. Publishes `llm_start`/`llm_end` status events to bus. When retries are exhausted, returns `AgentStopMaxTokens` (graceful stop, not an error).

An ephemeral progress hint is injected into the LLM request (not persisted to session): `[Progress: iteration N/M | context ~X/Y tokens]`. Only added after the first iteration.

## executeTools

Converts `resp.ToolCalls` to `tool.CallRequest` batch (with raw JSON args and Env). Calls `tools.ExecBatch()`. Deflates data URIs → `media://` references before appending. Converts results to tool messages and appends best-effort. Runs repetition detection — returns `AgentStopLoop` if detected.

## drainInboxOrComplete

1. Fetch and clear inbox. If events found, convert to messages (deflate data URIs) and continue loop.
2. If pending async jobs: wait (if `WaitForAsyncTasks`) or return `AgentStopAsync`.
3. Otherwise: return `AgentStopComplete`.

## Prompt Assembly

`buildPrompt()` (`prompt.go`) creates a `Prompt` with prioritized sections:

| Section | Priority | MinMode | Content |
|---------|----------|---------|---------|
| identity | Required (0) | None | System prompt text (default: autonomous agent prompt) |
| tools | Medium (2) | Minimal | Tool summary via `buildToolSummary()` |
| sysinfo | Medium (2) | Minimal | Agent ID, model, cwd, OS, arch, timezone, context window |
| memory | Medium (2) | Full | Memory entries from `memory.Provider.BeforeRun()` |
| skills | Low (3) | Full | Mounted skill descriptions (SKILL.md paths) |

### Prompt Modes

| Mode | Value | Includes |
|------|-------|----------|
| `PromptModeNone` | 0 | Identity only |
| `PromptModeMinimal` | 1 | Identity + tools + sysinfo |
| `PromptModeFull` | 2 | All sections (default) |

A section is included when `section.MinMode <= requestedMode`.

### Budget Trimming

Three methods available:

- `BuildForMode(mode)` — filter by mode only.
- `BuildWithBudget(maxChars)` — budget trim all sections (no mode filter).
- `BuildForModeWithBudget(mode, maxChars)` — mode filter + budget trim. Used after compaction when context window is known.

Trim order: Low → Medium → High. `PriorityRequired` sections are never removed. When `maxChars <= 0` (extreme pressure), only Required sections survive.

## Context Pressure Management

Three-tier strategy triggered by `ContextWindow` availability:

### 1. Micro-Compaction (70% threshold)

`compactOldToolResults()` (`tool_compact.go`) truncates tool results from **previous** iterations only (current iteration results untouched). Keeps first/last halves with a marker: `...[truncated: N chars total]...`. Error results (`IsError=true`) are never truncated. Max chars: `defaultToolCompactMaxChars = 1000`. Skips already-compacted results.

### 2. Proactive Compaction (90% threshold)

LLM-driven session compaction via `session.Compact(KeepRecentN: 4)`. Only "sticks" if it actually reduces message count; otherwise treated as ineffective. Rebuilds system prompt with budget-aware trimming (`BuildForModeWithBudget`). Cooldown: `compactCooldownIters = 3` iterations between proactive attempts. The reactive `ErrContextOverflow` handler bypasses cooldown.

### 3. Token Estimation

`estimateRequestTokens()` scans backwards for last assistant message with Usage data, uses its Input as base + estimates newly added messages. Fixed weights: image=1000 tokens, document=2000 tokens. Text estimation via `token.Estimate()`.

## Security Model

Guard-based access control (`internal/security`):

**Top-level agents**: Fresh `security.Guard` created with `AccessProfile` (default: `ProfileWorkdir`) and Cwd.

**SubAgents**: Fork parent Guard via `security.Forkable.Fork()`, then supplement:
- Cwd: granted only if parent already allows it (prevents boundary escape).
- RunRoot: granted only if parent allows write access (validated via canonical path).

**Grants applied to all agents:**
- `Cwd` (read+write for top-level; conditional for subagents)
- `RunRoot` (read+write, temp dir for artifacts/skills)
- Skill mount dirs (read-only)
- Skill backing dirs (read-only, if different from mount dir)

**Enforcement**: Guard stored in context via `security.WithChecker(ctx, guard)`. Tools query via `security.FromContext(ctx)`.

## Event Bus Integration

### Output Streaming

Published to `bus.StepOutputTopic(stepRunID)` during LLM calls:
- `"status"` messages: `llm_start` (iteration/model), `llm_end` (stop reason/tool count).
- `"text"` messages: streaming text deltas from `drainStreamWithEvents()`.

### Input Handling (Steer)

`startBusSubscriber()` (`runner_bus.go`) subscribes to `bus.StepInputTopic(stepRunID)` in a background goroutine. Dispatches `"steer"` messages: parses `SteerPayload`, writes a `RoleUser` message into the session inbox via `AppendInbox()`. The loop picks it up via inbox drain.

## Media Handling

**Deflation**: `deflateMessages()` replaces inline `data:` URIs in image/document content with `media://` references by uploading to the MediaService. Failures are gracefully degraded — original data URI preserved.

**Input resolution**: `resolveMediaInput()` supports `media://` URIs (validated via `svc.Get`) and `data:` URIs. Creates `ImageContent` or `DocumentContent` based on MIME type. Raw local file paths are rejected (must be converted at CLI/HTTP boundary).

## Repetition Detection

`repetitionDetector` (`json.go`) uses a sliding window (default size: 3):

1. Each tool execution round is recorded as `[]roundCall{Name, Input, Output}`.
2. Calls sorted alphabetically by name.
3. Input: canonical JSON (`json.Marshal` on parsed map). Output: SHA-256 hash prefix (first 16 chars).
4. Round digest: SHA-256 of concatenated name+input+output (first 32 hex chars).
5. Window stores last 3 round digests. Triggers `AgentStopLoop` if all 3 are identical.

## JSON Helpers

Separate from the repetition detector, `json.go` delegates to `pkg/jsonutil` for general-purpose JSON utilities (`DeterministicMarshal`, `HashPrefix`, `DeepClone`): deterministic marshalling and content hashing. Used for robust argument handling in tool dispatch, not by the detector itself.

## Inbox Message Conversion

`inboxEventsToMessages()` (`inbox.go`) handles two entry types:

- **SessionEntryMessage**: Direct message injection (e.g., steer via bus).
- **SessionEntryCustom**: Async tool results (JSON payload with `toolCallID`, `toolName`, `isError`, `content[]`, `text` (legacy fallback)). Parse errors create error messages with `toolCallID` preserved for correlation. Empty results become `"(empty result)"`.

## Sub-Agent Delegation

`NewSubAgentRunner(runner, cfg)` (`subagent_runner.go`) creates a `tool.SubAgentRunner` that delegates to `Runner.Run()`.

Configuration via `SubAgentRunConfig`: Namespace, Model, Profile, Tools, Skills, SystemPrompt, MaxIterations, TimeoutMs.

Behavior:
- Uses `PromptModeMinimal` (identity + tools + sysinfo only).
- Forces `WaitForAsyncTasks: true`. Nested async is rejected (`AgentStopAsync` → error).
- Deep-copies `Env` map to prevent cross-contamination.
- Preserves nil vs empty slice semantics for `Tools` via `slices.Clone()` (`nil`=use defaults, `empty`=none). Note: `Skills` uses `len() == 0` check in `resolveToolDefs()`, so an empty slice still falls back to profile defaults — only `nil` vs non-nil matters for Tools, not Skills.
- Security: inherits parent Guard via Fork (see Security Model).

## runState

Mutable runtime state during loop iteration (`runner_state.go`):

| Field | Type | Purpose |
|-------|------|---------|
| `messages` | `[]model.Message` | Full conversation (rebuilt after compaction) |
| `totalUsage` | `model.Usage` | Accumulated token counts |
| `iteration` | `int` | Current loop counter (0-based) |
| `repDetector` | `*repetitionDetector` | Sliding window for repetition detection (window=3) |
| `lastToolBoundary` | `int` | Message index where current iteration's tool results start (for micro-compaction) |
| `lastCompactIter` | `int` | Iteration when last compaction ran (-1 = never, for cooldown) |

## Constants and Defaults

| Constant | Value | Purpose |
|----------|-------|---------|
| `DefaultMaxIterations` | 64 | Max loop iterations when not specified |
| `maxLLMRetries` | 8 | Max retries per `callLLM` invocation |
| `compactCooldownIters` | 3 | Min iterations between proactive compaction |
| `defaultToolCompactMaxChars` | 1000 | Max chars for micro-compacted tool results |
| `afterRunTimeout` | 60s | Timeout for `afterRun` memory persistence |
| `imageTokenWeight` | 1000 | Token estimate for image content blocks |
| `documentTokenWeight` | 2000 | Token estimate for document content blocks |

## Interfaces

### AIService

```go
Responses(ctx, *model.Request) (model.ResponseStream, error)
ModelMetadataFor(modelRef string) model.ModelMetadata
```

### SessionProvider

```go
Acquire(ctx, namespace, agentID, sessionKey) (sessionID, release, error)
AcquireByID(ctx, sessionID) (release, error)
LoadContext(ctx, sessionID) ([]Message, error)
Compact(ctx, sessionID, CompactOptions) ([]Message, error)
AppendMessages(ctx, sessionID, []Message) error
FetchAndClearInbox(ctx, sessionID) ([]SessionEntry, error)
HasPendingJobs(sessionID) bool
WaitPendingJobs(ctx, sessionID) error
AppendInbox(ctx, sessionID, SessionEntry) error
```

### ToolExecutor

```go
ExecBatch(ctx, BatchRequest) BatchResponse
ListTools(ids) ([]ToolDef, error)
ListToolsLenient(ids) []ToolDef
CoreToolIDs() []string
```

Adapter: `WrapToolService(*tool.Service) ToolExecutor` — flattens the Service/Registry two-layer API.
