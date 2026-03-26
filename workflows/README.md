# Reed Workflow Library

A curated collection of ready-to-use workflows for the Reed workflow runtime. The library is built around the **"skeleton + agent" pattern** — shell steps handle deterministic collection and formatting, while agent steps make decisions with structured output. This 1+1>2 approach gives you repeatable pipelines with AI judgment at the decision points.

## Quick Start

```bash
# Simplest workflow — no LLM needed
reed run workflows/getting-started/hello.yml -i name=World

# The skeleton + agent pattern (tutorial)
reed run workflows/getting-started/agent-pipeline.yml -i file=go.mod

# Parallel fan-out/fan-in DAG
reed run workflows/getting-started/parallel.yml

# Full code review: parallel lint/diff/structure → agent → report
reed run workflows/dev/code-review.yml -i directory=. -i base=main
```

## Prerequisites

- **Shell workflows**: Only standard Unix tools (`bash`, `curl`, `jq`, `openssl`, `git`, etc.)
- **Agent workflows**: A configured LLM provider in `~/.reed/reed.json`. Agent workflows use the `models.default` model — no model is hardcoded in the YAML. Override per-run with `--env REED_DEFAULT_MODEL=your-model`.

## When to Use Each Pattern

| Pattern | Use When | Example |
|---------|----------|---------|
| **Shell-only** | Deterministic, no judgment needed | Health checks, cert expiry, disk usage |
| **Shell → Agent → Shell** | Data needs classification or analysis | Log analysis, CSV insights, code review |
| **Agent → Shell** | Agent generates a plan, shell executes it | JSON transform (agent writes jq filter) |
| **Parallel + Agent fan-in** | Multiple data sources need unified assessment | Incident triage, scheduled health alerts |
| **Service** | Long-running HTTP/MCP endpoint | Chat API, webhook relay |

The **skeleton + agent** pattern (shell→agent→shell) is the sweet spot: shell collects data cheaply, agent analyzes with structured JSON output (`with.schema`), shell formats deterministically. This avoids context bloat while getting AI judgment where it matters.

## Workflow Inventory

### getting-started/ — Onboarding & Tutorials

| Workflow | Type | Description |
|----------|------|-------------|
| [hello.yml](getting-started/hello.yml) | shell | Echo greeting + date — simplest possible workflow |
| [multi-step.yml](getting-started/multi-step.yml) | shell→agent→shell | System info → agent analysis → report — DAG + output passing |
| [parallel.yml](getting-started/parallel.yml) | shell (parallel) | Three concurrent checks → fan-in summary — parallel DAG |
| [conditional.yml](getting-started/conditional.yml) | shell | Steps skip/run based on `if:` expressions — conditional execution |
| [http-request.yml](getting-started/http-request.yml) | http→shell | Fetch JSON API with `uses: http`, process with jq — HTTP worker |
| [schedule.yml](getting-started/schedule.yml) | shell (cron) | Heartbeat on `on.schedule` — cron trigger |
| [agent-pipeline.yml](getting-started/agent-pipeline.yml) | shell→agent→shell | **Tutorial**: the skeleton + agent pattern with structured output |

### dev/ — Software Development

| Workflow | Type | Description |
|----------|------|-------------|
| [repo-health.yml](dev/repo-health.yml) | shell | Run format/lint/test/build checks (auto-detects language) |
| [dependency-audit.yml](dev/dependency-audit.yml) | shell | Check outdated and vulnerable deps across package managers |
| [code-review.yml](dev/code-review.yml) | parallel→agent→shell | Parallel lint/diff/structure → agent review with structured findings |
| [release-notes.yml](dev/release-notes.yml) | shell→agent→shell | Git log → agent categorizes → markdown release notes |

### data/ — Data Processing & Analysis

| Workflow | Type | Description |
|----------|------|-------------|
| [log-analyze.yml](data/log-analyze.yml) | shell→agent | Extract log patterns, then agent identifies root causes |
| [json-transform.yml](data/json-transform.yml) | agent→shell | Agent generates jq expression, shell applies the transform |
| [csv-insight.yml](data/csv-insight.yml) | shell→agent→shell | CSV schema + stats → agent analysis → structured report |

### ops/ — System Admin & Monitoring

| Workflow | Type | Description |
|----------|------|-------------|
| [health-check.yml](ops/health-check.yml) | shell | Disk, memory, load, optional URL checks |
| [disk-usage-report.yml](ops/disk-usage-report.yml) | shell | Report largest dirs/files and disk usage |
| [env-doctor.yml](ops/env-doctor.yml) | shell | Verify required CLIs, versions, env vars for a project |
| [secrets-scan.yml](ops/secrets-scan.yml) | shell | Scan repo/folder for accidentally committed secrets |
| [scheduled-health-alert.yml](ops/scheduled-health-alert.yml) | parallel→agent→http (cron) | Schedule + parallel probes + agent triage + conditional alerting |
| [incident-triage.yml](ops/incident-triage.yml) | parallel→agent→http (cron) | **Flagship** — logs/metrics/services → agent triage → conditional alert |

### net/ — Network & Connectivity

| Workflow | Type | Description |
|----------|------|-------------|
| [api-smoke-test.yml](net/api-smoke-test.yml) | shell | Hit endpoints, assert status/body, emit pass/fail report |
| [cert-expiry-check.yml](net/cert-expiry-check.yml) | shell | Check TLS certificate expiration for a list of domains |
| [uptime-probe.yml](net/uptime-probe.yml) | shell | Probe multiple URLs, record latency/status |
| [url-change-watch.yml](net/url-change-watch.yml) | shell | Fetch URL, diff against last snapshot, detect changes |
| [dns-diagnose.yml](net/dns-diagnose.yml) | shell | Resolve DNS records and check propagation |

### service/ — Long-Running Services

| Workflow | Type | Description |
|----------|------|-------------|
| [chat-api.yml](service/chat-api.yml) | agent (service) | HTTP chat API with persistent sessions (port 8400) |
| [webhook-relay.yml](service/webhook-relay.yml) | http+shell (service) | Receive webhook, validate, transform, forward (port 8401) |
| [mcp-tools.yml](service/mcp-tools.yml) | shell (service/mcp) | Expose file/search/command tools via MCP (port 8402) |

## Feature Showcase

| Feature | Workflows |
|---------|-----------|
| **Structured output** (`with.schema`) | `agent-pipeline.yml`, `code-review.yml`, `release-notes.yml`, `csv-insight.yml`, `incident-triage.yml`, `scheduled-health-alert.yml` |
| **Parallel DAG** (fan-out/fan-in) | `parallel.yml`, `code-review.yml`, `incident-triage.yml`, `scheduled-health-alert.yml` |
| **Conditional execution** (`if:`) | `conditional.yml`, `incident-triage.yml`, `scheduled-health-alert.yml` |
| **HTTP worker** (`uses: http`) | `http-request.yml`, `scheduled-health-alert.yml`, `incident-triage.yml`, `webhook-relay.yml` |
| **Schedule trigger** (`on.schedule`) | `schedule.yml`, `scheduled-health-alert.yml`, `incident-triage.yml` |
| **Service mode** (`on.service`) | `chat-api.yml`, `webhook-relay.yml`, `mcp-tools.yml` |
| **Output passing** (cross-job) | `multi-step.yml`, `parallel.yml`, `agent-pipeline.yml`, `code-review.yml`, `release-notes.yml` |
| **Agent + shell pipeline** | `multi-step.yml`, `agent-pipeline.yml`, `log-analyze.yml`, `json-transform.yml`, `code-review.yml`, `release-notes.yml`, `csv-insight.yml` |

## Usage Patterns

### Shell-Only Workflows (no LLM required)

```bash
reed run workflows/ops/health-check.yml
reed run workflows/ops/disk-usage-report.yml -i path=/var/log
reed run workflows/dev/repo-health.yml -i directory=./my-project
reed run workflows/net/cert-expiry-check.yml -i domains=example.com,github.com
```

### Skeleton + Agent Pipelines

```bash
# Analyze a file with structured output
reed run workflows/getting-started/agent-pipeline.yml -i file=go.mod

# Code review with parallel data collection
reed run workflows/dev/code-review.yml -i directory=. -i base=main

# Generate release notes from git history
reed run workflows/dev/release-notes.yml

# Analyze CSV data
reed run workflows/data/csv-insight.yml -i file=data.csv
```

### Scheduled Workflows

```bash
# Validate schedule syntax
reed validate workflows/ops/scheduled-health-alert.yml
reed validate workflows/ops/incident-triage.yml

# Run as a service (schedule fires automatically)
reed run workflows/ops/scheduled-health-alert.yml -d
reed run workflows/ops/incident-triage.yml -d
```

### Service Workflows (long-running)

```bash
# Start chat API as a background service
reed run workflows/service/chat-api.yml -d

# Then interact via HTTP
curl -X POST http://localhost:8400/chat \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello!", "session_id": "user-1"}'
```

### Providing Inputs

```bash
# Single input
reed run workflow.yml -i key=value

# Multiple inputs
reed run workflow.yml -i file=data.csv -i question="Summarize this"

# Override model for a specific run
reed run workflow.yml --env REED_DEFAULT_MODEL=your-model
```

## Design Principles

1. **Skeleton + agent** — shell collects, agent decides, shell acts. Fixed flow + AI at decision points = 1+1>2
2. **Structured output** — agent steps use `with.schema` for deterministic JSON, not free-text
3. **Earn your place** — every workflow provides orchestration value beyond what a raw LLM chat can do
4. **Safe by default** — write-capable workflows default to dry-run/proposal mode
5. **Self-contained** — each workflow is a single YAML file with inline agent definitions
6. **Cross-platform** — shell commands handle macOS/Linux differences

## Statistics

- **Total workflows**: 28
- **Shell-only**: 13 (~45%) — no LLM required
- **Shell + Agent pipeline**: 10 (~34%) — shell gathers, agent analyzes with structured output
- **Service**: 3 (~10%) — long-running HTTP/MCP services
- **Getting-started**: 3 (~10%) — tutorials and onboarding

## Known Notes

- **Trailing newlines**: Shell step `stdout` preserves trailing newlines. Use `${{ trim(steps.X.outputs.stdout) }}` in expressions when needed.
- **Service workflows**: Use `reed run -d` for background execution. Service ports are configurable via workflow-level `on.service.port`.
- **Model resolution**: Agent workflows do not hardcode a model. The AI router uses `models.default` from `~/.reed/reed.json`. Override per-run with `--env REED_DEFAULT_MODEL=your-model`.
- **Schedule workflows**: Use `reed run -d` to start as a daemon. The schedule fires automatically per the cron expression.
- **Structured output**: `with.schema` sends a JSON Schema to the LLM for structured responses. The agent output is valid JSON matching the schema.
