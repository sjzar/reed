---
title: CLI Commands
summary: All reed CLI commands, flags, PID/ProcessID resolution, event consumer, detach mode
read_when: using or modifying CLI commands, understanding CLI flow
---

# CLI Commands

Package: `cmd/reed`

Built with Cobra. Entry point: `Execute()` in `root.go`.

## Global Flags

| Flag | Description |
|---|---|
| `--debug` | Enable debug logging |
| `--home <dir>` | Reed home directory (sets `REED_HOME` env) |

## reed run

```
reed run <workflow-file> [command]
```

Executes a workflow. Determines process mode from the `on` block: `service` if `on.service` present, `schedule` if `on.schedule` alone, otherwise `cli`.

| Flag | Short | Description |
|---|---|---|
| `--detach` | `-d` | Run in background |
| `--set` | | Override values (key=value) |
| `--set-file` | | Patch files (RFC 7386 merge) |
| `--set-string` | | Override values as strings |
| `--env` | | Set env vars (K=V) |
| `--job` | `-j` | Run only specific jobs |
| `--input` | `-i` | Set input values (key=value) |

Flow: `loadConfig` -> `initLogger(LogModeShared)` -> detach check -> `workflow.PrepareWorkflow` -> `resolveInitialRunRequest` -> bootstrap Manager -> `OpenRuntime` -> switch to per-process logger -> start event consumer -> `InitRuntime` -> `m.Run()` -> print summary.

### Detach Mode

`-d` re-executes the process in background with `REED_INTERNAL_DETACHED=1` set to prevent fork bombs. The parent polls the DB for up to 5 seconds to resolve the child's ProcessID.

## reed ps

```
reed ps [--all]
```

Lists active processes. Bootstrap: DB only. Calls `m.CleanStale()` for lazy stale process cleanup.

| Flag | Description |
|---|---|
| `--all` | Show all processes including stopped |

Output columns: PROCESS ID, PID, MODE, STATUS, WORKFLOW, CREATED.

## reed status

```
reed status <PID|ProcessID> [--json] [-r runID]
```

Queries process status. Tries UDS (live) first, falls back to SQLite (offline). Bootstrap: DB only.

| Flag | Short | Description |
|---|---|---|
| `--json` | | Output raw JSON |
| `--run` | `-r` | Query a specific run by ID |

## reed stop

```
reed stop <PID|ProcessID> [-r runID]
```

Sends SIGTERM to a running process. With `--run`, stops a specific run via IPC instead.

| Flag | Short | Description |
|---|---|---|
| `--run` | `-r` | Stop a specific run by ID (via IPC) |

Reports whether the process was gracefully stopped or force-killed after 3-second grace period.

## reed logs

```
reed logs <PID|ProcessID> [-n N] [-f] [--process]
```

Reads process event logs (default) or process application logs.

| Flag | Short | Description |
|---|---|---|
| `--tail` | `-n` | Show last N lines (0 = all) |
| `--follow` | `-f` | Follow log output |
| `--process` | | Include process application logs |

## reed validate

```
reed validate <workflow-file>
```

Parses and validates workflow syntax. Bootstrap: workflow service only (no DB, no network). Prints "workflow is valid" on success.

## reed version

```
reed version [-m]
```

Prints version information. `-m` shows extended version details.

## Event Consumer

`runEventConsumer` subscribes to the event bus and renders lifecycle events to stdout in real-time:

- `EventStepStarted` -> prints step name and subscribes to step output topic for streaming text deltas.
- `EventStepFinished` -> unsubscribes and prints status.
- `EventStepFailed` -> unsubscribes and prints error.
- `EventRunFinalized` -> exits consumer.

Step output is streamed as text deltas via `drainStepOutput`. Remaining buffered messages are flushed on subscription close.

## Status Formatting

`format.go` renders `StatusResult` as human-readable text. Live IPC responses are parsed into `StatusView` and rendered with process info, listeners, and active runs with job details. Offline fallback shows `ProcessRow` fields with a note.
