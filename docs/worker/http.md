---
title: HTTP Worker
summary: HTTP request building, response handling, output mapping, file downloads, event streaming, and error semantics.
read_when: Debugging HTTP steps, understanding response handling, or adding HTTP worker features.
---

## HTTPWorker

`HTTPWorker` (`internal/worker/http.go`) handles steps with `uses: http`. Default HTTP client timeout: 30 seconds (used when `HTTPWorker.Client` is nil). Note: `StepPayload.Timeout` is not used by HTTPWorker — duration is controlled only by the incoming context and the `http.Client` timeout.

## Request Building

Parameters from `payload.With`:

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `url` | string | required | Request URL |
| `method` | string | `GET` | HTTP method (uppercased) |
| `body` | string/map/any | nil | Request body; non-string types auto-marshal to JSON |
| `headers` | map[string]any | nil | Custom request headers (string values only; non-string values are silently ignored) |
| `output_file` | string | "" | Stream response to file instead of memory |
| `success_on_http_error` | bool | false | Treat HTTP 4xx/5xx as success |

When `body` is a `map[string]any` or any non-string/non-nil type, it is JSON-marshaled and `Content-Type: application/json` is set automatically. Custom headers are applied after this auto-detection, so a user-supplied `Content-Type` header will override the automatic `application/json`.

## Response Handling

Two branches based on `output_file`:

**Branch A -- In-memory** (no output_file):
1. Check response `Content-Type` against blocked MIME prefixes: `video/*`, `audio/*`, `application/octet-stream`, `application/zip`. Blocked types return `StepFailed` suggesting `output_file`.
2. Read body with limit check: reads `maxOutputBytes + 1` bytes via `io.LimitReader`. If the full amount is read, the body exceeds the limit and returns `StepFailed`.
3. Body chunks are streamed to bus via `busWriter` (if bus is available).
4. JSON auto-parse: if trimmed body starts with `{` or `[`, parsed into `outputs.result`.

**Branch B -- File download** (output_file set):
- Resolves relative paths against `payload.WorkDir`, then makes absolute.
- Creates parent directories with `os.MkdirAll`.
- Streams body directly to file via `io.Copy`.
- Sets `outputs.filepath` to the absolute path.
- No MIME check, no size limit, no JSON auto-parse.

## Error Semantics

HTTP status >= 400 sets `StepFailed` with message `"http worker: HTTP <code>"` unless `success_on_http_error: true`. The status check happens **after** body reading/file writing, so all outputs (`body`, `code`, `headers`, `result`, `filepath`) are populated even on failure.

## Event Streaming

When `payload.Bus` is non-nil and `payload.StepRunID` is set, HTTPWorker publishes events to `bus.StepOutputTopic(StepRunID)`:

| Event | Type | Payload | When |
|-------|------|---------|------|
| HTTP start | `status` | `StatusPayload{Message: "http_start method=<M> url=<U>"}` | Before request |
| HTTP response | `status` | `StatusPayload{Message: "http_response code=<C> duration=<D>"}` | After response headers |
| Body chunk | `text` | `TextPayload{Delta: <chunk>}` | Each body read (in-memory mode) |
| HTTP end (success) | `status` | `StatusPayload{Message: "http_end status=succeeded code=<C> duration=<D>"}` | On success |
| HTTP end (failure) | `status` | `StatusPayload{Message: "http_end status=failed ..."}` | On failure |

Note: failure events have two different formats depending on the failure type:
- Early/runtime failures: `"http_end status=failed error=<message>"`
- HTTP status failures (4xx/5xx): `"http_end status=failed code=<code>"`

## Outputs

| Key | Value | Condition |
|-----|-------|-----------|
| `code` | HTTP status code (int) | Always |
| `duration` | Elapsed time string | Always |
| `headers` | Response headers (map[string]string) | Always |
| `body` | Response body string | In-memory mode only |
| `result` | Parsed JSON (any) | In-memory, valid JSON object/array only |
| `filepath` | Absolute file path | File download mode only |
