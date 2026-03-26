package configpatch

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestApplySet_Table(t *testing.T) {
	tests := []struct {
		name   string
		raw    map[string]any
		set    string
		expect map[string]any
		err    bool
	}{
		// Basic scalar assignment
		{name: "simple string", raw: map[string]any{}, set: "name1=value1", expect: map[string]any{"name1": "value1"}},
		{name: "comma separated", raw: map[string]any{}, set: "name1=value1,name2=value2", expect: map[string]any{"name1": "value1", "name2": "value2"}},
		{name: "empty value", raw: map[string]any{}, set: "name1=,name2=value2", expect: map[string]any{"name1": "", "name2": "value2"}},
		{name: "value with spaces", raw: map[string]any{}, set: "name1=one two three,name2=three two one", expect: map[string]any{"name1": "one two three", "name2": "three two one"}},

		// Type inference
		{name: "bool true", raw: map[string]any{}, set: "boolean=true", expect: map[string]any{"boolean": true}},
		{name: "bool false", raw: map[string]any{}, set: "boolean=false", expect: map[string]any{"boolean": false}},
		{name: "zero int", raw: map[string]any{}, set: "zero_int=0", expect: map[string]any{"zero_int": 0}},
		{name: "positive int", raw: map[string]any{}, set: "long_int=1234567890", expect: map[string]any{"long_int": 1234567890}},
		{name: "negative int", raw: map[string]any{}, set: "neg=-42", expect: map[string]any{"neg": -42}},
		{name: "float", raw: map[string]any{}, set: "pi=3.14", expect: map[string]any{"pi": 3.14}},
		{name: "leading zeros stay string", raw: map[string]any{}, set: "leading_zeros=00009", expect: map[string]any{"leading_zeros": "00009"}},

		// Null / tombstone
		{name: "null tombstone", raw: map[string]any{"name1": "val"}, set: "name1=null", expect: map[string]any{}},
		{name: "tilde tombstone", raw: map[string]any{"name1": "val"}, set: "name1=~", expect: map[string]any{}},
		{name: "null tombstone nonexistent key", raw: map[string]any{}, set: "ghost=null", expect: map[string]any{}},
		{name: "null with bool and true", raw: map[string]any{}, set: "f=false,t=true", expect: map[string]any{"f": false, "t": true}},

		// Escaped comma
		{name: "escaped comma in value", raw: map[string]any{}, set: `name1=one\,two,name2=three\,four`, expect: map[string]any{"name1": "one,two", "name2": "three,four"}},

		// Dot paths (auto-vivify)
		{name: "single dot path", raw: map[string]any{}, set: "outer.inner=value", expect: map[string]any{"outer": map[string]any{"inner": "value"}}},
		{name: "triple dot path", raw: map[string]any{}, set: "outer.middle.inner=value", expect: map[string]any{"outer": map[string]any{"middle": map[string]any{"inner": "value"}}}},
		{name: "two keys same parent", raw: map[string]any{}, set: "outer.inner1=value,outer.inner2=value2", expect: map[string]any{"outer": map[string]any{"inner1": "value", "inner2": "value2"}}},
		{name: "mixed depth same parent", raw: map[string]any{}, set: "outer.inner1=value,outer.middle.inner=value", expect: map[string]any{
			"outer": map[string]any{
				"inner1": "value",
				"middle": map[string]any{"inner": "value"},
			},
		}},
		{name: "dot path empty value", raw: map[string]any{}, set: "name1.name2=", expect: map[string]any{"name1": map[string]any{"name2": ""}}},

		// Overwrite existing nested
		{
			name: "overwrite nested value",
			raw:  map[string]any{"outer": map[string]any{"inner1": "overwrite", "inner2": "value2"}},
			set:  "outer.inner1=value1,outer.inner3=value3,outer.inner4=4",
			expect: map[string]any{"outer": map[string]any{
				"inner1": "value1",
				"inner2": "value2",
				"inner3": "value3",
				"inner4": 4,
			}},
		},

		// Array literal
		{name: "array literal strings", raw: map[string]any{}, set: "name1={value1,value2}", expect: map[string]any{"name1": []any{"value1", "value2"}}},
		{name: "two array literals", raw: map[string]any{}, set: "name1={value1,value2},name2={value3,value4}", expect: map[string]any{
			"name1": []any{"value1", "value2"},
			"name2": []any{"value3", "value4"},
		}},
		{name: "array literal ints", raw: map[string]any{}, set: "name1={1021,902}", expect: map[string]any{"name1": []any{1021, 902}}},
		{name: "nested array literal", raw: map[string]any{}, set: "name1.name2={value1,value2}", expect: map[string]any{"name1": map[string]any{"name2": []any{"value1", "value2"}}}},
		{name: "empty array literal", raw: map[string]any{}, set: "name1={}", expect: map[string]any{"name1": []any{}}},

		// Bracket index
		{name: "list[0] vivify", raw: map[string]any{}, set: "list[0]=foo", expect: map[string]any{"list": []any{"foo"}}},
		{name: "list[0].foo nested", raw: map[string]any{}, set: "list[0].foo=bar", expect: map[string]any{"list": []any{map[string]any{"foo": "bar"}}}},
		{name: "list[0] two keys same element", raw: map[string]any{}, set: "list[0].foo=bar,list[0].hello=world", expect: map[string]any{
			"list": []any{map[string]any{"foo": "bar", "hello": "world"}},
		}},
		{name: "list[0] and list[1]", raw: map[string]any{}, set: "list[0]=foo,list[1]=bar", expect: map[string]any{"list": []any{"foo", "bar"}}},
		{name: "list sparse (gap fill nil)", raw: map[string]any{}, set: "list[0]=foo,list[3]=bar", expect: map[string]any{"list": []any{"foo", nil, nil, "bar"}}},
		{name: "nested dot then bracket", raw: map[string]any{}, set: "name1.name2[0].foo=bar", expect: map[string]any{
			"name1": map[string]any{
				"name2": []any{map[string]any{"foo": "bar"}},
			},
		}},
		{name: "bracket then dot two elements", raw: map[string]any{}, set: "name1.name2[0].foo=bar,name1.name2[1].foo=baz", expect: map[string]any{
			"name1": map[string]any{
				"name2": []any{
					map[string]any{"foo": "bar"},
					map[string]any{"foo": "baz"},
				},
			},
		}},
		{name: "bracket reverse order", raw: map[string]any{}, set: "name1.name2[1].foo=bar,name1.name2[0].foo=baz", expect: map[string]any{
			"name1": map[string]any{
				"name2": []any{
					map[string]any{"foo": "baz"},
					map[string]any{"foo": "bar"},
				},
			},
		}},
		{name: "bracket gap with nested", raw: map[string]any{}, set: "name1.name2[1].foo=bar", expect: map[string]any{
			"name1": map[string]any{
				"name2": []any{nil, map[string]any{"foo": "bar"}},
			},
		}},

		// Error cases
		{name: "missing equals", raw: map[string]any{}, set: "noequals", err: true},
		{name: "empty key", raw: map[string]any{}, set: "=value", err: true},
		{name: "traverse through scalar", raw: map[string]any{"name": "scalar"}, set: "name.sub=val", err: true},
		{name: "index non-array", raw: map[string]any{"name": "scalar"}, set: "name[0]=val", err: true},
		{name: "negative index", raw: map[string]any{}, set: "list[-1]=foo", err: true},
		{name: "dot then empty segment", raw: map[string]any{}, set: "name1.=name2", err: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := cloneRaw(tt.raw)
			got, err := ApplySet(raw, []string{tt.set})
			if tt.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("got  %v\nwant %v", got, tt.expect)
			}
		})
	}
}

func TestApplySetString_Table(t *testing.T) {
	tests := []struct {
		name   string
		raw    map[string]any
		set    string
		expect map[string]any
	}{
		{name: "int stays string", raw: map[string]any{}, set: "long_int_string=1234567890", expect: map[string]any{"long_int_string": "1234567890"}},
		{name: "bool stays string", raw: map[string]any{}, set: "boolean=true", expect: map[string]any{"boolean": "true"}},
		{name: "null stays string", raw: map[string]any{}, set: "is_null=null", expect: map[string]any{"is_null": "null"}},
		{name: "zero stays string", raw: map[string]any{}, set: "zero=0", expect: map[string]any{"zero": "0"}},
		{
			name: "overwrite nested with string",
			raw:  map[string]any{"outer": map[string]any{"inner1": "overwrite", "inner2": "value2"}},
			set:  "outer.inner1=1,outer.inner3=3",
			expect: map[string]any{"outer": map[string]any{
				"inner1": "1",
				"inner2": "value2",
				"inner3": "3",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := cloneRaw(tt.raw)
			got, err := ApplySetString(raw, []string{tt.set})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("got  %v\nwant %v", got, tt.expect)
			}
		})
	}
}

func TestApplySet_NestedLevelLimit(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= maxNestedLevel+2; i++ {
		fmt.Fprintf(&b, "name%d", i)
		if i <= maxNestedLevel+1 {
			b.WriteString(".")
		}
	}
	path := b.String() + "=value"

	_, err := ApplySet(map[string]any{}, []string{path})
	if err == nil {
		t.Fatal("expected error for excessive nesting")
	}
	if !strings.Contains(err.Error(), "nesting depth") {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify that maxNestedLevel depth is OK
	var ok strings.Builder
	for i := 1; i <= maxNestedLevel; i++ {
		fmt.Fprintf(&ok, "n%d", i)
		if i < maxNestedLevel {
			ok.WriteString(".")
		}
	}
	okPath := ok.String() + "=value"
	_, err = ApplySet(map[string]any{}, []string{okPath})
	if err != nil {
		t.Fatalf("expected success at exactly maxNestedLevel, got: %v", err)
	}
}

func TestApplySet_ArrayIndexLimit(t *testing.T) {
	set := fmt.Sprintf("list[%d]=val", maxArrayIndex+1)
	_, err := ApplySet(map[string]any{}, []string{set})
	if err == nil {
		t.Fatal("expected error for index exceeding max")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestApplySet_MultipleFlags(t *testing.T) {
	raw := map[string]any{}
	raw, err := ApplySet(raw, []string{
		"a.b=1",
		"a.c=2",
		"d=3",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a := raw["a"].(map[string]any)
	if a["b"] != 1 || a["c"] != 2 {
		t.Errorf("a = %v, want {b:1, c:2}", a)
	}
	if raw["d"] != 3 {
		t.Errorf("d = %v, want 3", raw["d"])
	}
}

func TestApplySet_NilRaw(t *testing.T) {
	got, err := ApplySet(nil, []string{"a=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["a"] != 1 {
		t.Errorf("a = %v, want 1", got["a"])
	}
}

func TestApplySetString_NilRaw(t *testing.T) {
	got, err := ApplySetString(nil, []string{"a=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["a"] != "1" {
		t.Errorf("a = %v, want '1' (string)", got["a"])
	}
}

func TestApplySet_EmptySets(t *testing.T) {
	raw := map[string]any{"a": "1"}
	got, err := ApplySet(raw, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["a"] != "1" {
		t.Errorf("a = %v, want 1", got["a"])
	}
}

func TestSplitPairs(t *testing.T) {
	tests := []struct {
		input  string
		expect []string
	}{
		{"a=1,b=2", []string{"a=1", "b=2"}},
		{`a=1\,2,b=3`, []string{"a=1,2", "b=3"}},
		{"a={1,2,3},b=4", []string{"a={1,2,3}", "b=4"}},
		{"a={1,2},b={3,4}", []string{"a={1,2}", "b={3,4}"}},
		{`a=\,\,,b=c`, []string{"a=,,", "b=c"}},
		{"single", []string{"single"}},
		{"", nil},
		{"a=1,", []string{"a=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitPairs(tt.input)
			if !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("splitPairs(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestParseValue(t *testing.T) {
	tests := []struct {
		input       string
		forceString bool
		expect      any
	}{
		{"true", false, true},
		{"false", false, false},
		{"null", false, tombstone{}},
		{"~", false, tombstone{}},
		{"42", false, 42},
		{"-7", false, -7},
		{"3.14", false, 3.14},
		{"hello", false, "hello"},
		{"00009", false, "00009"},
		{"", false, ""},

		// forceString mode
		{"true", true, "true"},
		{"42", true, "42"},
		{"null", true, "null"},
		{"3.14", true, "3.14"},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("%s_force=%v", tt.input, tt.forceString)
		t.Run(name, func(t *testing.T) {
			got := parseValue(tt.input, tt.forceString)
			if !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("parseValue(%q, %v) = %v (%T), want %v (%T)", tt.input, tt.forceString, got, got, tt.expect, tt.expect)
			}
		})
	}
}

func TestParseArrayLiteral(t *testing.T) {
	tests := []struct {
		input       string
		forceString bool
		expect      []any
		ok          bool
	}{
		{"{a,b,c}", false, []any{"a", "b", "c"}, true},
		{"{1,2,3}", false, []any{1, 2, 3}, true},
		{"{true,false}", false, []any{true, false}, true},
		{"{}", false, []any{}, true},
		{"{a}", false, []any{"a"}, true},
		{"not_array", false, nil, false},
		{"{only_open", false, nil, false},
		{"only_close}", false, nil, false},

		// forceString
		{"{1,true,null}", true, []any{"1", "true", "null"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseArrayLiteral(tt.input, tt.forceString)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("got %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestApplySet_NestedArrayIndex(t *testing.T) {
	raw := map[string]any{}
	got, err := ApplySet(raw, []string{"nested[0][0]=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested := got["nested"].([]any)
	inner := nested[0].([]any)
	if inner[0] != 1 {
		t.Errorf("nested[0][0] = %v, want 1", inner[0])
	}
}

func TestApplySet_NestedArraySparse(t *testing.T) {
	raw := map[string]any{}
	got, err := ApplySet(raw, []string{"nested[1][1]=1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nested := got["nested"].([]any)
	if len(nested) != 2 {
		t.Fatalf("nested len = %d, want 2", len(nested))
	}
	if nested[0] != nil {
		t.Errorf("nested[0] = %v, want nil", nested[0])
	}
	inner := nested[1].([]any)
	if len(inner) != 2 || inner[0] != nil || inner[1] != 1 {
		t.Errorf("nested[1] = %v, want [nil, 1]", inner)
	}
}

func TestApplySet_IllegalAfterBracket(t *testing.T) {
	raw := map[string]any{}
	_, err := ApplySet(raw, []string{"illegal[0]name.foo=bar"})
	if err == nil {
		t.Fatal("expected error for text after bracket close")
	}
}

func TestApplySet_ErrorPrefix(t *testing.T) {
	_, err := ApplySet(map[string]any{}, []string{"noequals"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "configpatch:") {
		t.Errorf("error should have configpatch: prefix, got %q", err.Error())
	}
}

// --- Helper ---

func cloneRaw(r map[string]any) map[string]any {
	if r == nil {
		return nil
	}
	out := make(map[string]any, len(r))
	maps.Copy(out, r)
	return out
}
