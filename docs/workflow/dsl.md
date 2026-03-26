---
title: Workflow DSL
summary: Workflow YAML structure, trigger modes, jobs/steps schema, agents, skills, MCP servers
read_when: writing or modifying workflow YAML files, or working on the workflow parser/validator
---

# Workflow DSL

JSON Schema: [`workflow-schema.json`](workflow-schema.json)

## Top-Level Fields

Defined in `model.Workflow` (`internal/model/workflow.go`):

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `app` | string | No | | Application name |
| `name` | string | No | | Workflow name |
| `version` | string | No | | Version string |
| `description` | string | No | | Human-readable description |
| `on` | OnSpec | No | implicit `cli: {}` | Trigger configuration |
| `tool_access` | string | No | `"workdir"` | `"workdir"` or `"full"` |
| `run_jobs` | []string | No | | Default DAG entrypoint filter; each item must reference an existing job ID |
| `inputs` | map[string]InputSpec | No | | Input parameter definitions |
| `outputs` | map[string]string | No | | Output expressions (must not pierce into steps; use `jobs.<id>.outputs.<key>`) |
| `env` | map[string]string | No | | Environment variables |
| `agents` | map[string]AgentSpec | No | | Agent role definitions |
| `skills` | map[string]SkillSpec | No | | Skill definitions |
| `mcp_servers` | map[string]MCPServerSpec | No | | MCP server connections (Reed as client) |
| `jobs` | map[string]Job | **Yes** | | Job DAG; must contain at least one job |
| `metadata` | map[string]any | No | | Opaque metadata |

## Trigger Modes (`on` block)

Defined in `model.OnSpec`. Mutual exclusivity rules (enforced in `validate.go`):
- `cli` and `service` are mutually exclusive
- `cli` and `schedule` are mutually exclusive
- `service` and `schedule` can coexist

No `on` block — or an empty `on: {}` — implies `on: { cli: {} }` (set in `postProcess`).

### CLI Trigger

```yaml
on:
  cli:
    commands:
      build:
        description: "Run build"
        run_jobs: [compile, test]
        inputs:
          target:
            type: string
            default: "linux"
        outputs:
          artifact: "${{ jobs.compile.outputs.path }}"
```

CLICommand fields:

| Field | Type | Description |
|---|---|---|
| `description` | string | Human-readable description |
| `run_jobs` | []string | Job IDs to run (must reference existing jobs) |
| `inputs` | map[string]InputSpec | Input overrides for this command |
| `outputs` | map[string]string | Output overrides for this command |

### Service Trigger

```yaml
on:
  service:
    port: 8080
    http:
      - path: /webhook
        method: POST
        async: false
        run_jobs: [process]
        concurrency:
          group: "${{ inputs.session_id }}"
          behavior: queue  # queue | skip | replace-pending
    mcp:
      - name: my_tool
        description: "Tool description"
        run_jobs: [handle]
        inputs: { ... }
        outputs: { ... }
```

`port` is required (must be > 0). If both `http` and `mcp` are empty, a default `POST /` route is auto-injected by `postProcess`.

#### HTTPRoute fields

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | **Yes** | URL path |
| `method` | string | **Yes** | HTTP method: `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` (case-insensitive; validator uppercases before checking) |
| `async` | bool | No | Async execution (default: false) |
| `run_jobs` | []string | No | Job IDs to run |
| `inputs` | map[string]InputSpec | No | Input definitions |
| `outputs` | map[string]string | No | Output expressions |
| `concurrency` | ConcurrencySpec | No | Concurrency control |

#### ConcurrencySpec fields

| Field | Type | Required | Description |
|---|---|---|---|
| `group` | string | **Yes** | Partition key expression (must not be empty/whitespace) |
| `behavior` | string | No | `"queue"` (default), `"skip"`, or `"replace-pending"` |

#### MCPTool fields

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | **Yes** | Tool name (must not be empty) |
| `description` | string | No | Human-readable description |
| `run_jobs` | []string | No | Job IDs to run |
| `inputs` | map[string]InputSpec | No | Input definitions |
| `outputs` | map[string]string | No | Output expressions |

### Schedule Trigger

```yaml
on:
  schedule:
    - cron: "0 9 * * *"
      run_jobs: [daily_report]
    - cron: "*/30 * * * *"
      run_jobs: [health_check]
```

Cron expressions are validated using 5-field format (minute, hour, dom, month, dow).

| Field | Type | Required | Description |
|---|---|---|---|
| `cron` | string | **Yes** | 5-field cron expression |
| `run_jobs` | []string | No | Job IDs to run |

## Inputs

```yaml
inputs:
  question:
    type: string
    required: true
    description: "The question to ask"
  count:
    type: integer
    default: 10
```

| Field | Type | Description |
|---|---|---|
| `type` | string | `"string"`, `"number"`, `"boolean"`, `"integer"`, `"media"`, `"[]media"`. Note: type enum is only validated for trigger-scoped inputs (on.service.http, on.service.mcp, on.cli.commands), not for workflow-level inputs |
| `required` | bool | Whether the input must be provided (default: false) |
| `default` | any | Default value if not provided |
| `description` | string | Human-readable description |

## Agents

```yaml
agents:
  qa_expert:
    model: ${{ env.REED_DEFAULT_MODEL ?? 'gpt-5.4' }}
    system_prompt: |
      You are a knowledgeable assistant.
    skills:
      - file-reader
    max_iterations: 20
    temperature: 0.7
    thinking_level: high
```

| Field | Type | Description |
|---|---|---|
| `model` | string | LLM model identifier (supports expressions) |
| `system_prompt` | string | System prompt (supports multi-line YAML) |
| `skills` | []string | Skill IDs to mount; each must reference a defined skill |
| `max_iterations` | int | Maximum conversation iterations; 0 = system default |
| `temperature` | float | LLM temperature; omit for model default |
| `thinking_level` | string | Extended thinking level; omit for model default |

Steps reference agents via `uses: agent` with `with.agent: <agent_id>`. The agent ID must exist in the `agents` map.

## Skills

```yaml
skills:
  # Catalog reference
  web-search:
    uses: web-search

  # Inline definition
  file-reader:
    resources:
      - path: "SKILL.md"
        content: |
          ---
          name: file-reader
          description: "Read files"
          allowed_tools: ["bash"]
          ---
          You can read files from the filesystem.
```

Exactly one of `uses` or `resources` must be provided (mutually exclusive).

| Field | Type | Description |
|---|---|---|
| `uses` | string | Skill catalog reference |
| `resources` | []SkillResourceSpec | Inline skill resources |

SkillResourceSpec:

| Field | Type | Required | Description |
|---|---|---|---|
| `path` | string | **Yes** | Relative file path. Must not be absolute, must not contain `..` traversal, no duplicate paths allowed. Nested `SKILL.md` (e.g., `subdir/SKILL.md`) is rejected |
| `content` | string | **Yes** | Resource file content. For `SKILL.md`: YAML frontmatter (name, description, allowed_tools) + markdown body |

The resource set must include exactly one root-level `SKILL.md`.

## MCP Servers

```yaml
mcp_servers:
  local-tools:
    transport: stdio
    command: npx
    args: ["-y", "@my/mcp-server"]
    env:
      API_KEY: ${{ env.API_KEY }}

  remote-api:
    transport: streamable-http
    url: https://api.example.com/mcp
    header:
      Authorization: "Bearer ${{ env.TOKEN }}"
```

| Field | Type | Required | Description |
|---|---|---|---|
| `transport` | string | **Yes** | `"stdio"`, `"sse"`, or `"streamable-http"` |
| `command` | string | If stdio | Command to execute |
| `args` | []string | No | Command arguments |
| `url` | string | If sse/streamable-http | Server URL |
| `env` | map[string]string | No | Environment variables for the server process |
| `header` | map[string]string | No | HTTP headers (for HTTP-based transports) |

## Jobs

```yaml
jobs:
  build:
    needs: [setup]
    steps: [...]
    outputs:
      artifact: "${{ steps.compile.outputs.path }}"
```

`Job.ID` is set from the map key during `postProcess`. `needs` declares DAG dependencies. Cycle detection and self-dependency checks are performed during validation.

| Field | Type | Required | Description |
|---|---|---|---|
| `needs` | []string | No | Job IDs that must complete first; no cycles or self-references |
| `steps` | []Step | **Yes** | Steps to execute; must contain at least one |
| `outputs` | map[string]string | No | Output expressions; can reference `${{ steps.<id>.outputs.<key> }}` |

## Steps

```yaml
steps:
  - id: compile
    uses: shell
    run: "go build ./..."
    with: { ... }
    if: "${{ inputs.enabled }}"
    timeout: 300
    workdir: /app
    shell: bash
    env: { BUILD: release }
    background: false
```

| Field | Type | Default | Description |
|---|---|---|---|
| `id` | string | auto (`step_N`) | Step identifier. postProcess auto-fills IDs before validation |
| `uses` | string | | Worker type: `"shell"`, `"bash"`, `"run"`, `"agent"`, `"http"` |
| `run` | string | | Shell command (DSL sugar; promoted to `with.run` during postProcess). Does NOT auto-set `uses`; you must set `uses` explicitly |
| `with` | map[string]any | | Worker parameters |
| `if` | string | | Conditional expression (step skipped if false) |
| `timeout` | int | 0 (system default) | Timeout in seconds |
| `workdir` | string | | Working directory override |
| `shell` | string | | Shell ID: `bash`, `sh`, `zsh`, `powershell`, `cmd` |
| `env` | map[string]string | | Step-level environment variables |
| `background` | bool | false | Run in background |

### Worker types

Registered workers (in `internal/worker/router.go`):

- **shell / bash / run** — All route to `ShellWorker`. Execute shell commands via `run` field or `with.run`
- **agent** — AI agent conversation loop via `with.agent` (agent ID) and `with.prompt`
- **http** — HTTP request worker

Using an unregistered `uses` value will fail at runtime with "unknown uses".

### Shell field rules

- `shell` is only valid when `uses` is `"shell"`, `"bash"`, `"run"`, or empty
- Valid shell IDs: `bash`, `sh`, `zsh`, `powershell`, `cmd`
- `uses: bash` does not allow shell override (must be `"bash"` or omitted)

### Run field promotion

The `run` field is DSL sugar. During `postProcess`, it is promoted to `with["run"]`. If `with["run"]` already exists, the `run` field is ignored. Note: this only moves the value into `with`; it does NOT set `uses`. A step with `run` but no `uses` will fail at runtime. Always pair `run` with `uses: shell` (or `bash`/`run`).

## Validation Summary

`workflow.Validate()` performs the following compile-time checks:

1. **Jobs**: at least one job must exist, each job must have at least one step
2. **DAG**: no cycles, no self-dependencies, all `needs` references must be valid
3. **On block**: CLI/service mutual exclusivity, CLI/schedule mutual exclusivity
4. **Tool access**: must be `"workdir"` or `"full"` if specified
5. **Shell fields**: only on shell-type workers, must use valid shell ID
6. **Agent references**: `with.agent`, if present, must reference a defined agent (presence of `with.agent` or `with.prompt` is not required at compile time)
7. **Skill references**: agent skill arrays must contain non-empty strings (existence is checked at runtime, not compile time)
8. **Skill specs**: exactly one of `uses` or `resources`; resources must have exactly one root-level `SKILL.md`, relative paths, no traversal, no duplicates
9. **MCP servers**: transport required and valid; command/url required per transport type (url is checked for non-empty only, no URI format validation)
10. **Output expressions**: rejects expressions containing both `jobs.` and `.steps` (cross-job step piercing); bare `steps.` at workflow scope is NOT caught
11. **Run jobs**: all referenced job IDs must exist
12. **Step IDs**: no duplicates within the same job (background+id rule exists but is effectively dead since postProcess auto-fills IDs first)
13. **Uses field**: remote URLs, fragment (`#`) and registry (`@`) references are rejected via substring checks
14. **Input types**: type enum validated for trigger-scoped inputs only (on.service.http, on.service.mcp, on.cli.commands), not workflow-level inputs

Note: the parser silently ignores unknown YAML fields. Unknown keys are not rejected at parse or validation time.

## PostProcess Defaults

Applied automatically after YAML parsing (in `internal/workflow/parse.go`):

1. No `on` block — or empty `on: {}` — → `on: { cli: {} }`
2. Service with port but no http/mcp → inject default `POST /` route
3. All nil maps initialized to empty maps
4. `Job.ID` set from map key
5. Step IDs auto-generated as `step_0`, `step_1`, ... if omitted
6. `run` field promoted to `with["run"]` and cleared
