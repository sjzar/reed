---
title: "Reed Documentation"
summary: "Navigation index for all Reed project documentation"
read_when:
  - Starting work on the Reed project
  - Looking for documentation on a specific module
---

# Reed Documentation

Reed is a local-first, daemonless workflow runtime written in Go. 3-tier execution model: Process (OS process host) -> Run (workflow execution) -> StepRun (step execution). 22 internal packages covering engine, agent/AI, worker, MCP, session, tool, skill, media, and security subsystems.

## Project

- [Architecture](architecture.md) — 3-tier model, package layout, dependency graph, bootstrap, teardown
- [Conventions](conventions.md) — Naming, package layout, service design, error handling, testing

## Modules

| Module | Docs | Package |
|--------|------|---------|
| **Workflow** | [DSL](workflow/dsl.md) · [Config Resolution](workflow/config-resolution.md) · [Expressions](workflow/expressions.md) | `internal/workflow/`, `internal/render/` |
| **Engine** | [State Machine](engine/state-machine.md) · [Storage](engine/storage.md) | `internal/engine/` |
| **Worker** | [Overview](worker/overview.md) · [Shell](worker/shell.md) · [HTTP](worker/http.md) · [Agent](worker/agent.md) | `internal/worker/` |
| **Agent** | [Overview](agent/overview.md) · [AI Providers](agent/ai-providers.md) | `internal/agent/`, `internal/ai/` |
| **Tool** | [Overview](tool/overview.md) | `internal/tool/` |
| **MCP** | [Overview](mcp/overview.md) | `internal/mcp/` |
| **Session** | [Overview](session/overview.md) | `internal/session/` |
| **CLI** | [Commands](cli/commands.md) · [IPC](cli/ipc.md) | `cmd/reed/`, `internal/http/` |
| **Model** | [Types](model/types.md) | `internal/model/` |

## Packages Without Dedicated Docs

These packages are documented inline and described in [Architecture](architecture.md):

| Package | Purpose |
|---------|---------|
| `internal/bus/` | In-process event pub/sub |
| `internal/conf/` | Config loading |
| `internal/db/` | SQLite (processes, session_routes, media) |
| `internal/errors/` | Domain error codes |
| `internal/http/` | HTTP server (Gin), IPC (Unix domain socket), triggers |
| `internal/logutil/` | Log rotation (lumberjack wrapper) |
| `internal/media/` | Media file storage and GC |
| `internal/memory/` | Agent memory (file, noop, LLM extractor) |
| `internal/reed/` | Manager — lifecycle facade |
| `internal/security/` | Secret store, permission guard/grant |
| `internal/shellutil/` | Shell helpers (bash resolution, platform-specific) |
| `internal/skill/` | Skill resolution, scanning, mounting, validation |

## Analysis

Historical analysis documents in `docs/analysis/`.
