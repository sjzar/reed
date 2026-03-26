---
title: Agent Worker
summary: AgentWorker step-to-agent mapping, tool resolution, namespace derivation, parameter extraction, media attachment, and the Build dependency chain.
read_when: Debugging agent steps, understanding tool/skill injection, parameter passing, or modifying agent worker behavior.
---

## AgentWorker

`AgentWorker` (`internal/worker/agent.go`) adapts `engine.Worker` to `agent.Runner`. It handles steps with `uses: agent`.

## Execution Flow

1. **Parse agent ID**: From `payload.With["agent"]`. Looks up `AgentSpec` in `workflow.Agents`. Missing agent ID is an error only if specified but not found.
2. **Extract inputs**: `prompt`, `session_key`, `session_id` from `payload.With`.
3. **Namespace resolution** (priority order): `with.namespace` > `app:cleanWorkDir` > `cleanWorkDir` > `app` > `workflow.Source` > `workflow.Name`.
4. **Mutual exclusion**: `session_key` and `session_id` cannot both be set (returns `StepFailed`).
5. **Tool resolution** (see below).
6. **Build `AgentRunRequest`**: Deep-copies env, merges AgentSpec defaults, applies rendered overrides, extracts model parameters from `With`, collects media URIs.
7. **Pass Bus + StepRunID**: For streaming agent events.
8. **Delegate**: Calls `agent.Runner.Run(ctx, req)`.
9. **Convert result**: Maps `AgentRunResponse` to `StepRunResult`.

## Tool Resolution

Two modes based on whether `with.tools` is set:

**Explicit tools** (`with.tools` provided):
- Converted to `[]string` via `toStringSlice()`. Invalid format returns `StepFailed`.
- Used exactly as specified, order preserved (no sorting).

**Implicit tools** (no `with.tools`):
1. Collect `allowed_tools` from all skills referenced by the agent spec.
2. Normalize Claude-sanitized names: `mcp__server__tool` -> `mcp/server/tool` (only when `__` present). Prefer the normalized form.
3. Union with `coreToolIDs` (additive, not replacing).
4. Sort alphabetically for determinism.
5. If no skill tools found, `tools` stays nil (agent engine uses core defaults).

## With Parameters

### Core Parameters

| Key | Type | Description |
|-----|------|-------------|
| `agent` | string | Agent ID to look up in `workflow.Agents` |
| `prompt` | string | User prompt (required for meaningful execution) |
| `namespace` | string | Explicit namespace override |
| `session_key` | string | User-provided key for session lookup |
| `session_id` | string | Explicit session ID |
| `tools` | []string / []any | Explicit tool list (overrides skill-based resolution); accepts `[]string` or `[]any` with string elements |

### Model Parameters (override AgentSpec defaults)

| Key | Type | Description |
|-----|------|-------------|
| `schema` | map[string]any | Structured output schema |
| `extra_params` | map[string]any | Additional API parameters |
| `max_tokens` | int | Max tokens for generation |
| `temperature` | float64 | Sampling temperature |
| `top_p` | float64 | Top-p sampling |
| `thinking_level` | string | Thinking level for extended thinking |
| `max_iterations` | int | Max agent loop iterations |
| `timeout_ms` | int | Agent timeout in milliseconds |
| `profile` | string | Agent profile |
| `prompt_mode` | string | Prompt assembly mode |

Note: `model` and `system_prompt` are **not** read from `With`. They come only from the `AgentSpec` (workflow agent definition) and can be overridden by expression-evaluated `RenderedAgentModel` / `RenderedAgentSystemPrompt` set by engine dispatch.

### Override Priority

For `model` and `system_prompt`:
1. `RenderedAgentModel` / `RenderedAgentSystemPrompt` (expression-evaluated by engine dispatch) -- highest
2. `AgentSpec.Model` / `AgentSpec.SystemPrompt` -- lowest (defaults from workflow agent definition)

For `max_iterations`, `temperature`, `thinking_level`: AgentSpec defaults apply only when the request field is zero/nil (i.e., not set by `With`).

### Type Coercion

Invalid types for numeric/map parameters are logged as warnings (not fatal). Fractional floats for int fields (e.g., `max_tokens: 1.9`) are rejected to avoid silent truncation. String-typed fields (`prompt`, `namespace`, `session_key`, `session_id`, `thinking_level`, `profile`, `prompt_mode`) use Go type assertion — non-string values are silently ignored (no warning logged).

## Derived Request Fields

Several `AgentRunRequest` fields are derived from the payload rather than from `With`:

| Request Field | Source |
|---------------|--------|
| `RunRoot` | `payload.Env["REED_RUN_TEMP_DIR"]` |
| `Cwd` | `payload.WorkDir` |
| `ToolAccessProfile` | `payload.ToolAccess` |
| `Skills` | `AgentSpec.Skills` (from workflow agent definition) |
| `Env` | Deep copy of `payload.Env` |
| `Bus` | `payload.Bus` (for streaming agent events) |
| `StepRunID` | `payload.StepRunID` |

## Media Attachment

`collectMediaURIs(With)` scans all `With` parameter values (in key-sorted order for determinism) for `media://` URIs. Found in string values, `[]string` values, and `[]any` values. Collected URIs are passed to `AgentRunRequest.Media`.

## Namespace Resolution Detail

`cleanWorkDir()` converts the working directory to an absolute, cleaned path.

Priority chain:
1. `With["namespace"]` (explicit)
2. `workflow.App + ":" + cleanWorkDir(WorkDir)` (when both present)
3. `cleanWorkDir(WorkDir)` (when only WorkDir)
4. `workflow.App` (when only app)
5. `workflow.Source` (fallback)
6. `workflow.Name` (last resort)

## Outputs

| Key | Value |
|-----|-------|
| `output` | Final assistant text content |
| `iterations` | Number of agent loop iterations |
| `total_tokens` | Total token usage (`AgentRunResponse.TotalUsage.Total`) |
| `stop_reason` | Agent stop reason string (cast to string) |

## workerSkillProvider Interface

```go
type workerSkillProvider interface {
    Get(id string) (*skill.ResolvedSkill, bool)
}
```

Used by `AgentWorker` to look up skill metadata for tool dependency resolution.

## Build Function

There is no separate `BuildWorker` type. The `Build()` function in `internal/worker/build.go` constructs the full dependency chain. See `docs/worker/overview.md` for the complete construction sequence.
