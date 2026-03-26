package render

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/expr-lang/expr"
)

// evalExpr compiles and runs an expr expression with the reed stdlib.
func evalExpr(expression string, ctx map[string]any) (any, error) {
	env := mergeEnv(ctx, stdlibFuncs())
	program, err := expr.Compile(expression, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("compile %q: %w", expression, err)
	}
	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("eval %q: %w", expression, err)
	}
	return result, nil
}

// evalExprSafe is like evalExpr but treats nil-member-access errors as nil
// instead of returning an error. This matches GitHub Actions behavior where
// accessing a property of a nil/undefined value returns empty string.
func evalExprSafe(expression string, ctx map[string]any) (any, error) {
	result, err := evalExpr(expression, ctx)
	if err != nil && isNilAccessError(err) {
		return nil, nil
	}
	return result, err
}

// isNilAccessError checks if an error is caused by accessing a member on nil.
// expr-lang produces errors like: `eval "...": cannot fetch X from <nil>`.
func isNilAccessError(err error) bool {
	return strings.Contains(err.Error(), "from <nil>")
}

// scalarToString converts scalar values to string for interpolation.
// Non-scalar types (map, slice) cause a panic per spec — caller must
// use toJson() or join() explicitly.
func scalarToString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int, int64, float64:
		return fmt.Sprintf("%v", val)
	case bool:
		return fmt.Sprintf("%v", val)
	case nil:
		return ""
	default:
		// Per spec: complex objects must not be implicitly stringified
		return fmt.Sprintf("<%T>", val)
	}
}

// mergeEnv merges user context with stdlib functions.
func mergeEnv(ctx map[string]any, stdlib map[string]any) map[string]any {
	env := make(map[string]any, len(ctx)+len(stdlib))
	for k, v := range ctx {
		env[k] = v
	}
	for k, v := range stdlib {
		env[k] = v
	}
	return env
}

// stdlibFuncs returns the reed stdlib function set per RENDERING-ENGINE-SPEC.md §9.
func stdlibFuncs() map[string]any {
	return map[string]any{
		"trim":        strings.TrimSpace,
		"upper":       strings.ToUpper,
		"lower":       strings.ToLower,
		"split":       strings.Split,
		"join":        strings.Join,
		"toJson":      toJSON,
		"fromJson":    fromJSON,
		"sha256":      sha256Hex,
		"now":         func() string { return time.Now().Format(time.RFC3339) },
		"default":     defaultVal,
		"defaultVal":  defaultVal, // alias for backward compatibility
		"yesterday":   yesterday,
		"beginningOf": beginningOf,
		"format":      formatTime,
	}
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fromJSON(s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

func defaultVal(value, fallback any) any {
	if value == nil {
		return fallback
	}
	if s, ok := value.(string); ok && s == "" {
		return fallback
	}
	return value
}

// yesterday returns yesterday's date as an RFC3339 string.
func yesterday() string {
	return time.Now().AddDate(0, 0, -1).Format(time.RFC3339)
}

// beginningOf truncates a time string to the start of the given unit.
// Supported units: "day", "month", "year".
func beginningOf(t string, unit string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return "", fmt.Errorf("beginningOf: parse time %q: %w", t, err)
	}
	loc := parsed.Location()
	switch unit {
	case "day":
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	case "month":
		parsed = time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, loc)
	case "year":
		parsed = time.Date(parsed.Year(), 1, 1, 0, 0, 0, 0, loc)
	default:
		return "", fmt.Errorf("beginningOf: unsupported unit %q (use day, month, or year)", unit)
	}
	return parsed.Format(time.RFC3339), nil
}

// formatTime formats a time string using a Go time layout.
func formatTime(t string, layout string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return "", fmt.Errorf("format: parse time %q: %w", t, err)
	}
	return parsed.Format(layout), nil
}
