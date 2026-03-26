---
title: IPC Protocol
summary: HTTP over Unix socket, socket path, endpoints, request/response formats
read_when: working with inter-process communication, debugging status queries
---

# IPC Protocol

Package: `internal/http`

## Overview

Reed uses HTTP over Unix domain sockets for IPC between CLI commands and running processes. The IPC server and HTTP server share the same Gin router.

## Socket Path

Process-scoped: `~/.reed/socks/proc_<processID>.sock`. Permissions set to `0600`. Parent directory created with `0700`. Stale socket files are removed before binding.

## Transport

The IPC server uses `net.Listen("unix", sockPath)` and serves via a standard `http.Server`. A `ConnContext` hook injects an IPC flag into the request context. `IsIPC(c *gin.Context) bool` checks if a request arrived via the Unix socket.

## Endpoints

All endpoints are registered under both `/v1/` and `/api/v1/` (with no-cache middleware on the latter).

### GET /v1/ping

Returns basic process liveness info.

Response: `model.PingResponse` with `processID`, `pid`, `mode`, `now`.

### GET /v1/status

Returns full process status.

Response: `model.StatusView` with process info, listeners, and active runs.

### GET /v1/runs/:runID

Returns status of a specific run.

Response: Run data or 404 if not found.

### POST /v1/runs/:runID/stop

Stops a specific run.

Response: `{"runID": "...", "stopped": true}` or 404 if not found/already terminal.

### GET /health

Simple health check. Returns `{"status": "ok"}`.

## StatusProvider Interface

The HTTP service delegates to a `StatusProvider`:

```go
type StatusProvider interface {
    PingData() model.PingResponse
    StatusData() (any, error)
    RunData(runID string) (any, bool)
    StopRun(runID string) bool
}
```

## Workflow Routes

`RegisterWorkflowRoutes(routes, dispatcher)` mounts workflow-defined HTTP routes (`on.service.http`) on the shared router. Each route's method defaults to POST if unspecified.

## MCP Handler

`RegisterMCPHandler(handler)` mounts an MCP Streamable HTTP handler at `/mcp` (for `on.service.mcp`).

## Lifecycle

- `StartIPC(sockPath)` -- starts the UDS listener in a background goroutine.
- `ListenAndServe()` -- starts the TCP HTTP server (service mode only).
- `ShutdownIPC(ctx)` / `Shutdown(ctx)` -- graceful shutdown, removes socket file.
