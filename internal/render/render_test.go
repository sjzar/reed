package render

import (
	"strings"
	"testing"
	"time"
)

func TestRender_RawLiteral(t *testing.T) {
	result, err := Render("hello world", nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "hello world" {
		t.Errorf("result = %v, want hello world", result)
	}
}

func TestRender_ExactExpression_Typed(t *testing.T) {
	ctx := map[string]any{"x": 42}
	result, err := Render("${{ x }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %v (%T), want 42 (int)", result, result)
	}
}

func TestRender_ExactExpression_Bool(t *testing.T) {
	ctx := map[string]any{"debug": true}
	result, err := Render("${{ debug }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != true {
		t.Errorf("result = %v, want true", result)
	}
}

func TestRender_StringInterpolation(t *testing.T) {
	ctx := map[string]any{"name": "reed", "version": "1.0"}
	result, err := Render("app=${{ name }}-v${{ version }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "app=reed-v1.0" {
		t.Errorf("result = %v", result)
	}
}

func TestRender_Arithmetic(t *testing.T) {
	ctx := map[string]any{"a": 10, "b": 3}
	result, err := Render("${{ a + b }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != 13 {
		t.Errorf("result = %v, want 13", result)
	}
}

func TestRender_StdlibTrim(t *testing.T) {
	ctx := map[string]any{"s": "  hello  "}
	result, err := Render("${{ trim(s) }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "hello" {
		t.Errorf("result = %q, want hello", result)
	}
}

func TestRender_StdlibUpper(t *testing.T) {
	ctx := map[string]any{"s": "hello"}
	result, err := Render("${{ upper(s) }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "HELLO" {
		t.Errorf("result = %q", result)
	}
}

func TestRender_StdlibSha256(t *testing.T) {
	result, err := Render(`${{ sha256("test") }}`, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s, ok := result.(string)
	if !ok || len(s) != 64 {
		t.Errorf("sha256 result = %v (len=%d)", result, len(s))
	}
}

func TestRender_StdlibDefault(t *testing.T) {
	ctx := map[string]any{"x": nil}
	result, err := Render(`${{ defaultVal(x, "fallback") }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "fallback" {
		t.Errorf("result = %v, want fallback", result)
	}
}

func TestRender_EscapedDelimiter(t *testing.T) {
	result, err := Render(`\${{ not_expr }}`, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "${{ not_expr }}" {
		t.Errorf("result = %q", result)
	}
}

func TestRender_UndefinedVar(t *testing.T) {
	_, err := Render("${{ undefined_var }}", nil)
	if err == nil {
		t.Error("expected error for undefined variable")
	}
}

func TestRender_NestedContext(t *testing.T) {
	ctx := map[string]any{
		"env": map[string]any{"HOME": "/home/user"},
	}
	result, err := Render(`${{ env.HOME }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "/home/user" {
		t.Errorf("result = %v", result)
	}
}

// --- stdlib: lower ---

func TestRender_StdlibLower(t *testing.T) {
	ctx := map[string]any{"s": "HELLO"}
	result, err := Render("${{ lower(s) }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "hello" {
		t.Errorf("result = %q, want hello", result)
	}
}

// --- stdlib: split / join ---

func TestRender_StdlibSplit(t *testing.T) {
	// split returns []string, which is a non-scalar; exact-expression mode returns it typed.
	ctx := map[string]any{"s": "a,b,c"}
	result, err := Render(`${{ split(s, ",") }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	parts, ok := result.([]string)
	if !ok {
		t.Fatalf("result type = %T, want []string", result)
	}
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Errorf("result = %v", parts)
	}
}

func TestRender_StdlibJoin(t *testing.T) {
	ctx := map[string]any{"parts": []string{"x", "y", "z"}}
	result, err := Render(`${{ join(parts, "-") }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "x-y-z" {
		t.Errorf("result = %q, want x-y-z", result)
	}
}

// --- stdlib: toJson / fromJson ---

func TestRender_StdlibToJson(t *testing.T) {
	ctx := map[string]any{"m": map[string]any{"k": "v"}}
	result, err := Render(`${{ toJson(m) }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("result type = %T, want string", result)
	}
	if !strings.Contains(s, `"k"`) || !strings.Contains(s, `"v"`) {
		t.Errorf("toJson result = %q, missing expected keys", s)
	}
}

func TestRender_StdlibFromJson(t *testing.T) {
	result, err := Render(`${{ fromJson("{\"a\":1}") }}`, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if m["a"] != float64(1) {
		t.Errorf("m[\"a\"] = %v, want 1", m["a"])
	}
}

func TestRender_StdlibFromJson_Invalid(t *testing.T) {
	_, err := Render(`${{ fromJson("not-json") }}`, nil)
	if err == nil {
		t.Error("expected error for invalid JSON input")
	}
}

// --- stdlib: now ---

func TestRender_StdlibNow(t *testing.T) {
	before := time.Now().Add(-2 * time.Second)
	result, err := Render(`${{ now() }}`, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("result type = %T, want string", result)
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("now() result %q is not RFC3339: %v", s, err)
	}
	if parsed.Before(before) {
		t.Errorf("now() = %v, which is before test start %v", parsed, before)
	}
}

// --- stdlib: defaultVal with non-nil non-empty value ---

func TestRender_StdlibDefault_NonNilValue(t *testing.T) {
	ctx := map[string]any{"x": "actual"}
	result, err := Render(`${{ defaultVal(x, "fallback") }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "actual" {
		t.Errorf("result = %v, want actual", result)
	}
}

func TestRender_StdlibDefault_EmptyString(t *testing.T) {
	ctx := map[string]any{"x": ""}
	result, err := Render(`${{ defaultVal(x, "fallback") }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "fallback" {
		t.Errorf("result = %v, want fallback", result)
	}
}

// --- scalarToString coverage ---

func TestRender_Interpolation_IntValue(t *testing.T) {
	ctx := map[string]any{"n": 7}
	result, err := Render("val=${{ n }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "val=7" {
		t.Errorf("result = %q, want val=7", result)
	}
}

func TestRender_Interpolation_Float64Value(t *testing.T) {
	ctx := map[string]any{"f": float64(3.14)}
	result, err := Render("f=${{ f }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	s, ok := result.(string)
	if !ok || !strings.HasPrefix(s, "f=3.14") {
		t.Errorf("result = %q", result)
	}
}

func TestRender_Interpolation_BoolValue(t *testing.T) {
	ctx := map[string]any{"flag": true}
	result, err := Render("flag=${{ flag }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "flag=true" {
		t.Errorf("result = %q, want flag=true", result)
	}
}

func TestRender_Interpolation_NilValue(t *testing.T) {
	ctx := map[string]any{"x": nil}
	result, err := Render("x=${{ x }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "x=" {
		t.Errorf("result = %q, want x=", result)
	}
}

// --- String interpolation eval error path ---

func TestRender_StringInterpolation_EvalError(t *testing.T) {
	// Mixed literal + expression where the expression fails — exercises evalErr path.
	_, err := Render("prefix-${{ undefined_var }}-suffix", nil)
	if err == nil {
		t.Error("expected error for undefined variable in interpolation")
	}
}

// --- RenderSafe: nil-member-access tolerance ---

func TestRenderSafe_NilMemberAccess(t *testing.T) {
	// Accessing .intent on a nil value should return nil (not error).
	ctx := map[string]any{
		"jobs": map[string]any{
			"classify_intent": map[string]any{
				"outputs": map[string]any{
					"intent_json": nil,
				},
			},
		},
	}
	result, err := RenderSafe(`${{ jobs.classify_intent.outputs.intent_json.intent == 'qa' }}`, ctx)
	if err != nil {
		t.Fatalf("RenderSafe should not error on nil member access: %v", err)
	}
	// nil == 'qa' → nil (falsy)
	if result != nil && result != false {
		t.Errorf("result = %v (%T), want nil or false", result, result)
	}
}

func TestRenderSafe_ValidExpression(t *testing.T) {
	// Normal expressions should work as before.
	ctx := map[string]any{"x": 42}
	result, err := RenderSafe("${{ x }}", ctx)
	if err != nil {
		t.Fatalf("RenderSafe: %v", err)
	}
	if result != 42 {
		t.Errorf("result = %v, want 42", result)
	}
}

func TestRenderSafe_DeepNilAccess(t *testing.T) {
	// Accessing a deep path where an intermediate is nil.
	ctx := map[string]any{
		"steps": map[string]any{},
	}
	result, err := RenderSafe(`${{ steps.qa.outputs.output }}`, ctx)
	if err != nil {
		t.Fatalf("RenderSafe should not error on missing step: %v", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestRenderSafe_UndefinedTopLevelVar(t *testing.T) {
	// Undefined top-level variable is NOT a nil-access error — it should
	// still fail even in safe mode (this is a compile-time error, not runtime).
	_, err := RenderSafe(`${{ nonexistent_var }}`, nil)
	if err == nil {
		t.Error("expected error for undefined top-level variable in RenderSafe")
	}
}

func TestRenderSafe_StringInterpolation_NilAccess(t *testing.T) {
	// Nil member access in string interpolation mode should also be safe.
	ctx := map[string]any{
		"data": map[string]any{"nested": nil},
	}
	result, err := RenderSafe(`value=${{ data.nested.field }}`, ctx)
	if err != nil {
		t.Fatalf("RenderSafe interpolation should not error on nil access: %v", err)
	}
	if result != "value=" {
		t.Errorf("result = %q, want value=", result)
	}
}

// --- Escape handling: non-string result after escape replacement ---

func TestRender_EscapedDelimiter_WithOtherExpression(t *testing.T) {
	// Template has both an escape and a real expression — result is string.
	ctx := map[string]any{"name": "world"}
	result, err := Render(`\${{ ignored }} hello ${{ name }}`, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "${{ ignored }} hello world" {
		t.Errorf("result = %q", result)
	}
}
