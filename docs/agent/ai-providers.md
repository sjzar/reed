---
title: AI Provider Configuration
summary: How to configure LLM providers, model routing, failover, and capability overrides.
read_when: Setting up AI access, adding providers, configuring custom endpoints, or debugging model routing.
---

# AI Provider Configuration

Reed supports multiple LLM providers through a unified configuration system. Providers are configured in `~/.reed/reed.json` under the `models` key.

## Quick Setup

The fastest way to get started — set an environment variable:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
# or
export OPENAI_API_KEY=sk-...
```

When no `providers` are configured in `reed.json`, Reed auto-registers providers from these environment variables. This is sufficient for most use cases.

## Configuration File

For persistent or multi-provider setups, configure `~/.reed/reed.json`:

```json
{
  "models": {
    "default": "claude-sonnet-4.6",
    "providers": [
      {
        "id": "anthropic",
        "type": "anthropic-messages",
        "key": "sk-ant-..."
      }
    ]
  }
}
```

### `models` Fields

| Field | Type | Description |
|-------|------|-------------|
| `default` | string | Model reference used when a workflow doesn't specify one. |
| `providers` | array | List of provider configurations. |

### Provider Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | yes | Unique identifier (e.g., `"anthropic"`, `"openai"`, `"my-proxy"`). |
| `type` | string | yes | Handler type. See [Supported Types](#supported-types). |
| `key` | string | no | API key. If empty, auto-filled from environment variable for official endpoints. |
| `base_url` | string | no | Custom API endpoint. Omit to use the official API. |
| `disabled` | bool | no | Skip this provider during initialization. |
| `headers` | object | no | Custom HTTP headers added to every request. |
| `models` | array | no | Explicit model list. See [Model Fields](#model-fields). |

### Model Fields

Each entry in a provider's `models` array:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Model ID used in workflows (e.g., `"gpt-4o"`, `"fast"`). |
| `name` | string | Display name (not sent to the API). |
| `forward_id` | string | Actual model ID sent to the provider API, if different from `id`. Useful for aliases. |
| `thinking` | bool | Supports extended thinking. Omit to use built-in defaults. |
| `vision` | bool | Supports image input. Omit to use built-in defaults. |
| `context_window` | int | Max context tokens. Omit to use built-in defaults. |
| `max_tokens` | int | Default max output tokens. Omit to use built-in defaults. |
| `streaming` | bool | Supports streaming. Omit to use built-in defaults. |

Omitted capability fields fall back to Reed's built-in model database. You only need to set these when using custom or fine-tuned models that Reed doesn't know about.

## Supported Types

| Type | Protocol | Auto-registered from |
|------|----------|---------------------|
| `anthropic-messages` | Anthropic Messages API | `ANTHROPIC_API_KEY` |
| `openai-completions` | OpenAI Chat Completions API | `OPENAI_API_KEY` |
| `openai-responses` | OpenAI Responses API | *(manual config only)* |

All handlers are streaming-only.

## Examples

### Single Provider (Anthropic)

```json
{
  "models": {
    "default": "claude-sonnet-4-20250514",
    "providers": [
      {
        "id": "anthropic",
        "type": "anthropic-messages",
        "key": "sk-ant-..."
      }
    ]
  }
}
```

### Multiple Providers with Failover

```json
{
  "models": {
    "default": "claude-sonnet-4-20250514,gpt-4o",
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

The comma-separated default (`"claude-sonnet-4-20250514,gpt-4o"`) creates a failover chain — if Anthropic returns an error (rate limit, server error), Reed automatically retries with OpenAI.

### OpenAI-Compatible Proxy

```json
{
  "models": {
    "default": "my-proxy/llama-3-70b",
    "providers": [
      {
        "id": "my-proxy",
        "type": "openai-completions",
        "base_url": "https://my-proxy.example.com/v1",
        "key": "proxy-key-...",
        "models": [
          {
            "id": "llama-3-70b",
            "context_window": 128000,
            "max_tokens": 4096
          }
        ]
      }
    ]
  }
}
```

When using `base_url`, the API key is **not** auto-filled from environment variables. Provide the key explicitly.

### Model Aliases

Use `forward_id` to create short aliases for long model IDs:

```json
{
  "models": [
    {
      "id": "fast",
      "forward_id": "claude-3-5-haiku-20241022"
    },
    {
      "id": "smart",
      "forward_id": "claude-opus-4-20250514"
    }
  ]
}
```

Then reference `fast` or `smart` in workflows instead of the full model ID.

## Model Routing

When a workflow references a model (e.g., `model: gpt-4o`), Reed resolves it in this order:

1. **Explicit provider** — `openai/gpt-4o` routes directly to the `openai` provider.
2. **Model index** — bare `gpt-4o` matches any provider that lists it in its `models` array.
3. **Wildcard fallback** — env-auto-registered providers accept any model ID. Disambiguation uses family prefixes: `gpt-`/`o1`/`o3`/`o4` route to `openai-completions`, `claude-` routes to `anthropic-messages`.

If a model reference is ambiguous across multiple wildcard providers, use the explicit `provider/model` syntax.

## Auto Key Injection

Reed fills empty `key` fields from environment variables under these conditions:

- The provider type has a known env var (`anthropic-messages` → `ANTHROPIC_API_KEY`, `openai-completions` → `OPENAI_API_KEY`).
- The `base_url` is either empty (official API) or matches the official host (`api.anthropic.com`, `api.openai.com`).

Custom `base_url` endpoints never receive auto-injected keys. This prevents credential leakage to third-party proxies.

## Failover

Reed's failover system handles transient errors automatically:

| Error | Behavior | Cooldown |
|-------|----------|----------|
| Rate limit (429) | Try next provider | 60s |
| Server error (5xx) | Try next provider | 60s |
| Network error | Try next provider | 60s |
| Auth error (401/403) | Try next provider | 1 hour |
| Bad request (400) | Stop immediately | — |
| Context overflow | Stop immediately | — |

Failover chains are specified with comma-separated model references (e.g., `"claude-sonnet-4-20250514,gpt-4o"`). Cooldown penalties are scoped per provider+model pair, not per provider.

Before dispatching, Reed also checks whether the request's estimated token count fits within 80% of the candidate's context window. If no candidate can fit the request, it returns a context overflow error.
