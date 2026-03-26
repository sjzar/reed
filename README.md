# Reed

<div align="center">

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue?style=flat-square)](LICENSE)

**A deterministic agent runtime for LLM-driven DAG workflows.**

</div>

Reed is an agent runtime that anchors LLM reasoning within deterministic DAG workflows. It provides a structured execution environment for complex, automated tasks — extending AI capabilities beyond chat and coding interfaces. Deterministic DAG steps handle the "heavy lifting" of data orchestration and long-context processing, ensuring the Agent operates in a refined, high-signal environment. Agent steps handle judgment calls like classification, analysis, and triage, with optional JSON schema constraints to keep decisions machine-readable. The result: developers can embed autonomous decision-making into engineering ecosystems with repeatable, auditable control.

## How It Works

- **DAG defines the execution backbone.** It manages job dependencies and parallel processing with precision. By handling data collection and state preparation in deterministic steps, it ensures the Agent is only invoked when the context is perfectly primed.
- **Agent steps handle reasoning.** Anything requiring judgment gets an agent step. You can use a JSON schema (`with.schema`) to constrain and validate the output, turning unpredictable reasoning into reliable, structured data.
- **The core synergy: Workflows handle the noise, Agents handle the signal.** Deterministic steps filter and structure long-form data — where LLMs typically struggle — providing a "refined lens" for the Agent. This allows the Agent to focus purely on high-level decisions without being overwhelmed by raw data.

```text
                ┌──────────┐  ┌──────────┐  ┌──────────┐
                │ collect  │  │ collect  │  │ collect  │   Parallel Data Collection
                │ (shell)  │  │ (shell)  │  │ (shell)  │
                └────┬─────┘  └────┬─────┘  └────┬─────┘
                     │             │             │
                     └─────────┬───┘─────────────┘
                               ▼
                        ┌─────────────┐
                        │   decide    │   LLM Reasoning (JSON Schema)
                        │   (agent)   │
                        └──────┬──────┘
                               ▼
                        ┌─────────────┐
                        │    act      │   Deterministic Action
                        │   (shell)   │
                        └─────────────┘
```

## Install

```bash
go install github.com/sjzar/reed@latest
```

Or build from source:

```bash
git clone https://github.com/sjzar/reed.git
cd reed
go build -o reed .
```

## Configure AI Access

Shell-only workflows work without any AI configuration.

For agent workflows, the simplest setup is an environment variable. Reed auto-detects the provider:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
# or
export OPENAI_API_KEY=sk-...
```

For persistent configuration, add a `models` section to `~/.reed/reed.json`:

```json
{
  "models": {
    "default": "gpt-5.4",
    "providers": [
      {
        "id": "anthropic",
        "type": "anthropic-messages",
        "key": "sk-ant-..."
      },
      {
        "id": "openai",
        "type": "openai-completions",
        "key": "sk-..."
      }
    ]
  }
}
```

**Supported provider types:** `anthropic-messages`, `openai-completions`, `openai-responses`.

You can omit the `key` field — Reed fills it from `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` environment variables when the endpoint is the official API. For custom endpoints (proxies, Azure, etc.), set `base_url` and provide the key explicitly.

See [AI Provider Configuration](docs/agent/ai-providers.md) for the full reference: model aliases, failover chains, capability overrides, and OpenAI-compatible proxies.

## Quick Start

```bash
# Run a shell workflow
reed run workflows/getting-started/hello.yml

# Run parallel jobs
reed run workflows/getting-started/parallel.yml

# Run an agent pipeline (requires AI key)
reed run workflows/getting-started/agent-pipeline.yml -i file=README.md

# Process management
reed ps
reed logs <pid> -f
reed stop <pid>
```

**Other entry points:**

```bash
# Interactive agent session
reed do "summarize the last 10 git commits"

# Generate a workflow from a description
reed plan "review all Go files for error handling issues"
```

## Workflow Anatomy

A workflow is a YAML file with triggers, inputs, agent definitions, and a DAG of jobs. Each job contains steps that use a worker type (`shell`, `agent`, or `http`). Expressions (`${{ ... }}`) pass data between steps and jobs. Many example workflows use common Unix tools (`curl`, `jq`, `git`).

```yaml
name: example
on:                     # triggers (cli, schedule, http, mcp)
  schedule: "0 9 * * 1" # optional — cron trigger

inputs:                 # parameters passed at runtime
  target:
    type: string

agents:                 # agent definitions (model, prompt, skills)
  analyzer:
    system_prompt: "..."

jobs:                   # DAG of jobs — parallel by default
  collect:
    steps:
      - uses: shell
        run: echo "hello"
    outputs:
      data: ${{ steps.step_id.outputs.stdout }}

  decide:
    needs: [collect]    # dependency — runs after collect
    steps:
      - uses: agent
        with:
          agent: analyzer
          schema: { ... }  # structured JSON output
```

## Examples

### 1. Parallel DAG

Three concurrent checks fan in to a summary. Pure shell, no AI needed.

From [`workflows/getting-started/parallel.yml`](workflows/getting-started/parallel.yml):

```yaml
jobs:
  check_disk:
    steps:
      - id: disk
        uses: shell
        run: |
          usage=$(df -h / | awk 'NR==2 {print $5}')
          echo "Disk usage: $usage"
    outputs:
      result: ${{ steps.disk.outputs.stdout }}

  check_network:
    steps:
      - id: net
        uses: shell
        run: |
          curl -s --max-time 5 -o /dev/null -w '%{http_code}' https://example.com
    outputs:
      result: ${{ steps.net.outputs.stdout }}

  check_tools:
    steps:
      - id: tools
        uses: shell
        run: |
          for cmd in git curl jq; do
            command -v "$cmd" && echo "$cmd: ok" || echo "$cmd: MISSING"
          done
    outputs:
      result: ${{ steps.tools.outputs.stdout }}

  summary:
    needs: [check_disk, check_network, check_tools]
    steps:
      - uses: shell
        run: |
          echo "Disk:    ${{ jobs.check_disk.outputs.result }}"
          echo "Network: ${{ jobs.check_network.outputs.result }}"
          echo "Tools:   ${{ jobs.check_tools.outputs.result }}"
```

### 2. Skeleton + Agent

Shell collects file metadata, agent classifies with JSON schema, shell formats the output. The core pattern.

From [`workflows/getting-started/agent-pipeline.yml`](workflows/getting-started/agent-pipeline.yml):

```yaml
agents:
  classifier:
    system_prompt: |
      You are a file classifier. Given file metadata and a content preview,
      produce a structured analysis.

jobs:
  collect:
    steps:
      - id: metadata
        uses: shell
        run: |
          file="${{ inputs.file }}"
          echo "Name: $(basename $file)"
          echo "Size: $(wc -c < $file) bytes"
          echo "Lines: $(wc -l < $file)"
          head -30 "$file"
    outputs:
      data: ${{ steps.metadata.outputs.stdout }}

  analyze:
    needs: [collect]
    steps:
      - id: classify
        uses: agent
        with:
          agent: classifier
          prompt: |
            Analyze this file:
            ${{ jobs.collect.outputs.data }}
          schema:
            type: object
            properties:
              file_type: { type: string }
              language:  { type: string }
              summary:   { type: string }
              complexity: { type: string, enum: [low, medium, high] }
            required: [file_type, language, summary, complexity]
    outputs:
      result: ${{ steps.classify.outputs.output }}

  report:
    needs: [analyze]
    steps:
      - uses: shell
        run: |
          echo "${{ jobs.analyze.outputs.result }}" | jq -r '
            "Type: \(.file_type)", "Language: \(.language)",
            "Complexity: \(.complexity)", "Summary: \(.summary)"'
```

### 3. Parallel Code Review

Lint, diff, and structure analysis run concurrently, then an agent synthesizes findings into a structured report.

From [`workflows/dev/code-review.yml`](workflows/dev/code-review.yml):

```yaml
agents:
  reviewer:
    system_prompt: |
      You are a senior code reviewer. Focus on bugs, security,
      performance, and maintainability. Skip stylistic nitpicks.

jobs:
  lint:
    steps:
      - uses: shell
        run: |
          # auto-detects Go/JS/Python and runs appropriate linter
          ...
    outputs:
      result: ${{ steps.run.outputs.stdout }}

  diff:
    steps:
      - uses: shell
        run: git diff ${{ inputs.base }}...HEAD | head -300
    outputs:
      result: ${{ steps.run.outputs.stdout }}

  structure:
    steps:
      - uses: shell
        run: find . -maxdepth 3 -not -path './.git/*' | head -80
    outputs:
      result: ${{ steps.run.outputs.stdout }}

  review:
    needs: [lint, diff, structure]
    steps:
      - uses: agent
        with:
          agent: reviewer
          prompt: |
            Review this codebase:
            ${{ jobs.lint.outputs.result }}
            ${{ jobs.diff.outputs.result }}
            ${{ jobs.structure.outputs.result }}
          schema:
            type: object
            properties:
              summary: { type: string }
              findings:
                type: array
                items:
                  type: object
                  properties:
                    severity: { type: string, enum: [critical, warning, info] }
                    category: { type: string }
                    file: { type: string }
                    message: { type: string }
              recommendation: { type: string }
            required: [summary, findings, recommendation]
```

## Workflow Library

| Category | What's inside |
|----------|---------------|
| [getting-started](workflows/getting-started/) | Hello world, parallel DAG, conditionals, agent pipeline |
| [dev](workflows/dev/) | Code review, dependency audit, release notes, repo health |
| [ops](workflows/ops/) | Health checks, incident triage, secrets scan |
| [data](workflows/data/) | Knowledge base Q&A, CSV analysis, JSON transform, log analysis |
| [net](workflows/net/) | API smoke tests, cert checks, DNS, uptime monitoring |
| [service](workflows/service/) | Chat API, MCP tools, webhook relay |

## Key Features

- **DAG scheduling** — jobs run in parallel by default; `needs` declares dependencies
- **Three worker types** — `shell`, `agent`, `http`
- **Structured agent output** — JSON schema on agent steps via `with.schema`
- **Expressions** — `${{ ... }}` for data flow between steps and jobs
- **Triggers** — CLI (default), cron schedule (`on.schedule`), HTTP service (`on.service.http`), MCP service (`on.service.mcp`)
- **Skills** — Markdown-based domain knowledge, late-bound into agent context
- **MCP integration** — connect external tool servers; expose workflow tools via MCP
- **Daemonless** — each run is a standalone OS process
- **Process management** — `reed ps`, `reed status`, `reed logs -f`, `reed stop`

## Architecture

Reed follows a 3-tier execution model: **Process > Run > StepRun**.

- **Process** — OS process boundary. Has a PID, Unix socket for IPC, and a DB registration. `reed ps` lists them.
- **Run** — One execution of a workflow. Manages the DAG scheduler, expression rendering, and job dispatch.
- **StepRun** — A single step execution. Dispatched to a worker (shell, agent, or HTTP).

```text
[CLI / Trigger] → [Workflow Loader] → [Engine (DAG Scheduler)]
                                            │
                         ┌──────────────────┼──────────────────┐
                         ▼                  ▼                  ▼
                    [Shell Worker]    [Agent Worker]     [HTTP Worker]
```

## Documentation

- [Architecture & Internals](docs/architecture.md)
- [Workflow DSL](docs/workflow/dsl.md)
- [CLI Reference](docs/cli/commands.md)
- [Agent & AI Providers](docs/agent/ai-providers.md)

## Security

- Agents are restricted to the workspace directory by default (`tool_access: workdir`).
- Every tool call, input, and output is logged to process-level event logs.
- `SIGTERM → context cancel → 3s grace → exit` ensures final states are captured.

## License

[Apache License 2.0](LICENSE)
