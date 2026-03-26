---
title: Config Resolution
summary: Workflow config merge pipeline from base file through CLI flags
read_when: understanding how --set-file, --set, --set-string, --env flags transform a workflow
---

# Config Resolution

## Pipeline

Implemented in `workflow.PrepareWorkflow()` and `LoadAndResolve()` (`internal/workflow/resolve.go`):

```
Base Workflow -> --set-file (RFC 7386) -> --set (includes --env) -> --set-string -> parse -> validate
```

The `--env K=V` flag is syntactic sugar: it is appended to the `--set` list as `env.K=V` before processing. Since `--env` entries are appended after `--set` entries, `--env` wins over `--set` for the same key (last wins). `--set-string` runs as a separate stage after both and can override either.

## Stage 1: Load Base

`LoadBase()` in `internal/workflow/loader.go` accepts:
- Local file path (resolved to absolute via `filepath.Abs`, read via `os.ReadFile`)
- HTTP/HTTPS URL (fetched with 30s timeout, 10MB body limit; non-200 responses are rejected)
- Registry references (containing `@`) are rejected

Returns a `RawWorkflow` (`map[string]any`) from YAML unmarshaling. Empty or nil documents are rejected.

## Stage 2: --set-file (RFC 7386 Merge)

`configpatch.MergeRFC7386()` in `pkg/configpatch/merge.go`. Applied in order for each `--set-file` path (local files only, URLs rejected by `LoadSetFile`).

Rules:
- **Map + Map**: deep recursive merge (same key overrides, new key appends)
- **Array**: wholesale replacement (not element-wise merge)
- **Scalar**: last wins
- **null**: tombstone deletion (key removed from result)

`configpatch.MergeAll()` applies a sequence of patches to a base.

## Stage 3: --set (Type-Inferred Path Assignment)

`configpatch.ApplySet()` in `pkg/configpatch/set.go`. Each flag value is a comma-separated list of `key=value` pairs.

**Path syntax:** Dot-separated with optional bracket array indexing. Examples: `env.FOO=bar`, `jobs.build.steps[0].timeout=60`. Auto-vivifies intermediate maps. Maximum nesting depth: 30. Maximum array index: 65536. Negative array indices are rejected. Keys are trimmed of whitespace; values are not.

Sparse array assignment creates nil gaps: `--set list[3]=val` on an empty list produces `[nil, nil, nil, "val"]`.

**Type inference** (in `parseValue`):
- `true`/`false` → bool
- `null`/`~` → tombstone (deletes the key; for array elements, sets to nil instead of removing)
- Integer string → int (e.g., `"42"`, `"-5"`, `"0"`)
- Float string → float64 (e.g., `"3.14"`, `"0.5"`)
- Leading-zero strings → string (e.g., `"007"`, `"00009"`) — except `"0"` (→ int) and `"0.x"` (→ float)
- Everything else → string

**Array literals:** `{a,b,c}` syntax creates a `[]any`. Commas inside braces are element separators, not pair separators. Backslash-escaped commas (`\,`) are preserved as literal commas. Empty braces `{}` produce an empty array. Each element is type-inferred independently.

## Stage 4: --set-string (Forced String)

`configpatch.ApplySetString()` in `pkg/configpatch/set.go`. Same path parsing as `--set` but all values are forced to string (no type inference). The `forceString` flag bypasses `parseValue` logic.

Array literal syntax `{a,b,c}` is still supported — elements are forced to strings (e.g., `--set-string arr={1,true,null}` produces `["1", "true", "null"]`).

## Stage 5: Parse and Validate

After all patches are applied, `ParseRaw()` converts the `RawWorkflow` to a typed `*model.Workflow` via YAML round-trip (marshal then unmarshal using struct tags). `postProcess` applies defaults and DSL sugar (see [DSL — PostProcess Defaults](dsl.md#postprocess-defaults)). `Validate()` performs structural checks (see [DSL — Validation Summary](dsl.md#validation-summary)). The base source path is stored in `wf.Source` for tracking.

## --env Sugar

In `PrepareWorkflow()`, `--env K=V` flags are converted to `--set env.K=V` entries and appended to the `--set` list before calling `LoadAndResolve`. Since they are appended after explicit `--set` values, an explicit `--set env.FOO=x` followed by `--env FOO=y` results in `FOO=y` (last wins).
