---
title: MCP System
summary: MCP pool (client management), transport types, tool wrapping, server serving, trigger handling
read_when: working with MCP servers, understanding Reed's dual MCP role
---

# MCP System

Package: `internal/mcp`

## Dual Role

Reed operates as both MCP client and MCP server:

- **MCP Client** (top-level `mcp_servers`): Reed connects to external MCP servers via `Pool`. Tools from those servers are exposed to agents.
- **MCP Server** (`on.service.mcp`): Reed exposes workflow-defined tools to external MCP clients via `NewMCPServer`.

## Pool (Client Management)

`Pool` manages connections to external MCP servers. Thread-safe for concurrent tool calls. Owned by `reed.Manager` as a global singleton.

Constructor: `NewPool(opts ...PoolOption)`. Options: `WithMaxResultSize(bytes)`, `WithTransportFactory(f)`.

### Initialization

- `LoadAndInit(ctx, specs)` -- non-blocking. Registers servers and starts concurrent background initialization. All servers initialize in parallel with a 30-second timeout.
- `StartAll(ctx, specs)` / `Start(ctx, id, spec)` -- synchronous alternatives.
- `waitReady(ctx)` -- blocks internally until background init completes.

### Operations

- `ListTools(ctx, id)` -- tools for one server.
- `ListAllTools(ctx)` -- tools from all ready servers. Names are prefixed: `"{serverID}__{toolName}"`.
- `CallTool(ctx, serverID, toolName, args)` -- invoke a tool. Results exceeding `maxResultSize` (default 128KB) are truncated. Connection errors trigger automatic background reconnection.
- `ListServers(ctx, name)` -- server info with fuzzy name matching.
- `Stop(id)` / `StopAll()` -- close connections.

### Server Status

`ServerStatus` enum: `pending` -> `starting` -> `ready` / `failed` / `stopped`.

## Transport

`Transport` interface: `Connect`, `ListTools`, `CallTool`, `Ping`, `Close`.

`NewTransportFactory()` returns a factory that creates real transports using the MCP go-sdk:

| Transport | Config Required |
|---|---|
| `stdio` (default) | `command` + optional `args`, `env` |
| `sse` | `url` + optional `header` |
| `streamable-http` | `url` + optional `header` |

Environment variables from `spec.Env` are overlaid on `os.Environ()`. Custom headers are injected via a `headerRoundTripper`.

## Tool Wrapping

`convertTools` converts `[]ToolInfo` to `[]model.ToolDef`, optionally prefixing names with `"{serverID}__"`.

## Error Classification

`ConnError` wraps connection errors with a `ConnErrorKind`:

- `stdio_exit` -- stdio process exited.
- `offline` -- broken pipe, connection refused, reset, EOF.
- `auth` -- 401/403 responses.
- `other` -- unclassified.

## Fuzzy Name Matching

`matchName` resolves server names through: exact match -> normalized (strip non-alphanumeric, lowercase) -> case-insensitive -> Levenshtein distance (threshold: 30% of max length, min 2). Returns `MatchAuto` (high confidence) or `MatchSuggest`.

## Server Serving (Reed as MCP Server)

`NewMCPServer(tools, handler)` creates a go-sdk `Server` with tools from `on.service.mcp` definitions. The `ToolCallHandler` function dispatches tool calls.

`NewStreamableHandler(server)` wraps the server as a stateless Streamable HTTP handler, mounted at `/mcp` by the HTTP service.

Input schemas are built from workflow `InputSpec` maps into JSON Schema objects.

## Trigger Handling

`TriggerHandler` bridges MCP tool calls to workflow runs. `HandleToolCall(ctx, toolName, arguments)`:

1. Finds the matching `MCPTool` definition from `on.service.mcp`.
2. Builds `TriggerParams` with `TriggerMCP` type.
3. Resolves a `RunRequest` via `RunResolver`.
4. Submits the run via `RunSubmitter` and waits for completion.
5. Returns run outputs on success.
