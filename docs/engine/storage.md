---
title: Engine Storage Layer
summary: SQLite schema (processes, session_routes), JSONL event log format, and filesystem layout.
read_when: Working on DB layer, debugging process registration, or understanding event persistence.
---

## SQLite Database

Location: `<workdir>/reed.db` (default workdir: `~/.reed`). Driver: `modernc.org/sqlite` (pure Go). WAL mode, 5-second busy timeout, single-writer (`MaxOpenConns=1`).

`db.Open(dbDir)` creates the directory, opens the DB, and runs migrations. `db.OpenInMemory()` for tests.

### processes Table

```sql
CREATE TABLE IF NOT EXISTS processes (
    id TEXT PRIMARY KEY,
    pid INTEGER NOT NULL,
    pid_started_at TEXT NOT NULL,
    mode TEXT NOT NULL,
    status TEXT NOT NULL,
    workflow_source TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_processes_status ON processes(status);
CREATE INDEX idx_processes_pid ON processes(pid, pid_started_at);
```

All timestamps stored as RFC3339 strings.

**ProcessRepo** (`db/process_repo.go`): `Insert`, `UpdateStatus`, `FindByID`, `FindByPID`, `FindByPIDLatest`, `ListActive` (STARTING/RUNNING), `ListAll`, `Delete`.

### session_routes Table

```sql
CREATE TABLE IF NOT EXISTS session_routes (
    namespace TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    session_key TEXT NOT NULL,
    current_session_id TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (namespace, agent_id, session_key)
);
```

**SessionRouteRepo** (`db/session_route_repo.go`): `Upsert` (INSERT ON CONFLICT UPDATE), `Find` (by composite key), `FindBySessionID` (reverse lookup), `Delete`.

## JSONL Event Log

File: `<logDir>/<processID>.events.jsonl`. Written by `JSONLWriter` (`engine/jsonl.go`) via `logutil.RotatingWriter`.

Event struct fields (JSON keys):

| Field | JSON | Description |
|-------|------|-------------|
| Timestamp | `ts` | RFC3339Nano |
| ProcessID | `processID` | Always present |
| RunID | `runID` | Present for run/step events |
| StepRunID | `stepRunID` | Present for step events |
| JobID | `jobID` | Present for step events |
| StepID | `stepID` | Present for step events |
| Type | `type` | Event type constant |
| Status | `status` | Current status string |
| Message | `message` | Optional description |
| Error | `error` | Optional error detail |

Event type constants: `step_started`, `step_finished`, `step_failed`, `stop_requested`, `run_finalized`.

Events flow through the bus: engine emits to `bus.TopicLifecycle`, and `persistLifecycleEvents` goroutine converts `bus.LifecyclePayload` to `Event` and calls `sink.Append()`.

## Filesystem Layout

```
~/.reed/
  reed.db                           # SQLite (processes + session_routes)
  logs/
    <processID>.events.jsonl        # Per-process lifecycle events
  socks/
    proc_<processID>.sock           # Per-process Unix socket
  sessions/
    <sessionID>/
      history.jsonl                 # Conversation history
      inbox.jsonl                   # Async job results
```

Temp directories per run: `/tmp/reedruns/<runID>/` (cleaned up when run loop exits).
