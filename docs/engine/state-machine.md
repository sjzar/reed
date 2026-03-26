---
title: Engine State Machine
summary: Engine struct, Config, Submit/RunHandle, run loop phases, DAG dispatch, state transitions, stop/cancel semantics, and retention policy.
read_when: Working on engine internals, debugging run lifecycle, or understanding step dispatch.
---

## Engine Struct

`Engine` (`internal/engine/engine.go`) is the unified stateful runtime for a single Process. Key fields:

- `worker Worker` -- blackbox executor interface
- `processID string`, `pid int`, `mode model.ProcessMode`, `status model.ProcessStatus`
- `workflow *model.Workflow` -- bound at creation, immutable
- `activeRuns map[string]*runState` -- in-flight runs
- `terminalRuns map[string]RunView` -- completed run snapshots
- `retention RetentionPolicy` -- eviction settings
- `sink EventSink` -- optional JSONL persistence
- `bus *bus.Bus` -- lifecycle event publishing
- `sealed bool` -- when true, DoneCh closes after all active runs terminate
- `doneCh chan struct{}` -- closed when sealed and no active runs remain

Constructor: `New(ctx, worker, cfg Config, opts ...Option) (*Engine, error)`. Requires `ProcessID`, `Bus`, and `Workflow` in Config.

## Config and RetentionPolicy

`Config` fields: `ProcessID`, `PID`, `Mode`, `Workflow`, `WorkflowSource`, `Retention`, `Bus`, `EventSink`.

`RetentionPolicy`: `MaxTerminalRuns` (default 200), `TerminalTTL` (default 30min). Eviction runs on every `moveToTerminal` call -- first by TTL, then by count.

## Submit and RunHandle

`Submit(*model.RunRequest) (*RunHandle, error)` creates a `runState`, adds it to `activeRuns`, and launches `rs.loop()` in a goroutine. Returns a `RunHandle` with an `ID()` method. Rejects calls after `Close()`.

`WaitRun(ctx, runID)` blocks until the run reaches terminal state via `rs.doneCh`. `GetRun(runID)` returns a snapshot without blocking.

## Run Loop Phases

The run loop (`run_loop.go`) executes sequentially:

1. **Init**: Create temp dir at `/tmp/reedruns/<runID>`. Set status `RunStarting`.
2. **initStepRuns**: Create a `model.StepRun` (status `StepPending`) for every step in every job.
3. **dispatchReady**: Find and dispatch steps whose job `needs` dependencies are met. Steps within a job execute sequentially (first pending wins).
4. **Set RunRunning**: Transition to `RunRunning`.
5. **Event loop**: Select on `stopCh`, `stepRunResultCh`, `runTimeoutCh`, `graceTimer.C`, `rootCtx.Done()`.

Early exits: if all steps resolve synchronously (terminal check) or the DAG is stuck (no running, some pending), cancel remaining and drain.

## DAG Dispatch

`dispatchReady()` loops until no more synchronous state changes occur:

- `findReadyStepRuns()`: For each job, find the first non-terminal step. Include it if all `job.Needs` are in the `completedJobs()` set (all steps succeeded).
- `dispatch(sr)`: Calls `buildPayload` (renders `if`, `with`, `env`, `workdir`, agent spec) then `launchWorker`.
- `buildPayload` may synchronously skip (falsy `if`) or fail (render error) a step, which triggers re-evaluation.

`launchWorker` sets step to `StepRunning`, starts a goroutine calling `worker.Execute()`, and sends the result to `stepRunResultCh` (buffered 64).

## State Transitions

**RunStatus**: `RunCreated` -> `RunStarting` -> `RunRunning` -> `RunStopping` (on stop) -> terminal (`RunSucceeded` | `RunFailed` | `RunCanceled`).

**StepStatus**: `StepPending` -> `StepRunning` -> terminal (`StepSucceeded` | `StepFailed` | `StepSkipped` | `StepCanceled`).

**Finalize logic** (`run_terminal.go`):
- `stopRequested=true` -> `RunCanceled` regardless of step outcomes.
- Any `StepFailed` -> `RunFailed`.
- Any `StepCanceled` (without failed) -> `RunCanceled`.
- Otherwise -> `RunSucceeded`.
- Output render errors on a succeeded run force `RunFailed` with code `OUTPUT_RENDER_ERROR`.

## Stop and Cancel Semantics

`StopAll()` / `StopRun(id)` send to `rs.stopCh` (buffered 1).

Two-phase graceful stop (`onStopRequested`):
1. Set `stopRequested=true`, status `RunStopping`, cancel `rootCtx`.
2. Start grace timer (3 seconds). Worker results arriving via `stepRunResultCh` are processed normally.
3. If grace timer fires, `cancelAllPending()` marks PENDING steps as `StepSkipped` and RUNNING steps as `StepCanceled`.

`Close(ctx)` on the Engine: sets `closed=true`, cancels `rootCancel`, waits for all run `doneCh` channels or `ctx` timeout. Final process status: `ProcessStopped` unless any run failed (`ProcessFailed`).

## Seal

`Seal()` marks the engine so `DoneCh` closes when all active runs terminate. Submit is still allowed after Seal (for schedule triggers), but DoneCh only closes when those runs also finish.

## Lifecycle Event Persistence

If `EventSink` is provided, Engine subscribes to `bus.TopicLifecycle` and persists events via `sink.Append()` in a dedicated goroutine. Events are drained on unsubscribe.

## Key Types

| Type | File | Purpose |
|------|------|---------|
| `RunView` | types.go | Read-only run snapshot with Jobs, Outputs, Status |
| `JobView` | types.go | Job snapshot with Steps map and derived status |
| `StepView` | types.go | Step snapshot with outputs and error info |
| `ProcessView` | types.go | Full engine snapshot for status reporting |
| `StepPayload` | interfaces.go | Fully-rendered data sent to Worker |
| `StepRunResult` | interfaces.go | Worker execution outcome |
| `Worker` | interfaces.go | `Execute(ctx, StepPayload) StepRunResult` |
| `EventSink` | interfaces.go | `Append(Event) error` |
