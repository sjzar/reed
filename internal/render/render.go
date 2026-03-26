package render

import (
	"regexp"
	"strings"
)

// evalFunc is the signature for expression evaluation functions.
type evalFunc func(expression string, ctx map[string]any) (any, error)

// delimRe matches ${{ ... }} expressions.
var delimRe = regexp.MustCompile(`\$\{\{(.+?)\}\}`)

// exactRe matches a string that is exactly one ${{ ... }} with nothing else.
var exactRe = regexp.MustCompile(`^\$\{\{(.+?)\}\}$`)

// RenderSafe is like Render but treats nil-member-access as nil instead of
// returning an error. Used for `if` condition evaluation to match GitHub
// Actions behavior: accessing a property of nil returns empty/falsy.
func RenderSafe(template string, ctx map[string]any) (any, error) {
	return renderWith(template, ctx, evalExprSafe)
}

// Render evaluates a template string against the given context.
// Three modes per RENDERING-ENGINE-SPEC.md:
//  1. Raw Literal — no ${{ }}, returned as-is
//  2. Exact Expression — entire value is a single ${{ expr }}, returns native Go type
//  3. String Interpolation — mixed literal + ${{ }}, returns string
func Render(template string, ctx map[string]any) (any, error) {
	return renderWith(template, ctx, evalExpr)
}

func renderWith(template string, ctx map[string]any, eval evalFunc) (any, error) {
	// Handle escape: \${{ -> literal ${{
	if strings.Contains(template, `\${{`) {
		template = strings.ReplaceAll(template, `\${{`, "\x00ESCAPED\x00")
		result, err := renderInner(template, ctx, eval)
		if err != nil {
			return nil, err
		}
		if s, ok := result.(string); ok {
			return strings.ReplaceAll(s, "\x00ESCAPED\x00", "${{"), nil
		}
		return result, nil
	}
	return renderInner(template, ctx, eval)
}

func renderInner(template string, ctx map[string]any, eval evalFunc) (any, error) {
	// Mode 1: Raw Literal
	if !delimRe.MatchString(template) {
		return template, nil
	}

	// Mode 2: Exact Expression (typed passthrough)
	if m := exactRe.FindStringSubmatch(template); m != nil {
		return eval(strings.TrimSpace(m[1]), ctx)
	}

	// Mode 3: String Interpolation
	var evalErr error
	result := delimRe.ReplaceAllStringFunc(template, func(match string) string {
		if evalErr != nil {
			return ""
		}
		inner := delimRe.FindStringSubmatch(match)
		val, err := eval(strings.TrimSpace(inner[1]), ctx)
		if err != nil {
			evalErr = err
			return ""
		}
		return scalarToString(val)
	})
	if evalErr != nil {
		return nil, evalErr
	}
	return result, nil
}

// IsTruthy determines if a rendered expression value is truthy.
// Used by the engine to evaluate `if` conditions on steps.
func IsTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != "" && val != "false" && val != "0"
	case int:
		return val != 0
	case int64:
		return val != 0
	case float64:
		return val != 0
	default:
		return true
	}
}
