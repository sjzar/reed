# AGENTS.md

Guidance for OpenAI Codex and other AI coding agents working in this repository.

## Project Overview

Reed (`github.com/sjzar/reed`) is a local-first, daemonless workflow runtime written in Go 1.25. It uses a 3-tier execution model (Process / Run / StepRun) where Process is the OS-level host, Run is a single workflow execution, and StepRun is a step-level execution instance. The codebase has 22 internal + 12 pkg packages with ~320 Go source files and extensive test coverage.

## Build and Test

```bash
go build -v ./...                              # compile all packages
go test -v ./...                               # run full test suite
go test -v -run TestName ./...        # run a single test
go run main.go                                 # start the CLI
go run main.go run <workflow>                  # execute a workflow
go run main.go validate <workflow>             # validate workflow syntax
```

## CLI Commands

Defined in `cmd/reed/`. Cobra-based CLI with these subcommands:

| Command    | File              | Description                          |
|------------|-------------------|--------------------------------------|
| `run`      | `cmd_run.go`      | Execute a workflow (`-d` to detach)  |
| `do`       | `cmd_do.go`       | Execute ad-hoc agent task            |
| `plan`     | `cmd_plan.go`     | Generate workflow from natural language |
| `ps`       | `cmd_ps.go`       | List active processes                |
| `status`   | `cmd_status.go`   | Query process status via UDS/SQLite  |
| `logs`     | `cmd_logs.go`     | Read process-level JSONL logs        |
| `stop`     | `cmd_stop.go`     | Send SIGTERM to a process            |
| `validate` | `cmd_validate.go` | Parse and validate workflow syntax   |
| `skill`    | `cmd_skill.go`    | Manage skills (install/uninstall/list/tidy) |
| `version`  | `cmd_version.go`  | Print build metadata                 |

## Package Layout

Business logic lives under `internal/`, general-purpose utilities under `pkg/`. Root also contains `docs/`, `scripts/`, `testdata/`.

### Core Triad

| Package    | Purpose |
|------------|---------|
| `workflow`  | Stateless workflow loader, parser, merger (RFC 7386), validator, DAG builder, `--set`/`--set-file` config resolution |
| `engine`    | Stateful run orchestrator: DAG dispatch, run loop, state management, step rendering, output collection |
| `worker`    | Step executor implementations: shell commands, HTTP calls, agent (LLM) steps |

### AI and Agent

| Package    | Purpose |
|------------|---------|
| `agent`    | LLM agent engine loop: tool resolution, message handling, sub-agent runner |
| `ai`       | AI provider abstraction: router, failover, context compression, handlers (anthropic/openai/openai_responses), base types, middleware |

### Infrastructure

| Package    | Purpose |
|------------|---------|
| `conf`     | Configuration loading via Viper + `REED_*` env vars; `conf.Load()` returns `*Config` |
| `db`       | SQLite (modernc.org/sqlite): migrations, processes, session_routes, and media repositories |
| `http`     | Gin HTTP server, router, CORS middleware, concurrency control, trigger-based routing |
| `bus`      | In-process pub/sub event bus for decoupled component communication |
| `mcp`      | MCP (Model Context Protocol) client pool, server, tool serving, transport layer |
| `render`   | Expression rendering engine (`${{ ... }}` syntax) with stdlib functions |
| `builtin`  | Embedded workflow loader |
| `errors`   | Domain error types with error codes, HTTP status mapping, middleware |
| `shellutil`| Shell command helpers (Unix/Windows), bash detection |

### Domain and Services

| Package    | Purpose |
|------------|---------|
| `model`    | Domain structs, status enums, API/DB view models (leaf dependency, imports nothing internal) |
| `session`  | Session routing and management with TTL-based reset |
| `skill`    | Skill loading: frontmatter parsing, digest, file resolution, mounting, validation |
| `tool`     | Tool registry: locking, path policies, MCP adapter, truncation, sub-agent dispatch |
| `memory`   | Agent memory (file-backed, noop, LLM extractor) |
| `media`    | Media file storage and GC |
| `security` | Secret store, permission guard/grant system |
| `reed`     | Manager lifecycle facade: setup, teardown, process GC, status provider, CLI command orchestration |

### General-Purpose Utilities (`pkg/`)

| Package       | Purpose |
|---------------|---------|
| `configpatch` | Generic merge-patch ops on `map[string]any` configs |
| `confm`       | Config manager (JSON + Viper + ENV overlays) |
| `cron`        | Cron schedule wrapper (robfig/cron) |
| `fsutil`      | Filesystem utilities (stale file cleanup) |
| `jsonutil`    | Deterministic JSON marshal, SHA-256 hash, deep clone |
| `logutil`     | Log rotation (lumberjack) + tail follower |
| `mimetype`    | MIME type ↔ extension mapping, content sniffing |
| `procutil`    | Process lifecycle (alive/terminate/kill/signal) |
| `reexec`      | Process re-execution and detachment |
| `token`       | LLM token estimation |
| `truncate`    | Text truncation by line/byte |
| `version`     | Build version info |

## Architecture

### 3-Tier Execution Model

- **Process**: OS process host. CLI default target. One PID, one process-scoped Unix socket. ID: `proc_<prefix>_<hash>`.
- **Run**: Workflow execution within a Process. Memory-only (no SQLite persistence). ID: `run_<hash>`.
- **StepRun**: Step execution within a Run. Trace-only, read-only diagnostic. ID: `step_run_<hash>`.

### Key Patterns

- **Manager as lifecycle facade**: `internal/reed.Manager` handles selective bootstrapping via functional options. `reed validate` boots only `workflow.Service`; `reed ps` boots only `db`; `reed run` boots the full stack.
- **Accept interfaces, return structs**: Constructors take locally-defined small interfaces (e.g., `engine` defines its own `ProcessStore`, never imports `db` concrete types).
- **No global singletons**: Config via `conf.Load()` + explicit constructor injection. No DI frameworks.
- **Event bus**: `internal/bus` provides decoupled pub/sub between engine, session, and transport layers.
- **MCP dual role**: `mcp_servers` (top-level workflow key) = Reed as MCP client; `on.service.mcp` = Reed as MCP server.

### Dependency Direction (strict, no cycles)

```
cmd/reed -> internal/reed (Manager), pkg/*
internal/reed -> engine, db, http, workflow, conf...
engine -> workflow (render), worker (dispatch), db (via engine-defined interfaces)
worker, workflow, db, http -> model, errors
model -> (leaf, no internal imports)
pkg/ -> (leaf, no internal imports)
```

## Naming Conventions

These are mandatory project rules:

- **Go identifiers**: Acronyms are ALL-CAPS: `RunID`, `StepRunID`, `ProcessID`, `HTTP`, `JSON`, `URL`. Never `RunId`.
- **JSON fields**: lowerCamelCase preserving acronym caps: `runID`, `processID`, `currentSessionID`. Never `runId`.
- **Database columns**: snake_case: `run_id`, `process_id`, `current_session_id`.

## Testing Conventions

- Tests live alongside code (`*_test.go` in the same package). Use external `_test` package only for public API boundary tests.
- Table-driven tests for parsing, validation, merging, state mapping.
- Mock: clock, ID generators, filesystem, OS signals. Use in-memory DB or `t.TempDir()` for side effects.
- Assert error codes and semantics, not fragile string equality.
- No real network listeners unless testing HTTP/socket packages.

## Code Style

- **Logging**: `zerolog` exclusively. Never stdlib `log`.
- **Errors**: `internal/errors` for domain errors (codes, wrapping). Stdlib `errors` for `Is`/`As`.
- **Comments**: English only. No bilingual comments.
- **Log output**: All logging goes through `cmd/reed/log.go` -> `initLogger()` using `logutil.RotatingWriter`.

## Key Dependencies

| Dependency | Role |
|------------|------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/spf13/viper` | Configuration |
| `github.com/gin-gonic/gin` | HTTP server |
| `github.com/rs/zerolog` | Structured logging |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `github.com/expr-lang/expr` | Expression evaluation |
| `github.com/robfig/cron/v3` | Cron scheduling |
| `github.com/anthropics/anthropic-sdk-go` | Anthropic API client |
| `github.com/openai/openai-go/v3` | OpenAI API client |
| `github.com/modelcontextprotocol/go-sdk` | MCP protocol |
| `gopkg.in/natefinch/lumberjack.v2` | Log rotation |
| `gopkg.in/yaml.v3` | YAML parsing |
