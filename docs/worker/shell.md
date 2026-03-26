---
title: Shell Worker
summary: Shell command execution with process groups, timeouts, platform-specific behavior, output limiting, and event streaming.
read_when: Debugging shell step failures, understanding timeout/kill behavior, or adding shell features.
---

## ShellWorker

`ShellWorker` (`internal/worker/shell.go`) handles steps with `uses: shell`, `uses: bash`, or `uses: run`. It extracts the command from `payload.With["run"]` (fallback: `payload.With["cmd"]`). Missing both is a `StepFailed` error.

## Shell Resolution

`resolveShellInvocation(uses, requested)` dispatches to platform-specific `resolveShellPlatform()`:

- `uses: bash` -- strict: requires real bash via `shellutil.ResolveBash()`. Rejects non-bash shell overrides (error if `Shell` is set to anything other than `""` or `"bash"`).
- `uses: shell` / `uses: run` -- platform default with fallback.
- Named shell override via `payload.Shell` field.

**Unix** (`shell_unix.go`):
- Default for shell/run: bash if found, else `sh`.
- Supported named shells: `bash`, `sh`, `zsh` (all via `exec.LookPath`, bash via `shellutil.ResolveBash()`).
- Process groups via `Setpgid: true`.

**Windows** (`shell_windows.go`):
- Default for shell/run: `powershell -NoProfile -NonInteractive -Command`.
- Supported named shells: `bash`, `sh`, `zsh`, `powershell`, `cmd`.
- No process group support (`setSysProcAttr` is a no-op).

## Execution Flow

1. Extract command string from `With["run"]` or `With["cmd"]`.
2. Resolve shell invocation via `resolveShellInvocation(uses, Shell)`.
3. Apply step timeout via `context.WithTimeout` if `payload.Timeout > 0`.
4. Build `exec.Cmd` with resolved shell binary + flags + command string.
5. Set `SysProcAttr` for process groups (Unix only).
6. Resolve WorkDir: convert to absolute path, evaluate symlinks.
7. Merge env: OS `os.Environ()` + `payload.Env` overlay (appended, so step vars win).
8. Capture stdout/stderr via `limitedWriter` (10 MB cap).
9. If `Bus` is non-nil and `StepRunID` is set, wrap writers with `busWriter` to stream output events to `bus.StepOutputTopic(StepRunID)`.
10. Publish `shell_start` status event to bus.
11. `startAndWait(ctx, cmd)` -- starts process, waits for completion or context cancellation.
12. Publish `shell_end` status event (succeeded/failed + duration).

## Timeout and Kill

On context cancellation or timeout, `startAndWait` calls `killProcessGroup`:

- **Unix**: SIGTERM to process group (`-pgid`), wait up to 2-second grace period, then SIGKILL.
- **Windows**: Immediate `Process.Kill()` (no process group signal equivalent).

## Output Limiting

`limitedWriter` (`limited_writer.go`) caps captured output at `maxOutputBytes` (10 MB). Beyond the limit, writes are silently discarded. `Write()` always returns `len(p), nil` to avoid `exec.Cmd` short-write errors. Overflow is tracked internally (`Overflowed()`) but never surfaced in the step result — truncation is silent to the caller.

## Note on Background

`StepPayload.Background` exists on the worker contract but `ShellWorker` ignores it. All shell commands run synchronously; background execution is handled at the engine dispatch level, not inside the worker.

## Event Streaming

When `payload.Bus` is non-nil and `payload.StepRunID` is set, ShellWorker publishes events to `bus.StepOutputTopic(StepRunID)`:

| Event | Type | Payload | When |
|-------|------|---------|------|
| Shell start | `status` | `StatusPayload{Message: "shell_start cmd=<path>"}` | Before execution |
| Output chunk | `text` | `TextPayload{Delta: <chunk>}` | Each stdout/stderr write |

Note: stdout and stderr are published to the same bus topic with the same event type. There is no field to distinguish which stream produced a given chunk.
| Shell end | `status` | `StatusPayload{Message: "shell_end status=<s> duration=<d>"}` | After execution |

## Outputs

| Key | Value |
|-----|-------|
| `stdout` | Captured stdout string |
| `stderr` | Captured stderr string |
| `duration` | Elapsed time string |
| `result` | Auto-parsed JSON if stdout (trimmed) starts with `{` or `[` |

Non-zero exit code sets `StepFailed` with the error message from `cmd.Wait()`.

## JSON Auto-Parse

After successful execution, if stdout (whitespace-trimmed) starts with `{` or `[`, the worker attempts `json.Unmarshal`. On success, the parsed value is stored in `outputs.result`. On failure, `result` is simply omitted (no error).
