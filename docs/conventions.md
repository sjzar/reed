---
title: Development Conventions
summary: Naming rules, package layout, service design, error handling, logging, testing
read_when: writing or reviewing Go code in the Reed codebase
---

# Development Conventions

## Naming Rules

### Go Identifiers

Exported identifiers use PascalCase. Acronyms are always all-caps: `RunID`, `StepRunID`, `ProcessID`, `PIDStartedAt`, `HTTP`, `JSON`, `URL`, `IPC`. Never `RunId` or `StepRunId`.

### JSON Fields

lowerCamelCase with acronym caps preserved: `runID`, `stepRunID`, `processID`, `currentSessionID`, `pidStartedAt`. The first word, if an acronym, is lowercased: `id` not `ID`.

### Database Columns

snake_case: `run_id`, `step_run_id`, `process_id`, `current_session_id`, `pid_started_at`.

### Language

All code comments, log messages, error messages, and test names must be in English only. No bilingual comments.

## Package Layout

Core application code lives under `internal/`. Root also contains supporting directories (`pkg/`, `scripts/`, `testdata/`, `docs/`) and metadata files (`go.mod`, `go.sum`, `CLAUDE.md`, etc.).

22 packages under `internal/`. See [Architecture](architecture.md) for the full package layout, dependency graph, and descriptions. Key groupings:

- **Core runtime**: `engine`, `worker`, `workflow`, `render`, `bus`
- **AI/Agent**: `agent`, `ai`, `tool`, `skill`, `session`, `memory`
- **Infrastructure**: `db`, `http` (includes IPC), `mcp`, `conf`, `security`, `media`
- **Utilities**: `errors`, `logutil`, `shellutil`
- **Domain types**: `model` (leaf — imports nothing internal)
- **Lifecycle facade**: `reed` (Manager)

## Service Design

Packages with dependencies typically expose a primary struct with `func New(...) *T`. The naming follows the domain: `Engine`, `Router`, `Manager`, `Service`, etc. Pure model packages and small utility packages are exempt.

**Accept interfaces, return structs.** Constructors take locally-defined small interfaces, not concrete external types. This is enforced in practice: `engine.Engine` takes an `engine.Worker` interface, not a concrete `worker.Router`.

**No god services.** `internal/reed.Manager` is a lifecycle facade — it wires subsystems together but contains no business logic.

**Layer boundaries:**
- Transport (`http`) delegates to engine + model. Never imports business packages.
- `workflow` imports model only. Never imports transport.
- `engine` depends on `worker` (via interface), `render`, `bus`, and `model`. Never imports `db` directly — Manager handles DB.
- `agent` depends on `ai`, `tool`, `skill`, `session`, `mcp`, `bus`.
- See [Architecture — Dependency Graph](architecture.md#dependency-graph) for the full import map.

## Error Handling

Use `internal/errors` for domain errors (error codes, wrapping). Use stdlib `errors` for `Is`/`As` checks. Do not re-export stdlib functionality unless injecting specific context like HTTP status codes.

All error messages must be in English.

## Logging

Use `zerolog` exclusively. Never use stdlib `log` in application code. Log initialization goes through `cmd/reed/log.go` -> `initLogger()` using `logutil.RotatingWriter`.

## Testing

Tests live alongside code (`*_test.go` in the same package). Use external `_test` package only for public API boundary verification.

**Determinism:** Unit tests must be fast (milliseconds). Mock: clock (`time.Now`), ID generators, filesystem, OS signals. The engine accepts `newRunID`, `newStepRunID` function fields for this purpose.

**Table-driven tests** for parsing, merging, validation, state mapping, rendering.

**Assert error codes and semantics**, not fragile string equality.

**Side effects:** Use in-memory DB or `t.TempDir()`. No real network listeners unless testing HTTP/socket packages.

**Coverage priority:** `workflow` > `errors` > `engine` > `reed` > `worker` > `agent`.
