# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

*Last updated: 2026-03-26*

## Project Overview

Reed is a local-first, daemonless workflow runtime written in Go. 3-tier execution model: Process (OS process host, CLI target) → Run (workflow execution, memory-only) → StepRun (step execution). 22 internal packages + 12 pkg packages — engine, agent/AI, worker, MCP, session, tool, skill, media, security subsystems are all implemented.

## Build and Test

```bash
go build -v ./...
go test -v ./...
go test -v -run TestName ./...
```

## Mandatory Conventions

Full details: `docs/conventions.md`

### Naming (non-negotiable)

- **Go:** ALL-CAPS acronyms — `RunID`, `StepRunID`, `ProcessID`, `HTTP`, `JSON`. Never `RunId`.
- **JSON:** lowerCamelCase with acronym caps — `runID`, `stepRunID`. Never `runId`.
- **DB columns:** snake_case — `run_id`, `step_run_id`.
- **English only** in code comments, log messages, error messages, test names.

### Key Rules

- Business logic in `internal/`, general-purpose utilities in `pkg/`. `internal/model` is the leaf — never imports other internal packages. `pkg/` packages never import `internal/`.
- `zerolog` only. Never stdlib `log`.
- Accept interfaces, return structs. No DI frameworks. No global singletons.
- `internal/errors` for domain errors. stdlib `errors` for `Is`/`As`.
- Tests: same-package, table-driven, mock clock/ID/fs. `t.TempDir()` for side effects.

## Architecture

Package layout and details: `docs/architecture.md`

```
cmd/reed/        CLI (Cobra)
internal/
  engine/        "Brain" — Process registry, owner loop, DAG dispatch, scheduler
  worker/        "Muscle" — agent/shell/http workers, factory + router
  workflow/      "Blueprint" — stateless loader/parser/validator, DAG
  agent/         AI conversation loop, tool dispatch, prompt assembly
  ai/            LLM abstraction — router, failover, handlers (anthropic/openai/openai_responses), middleware
  tool/          Tool registry, path policy, builtin tools (bash/fs/search/gitignore/subagent), MCP adapter
  skill/         Skill resolution, scanning, mounting, validation
  session/       Conversation session management, routing, trimming
  mcp/           MCP client pool + server, transport, trigger handler
  bus/           In-process event pub/sub
  model/         Domain structs (leaf dependency)
  db/            SQLite (processes, session_routes, media)
  http/          Transport: HTTP server (Gin), IPC (Unix domain socket), triggers
  render/        Expression engine (${{ ... }})
  reed/          Manager — lifecycle facade, selective bootstrapping
  conf/          Config loading
  errors/        Domain error codes
  builtin/       Embedded workflow loader
  media/         Media file storage and GC
  memory/        Agent memory (file, noop, LLM extractor)
  security/      Secret store, permission guard/grant system
  shellutil/     Shell helpers (bash resolution, platform-specific)
pkg/
  configpatch/   Generic merge-patch ops on map[string]any configs
  confm/         Config manager (JSON + Viper + ENV overlays)
  cron/          Cron schedule wrapper (robfig/cron)
  fsutil/        Filesystem utilities (stale file cleanup)
  jsonutil/      Deterministic JSON marshal, SHA-256 hash, deep clone
  logutil/       Log rotation (lumberjack) + tail follower
  mimetype/      MIME type ↔ extension mapping, content sniffing
  procutil/      Process lifecycle (alive/terminate/kill/signal)
  reexec/        Process re-execution and detachment
  token/         LLM token estimation
  truncate/      Text truncation by line/byte
  version/       Build version info
```

### Dependency Direction

```
cmd/reed → reed.Manager → bus, conf, db, engine, http, mcp, media, model, security, worker
engine → bus, model, render, worker (interface)
worker → agent, model
agent → ai, bus, mcp, model, session, skill, tool
model → (leaf, imports nothing internal)
pkg/ → (leaf, no internal imports)
```

### Bootstrap

`reed.Manager` with functional options for selective bootstrapping:
- `reed validate`: workflow only
- `reed ps`: db only
- `reed run`: full stack
- Teardown: scheduler → engine (3s grace) → DB status → MCP → IPC → HTTP → bus → media GC → DB → event log

## Key Contracts

- **CLI targets Process** (PID or ProcessID). `run_id`/`step_run_id` are trace-only.
- **SQLite = system registry only** (processes, session_routes, media). Never execution history.
- **Liveness:** `reed status` tries IPC first, falls back to DB row. `reed ps` uses DB + OS PID check for cleanup.
- **Stop:** `SIGTERM → Context Cancel → 3s grace → exit`. Run→CANCELED, running step→CANCELED, pending step→SKIPPED.
- **Expressions:** `${{ ... }}` only. Engine evaluates — workers never do.
- **MCP dual role:** `mcp_servers` (top-level) = Reed as Client; `on.service.mcp` = Reed as Server.
- **Session TTL:** resets after local 4:00 AM boundary.

## Documentation

All docs in `docs/`. See `docs/index.md` for full navigation. Organized by module:

- `docs/architecture.md` — 3-tier model, dependency graph, bootstrap
- `docs/conventions.md` — All coding standards in one file
- `docs/workflow/` — DSL, config resolution, expressions
- `docs/engine/` — State machine, storage (JSONL + filesystem)
- `docs/worker/` — Shell, HTTP, agent workers
- `docs/agent/` — AI conversation loop, LLM providers
- `docs/tool/` — Registry, path policy, builtins
- `docs/mcp/` — Client pool, server, transport
- `docs/session/` — Routing, TTL, compaction
- `docs/cli/` — Commands, IPC protocol
- `docs/model/` — Complete type reference
