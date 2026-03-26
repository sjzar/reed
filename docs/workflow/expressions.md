---
title: Expressions
summary: Expression syntax, rendering modes, type rules, stdlib functions
read_when: using ${{ }} expressions in workflows or working on the render package
---

# Expressions

## Syntax

All expressions use `${{ ... }}` delimiters. Implemented in `internal/render/render.go` using the `expr-lang/expr` library for evaluation.

Escape sequence: `\${{` produces a literal `${{` in the output.

## Three Rendering Modes

`Render()` selects the mode automatically based on the template string:

**1. Raw Literal** -- No `${{ }}` present. The string is returned as-is (type: string).

**2. Exact Expression** -- The entire value is a single `${{ expr }}` with nothing else. Returns the native Go type from the expression (bool, int, float64, map, slice, etc.). Matched by `exactRe`: `^\$\{\{(.+?)\}\}$`.

**3. String Interpolation** -- Mixed literal text and `${{ }}` fragments. Each expression result is converted to a string via `scalarToString` and concatenated. Return type is always string.

## Type Rules

In Exact Expression mode, the expression result passes through with its native type.

In String Interpolation mode, `scalarToString` converts:
- `string` -> as-is
- `int`, `int64`, `float64` -> `fmt.Sprintf("%v", val)`
- `bool` -> `"true"` or `"false"`
- `nil` -> `""`
- Complex types (map, slice) -> `"<type>"` placeholder (use `toJson()` or `join()` explicitly)

## Stdlib Functions

Defined in `stdlibFuncs()` in `internal/render/stdlib.go`:

| Function | Signature | Description |
|---|---|---|
| `trim` | `trim(s string) string` | `strings.TrimSpace` |
| `upper` | `upper(s string) string` | `strings.ToUpper` |
| `lower` | `lower(s string) string` | `strings.ToLower` |
| `split` | `split(s, sep string) []string` | `strings.Split` |
| `join` | `join(elems []string, sep string) string` | `strings.Join` |
| `toJson` | `toJson(v any) (string, error)` | JSON marshal |
| `fromJson` | `fromJson(s string) (any, error)` | JSON unmarshal |
| `sha256` | `sha256(s string) string` | SHA-256 hex digest |
| `now` | `now() string` | Current time as RFC3339 |
| `default` | `default(value, fallback any) any` | Returns fallback if value is nil or empty string |
| `defaultVal` | `defaultVal(value, fallback any) any` | Alias for `default` |
| `yesterday` | `yesterday() string` | Yesterday's date as RFC3339 |
| `beginningOf` | `beginningOf(t, unit string) (string, error)` | Truncate time to start of `day`, `month`, or `year` |
| `format` | `format(t, layout string) (string, error)` | Format RFC3339 time using Go time layout |

## Expression Engine

Expressions are compiled and evaluated using `expr-lang/expr`. The environment is built by merging the user-provided context (inputs, env, jobs, steps, secrets, etc.) with the stdlib functions. Stdlib keys override context keys if there is a collision.

Only the engine owner loop evaluates expressions. Workers never evaluate expressions.
