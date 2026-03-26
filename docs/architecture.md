---
title: Architecture
summary: 3-tier execution model, dependency graph, bootstrap sequence, teardown order
read_when: understanding how Reed's runtime is structured or how subsystems are wired together
---

# Architecture

## 3-Tier Execution Model

Reed uses three nested execution tiers: Process, Run, and StepRun.

**Process** is the OS process host and the CLI default target. It owns a single PID, a process-scoped Unix domain socket, and an in-memory registry of Runs. ID format: `proc_<hex_8>_<hex_4>` (generated in `engine.DefaultNewProcessID`). Modes: `cli`, `service`, `schedule` (defined as `ProcessMode` constants in `internal/model/process.go`). Status lifecycle: STARTING -> RUNNING -> STOPPED | FAILED.

**Run** is a single workflow execution instance within a Process. All Runs are memory-only; history is persisted to process-level JSONL via an `EventSink`. ID format: `run_<hex_8>`. Status lifecycle: CREATED -> STARTING -> RUNNING -> STOPPING -> SUCCEEDED | FAILED | CANCELED.

**StepRun** is a step-level execution instance within a Run. ID format: `step_run_<hex_8>`. Status lifecycle: PENDING -> RUNNING -> SUCCEEDED | FAILED | CANCELED | SKIPPED.

## Package Layout

22 internal packages:

```
internal/
  agent/         AI conversation loop, tool dispatch, prompt assembly
  ai/            LLM abstraction — router, failover, handlers (anthropic/openai/openai_responses), middleware
  bus/            In-process event pub/sub
  conf/           Config loading
  db/             SQLite (processes, session_routes, media)
  engine/         "Brain" — Process registry, owner loop, DAG dispatch, scheduler, JSONL writer
  errors/         Domain error codes
  http/           Transport: HTTP server (Gin), IPC (Unix domain socket), triggers, concurrency
  logutil/        Log rotation (lumberjack wrapper)
  mcp/            MCP client pool, server, transport, trigger handler
  media/          Media file storage and GC
  memory/         Agent memory (file, noop, LLM extractor)
  model/          Domain structs (leaf dependency)
  reed/           Manager — lifecycle facade, selective bootstrapping
  render/         Expression engine (${{ ... }})
  security/       Secret store, permission guard/grant system
  session/        Conversation session management, routing, trimming
  shellutil/      Shell helpers (bash resolution, platform-specific)
  skill/          Skill resolution, scanning, mounting, validation
  tool/           Tool registry, path policy, builtin tools (bash/fs/search/gitignore/subagent), MCP adapter
  worker/         "Muscle" — agent/shell/http workers, factory + router
  workflow/       "Blueprint" — stateless loader/parser/validator, DAG
```

## Dependency Graph

```
cmd/reed -> internal/reed (Manager)
internal/reed -> bus, conf, db, engine, http, mcp, media, model, security, worker
engine -> bus, model, render, worker (interface)
worker -> agent, model
agent -> ai, bus, mcp, model, session, skill, tool
ai -> model, ai/base, ai/handler/*, ai/middleware/*
session -> model
skill -> model
tool -> model, mcp
workflow -> model (stateless loader/merger/validator)
http -> model (pure transport, triggers, IPC)
model -> (leaf, imports nothing internal)
```

The engine has no DB dependency. The Manager (`internal/reed`) handles all DB registration and status persistence. IPC is part of `internal/http`, not a separate package.

## Bootstrap Sequence

`Manager` in `internal/reed/manager.go` uses functional options for selective bootstrapping:

| Command | Options applied |
|---|---|
| `reed validate` | None (workflow.Service only) |
| `reed ps` | `WithDB` |
| `reed status/stop` | `WithDB` + IPC client |
| `reed run` | `WithDB` + `WithMedia` + `WithWorkflow` + `WithIPC` + `WithResolver` + `WithSecrets` + optionally `WithHTTP` |

Available options:

| Option | Description | Prerequisite |
|---|---|---|
| `WithDB()` | Opens SQLite database | — |
| `WithHTTP(port)` | Bootstraps HTTP server on TCP port | — |
| `WithEngine()` | Sets up basic worker router (shell, http) | `WithDB` |
| `WithMedia()` | Enables media service (file storage + SQLite metadata) | `WithDB` |
| `WithWorkflow(wf, workDir)` | Full workflow setup: MCP pool, agent engine, session, skill, tool | `WithDB` |
| `WithIPC()` | Marks IPC ready | — |
| `WithResolver(r)` | Sets RunResolver for HTTP/schedule triggers | — |
| `WithSecrets(store)` | Pre-created SecretStore | — |

Option ordering matters: `WithDB` must precede `WithEngine`/`WithWorkflow`/`WithMedia` (enforced by runtime checks in the option functions).

The runtime startup sequence in `OpenRuntime` (`internal/reed/run.go`):

1. Generate ProcessID via `engine.DefaultNewProcessID()`
2. DB INSERT (status=STARTING) then UPDATE (status=RUNNING)
3. Create event bus (`bus.New()`) and JSONL event log
4. Create Engine via `engine.New()` (receives worker, config, bus, event sink)

`InitRuntime` then completes setup:

1. Start IPC (Unix domain socket via `http.StartIPC`)
2. Submit initial Run and Seal (CLI mode only)
3. Setup HTTP/MCP triggers
4. Setup schedule triggers

## Teardown Order

`Manager.Shutdown()` in `internal/reed/lifecycle.go` executes once via `sync.Once`:

1. Stop scheduler (if configured)
2. `engine.Close()` — cancels root context, waits for all active runs (3s grace timeout)
3. Persist final process status to DB (fresh 5s context, independent of engine timeout)
4. Stop MCP pool (`mcpPool.StopAll()`)
5. Shutdown IPC via `http.ShutdownIPC()` (5s timeout)
6. Shutdown HTTP TCP listener via `http.Shutdown()` (5s timeout, only if started)
7. Close event bus
8. Media GC (cleanup expired entries)
9. Close DB
10. Close event log

Signal handling: SIGTERM/SIGINT triggers `engine.StopAll()` followed by `Shutdown()`. `Manager.Run()` returns a non-nil error (for non-zero exit code) if any Run ended in FAILED or CANCELED (`HasFailedRuns`). Note: this is separate from DB process status — `deriveFinalProcessStatus` only maps RunFailed to ProcessFailed; RunCanceled maps to ProcessStopped.

## Engine Internals

The `Engine` struct (`internal/engine/engine.go`) is the unified stateful runtime for a single Process. Key mechanisms:

- **Seal**: marks the engine so `DoneCh` closes when all active runs reach terminal state. CLI mode seals immediately after the initial Submit.
- **Submit**: creates a `runState`, starts its owner loop in a goroutine, returns a `RunHandle`.
- **Close**: cancels root context, waits for all run doneChs with context timeout, then unsubscribes lifecycle events and drains the persist goroutine.
- **Snapshot**: returns a read-only `ProcessView` of process info, active runs, terminal runs, and listeners.
- **StopAll**: cancels all active runs.
- **HasFailedRuns**: checks terminal runs for exit code semantics.

Lifecycle events flow through a `bus.Bus` subscription to an `EventSink` (JSONL writer) in a dedicated goroutine per Engine.

## Worker Architecture

Workers are registered in a `Router` (implements `engine.Worker` interface). Default workers in `worker.NewRouter()`:

| Uses field | Worker | Description |
|---|---|---|
| `shell`, `bash`, `run` | ShellWorker | Executes shell commands |
| `http` | HTTPWorker | Makes HTTP requests |

When using `worker.Build()` with a workflow (via `WithWorkflow`), the `BuildConfig` assembles the full agent stack:

- `ai.Service` (LLM router with failover)
- `session.Service` (conversation routing and trimming)
- `skill.Service` (skill scanning and mounting)
- `tool.Registry` (builtin tools + MCP adapter)
- `memory.Provider` (agent memory)
- `mcp.Pool` (MCP server connections)

In workflow mode, `Factory.NewRouter()` always registers `AgentWorker` on the `"agent"` key. It is used when a step has `uses: agent`.

## SQLite Schema

SQLite stores system registry data only (never execution history):

| Table | Purpose |
|---|---|
| `processes` | Process registration, status, PID, mode |
| `session_routes` | Session routing (agent → session mapping) |
| `media` | Media file metadata and TTL |
