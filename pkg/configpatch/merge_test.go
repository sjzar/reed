package configpatch

import (
	"testing"
)

func TestMergeRFC7386_ScalarOverride(t *testing.T) {
	base := map[string]any{"name": "base", "version": "1.0"}
	patch := map[string]any{"version": "2.0"}
	result := MergeRFC7386(base, patch)

	if result["name"] != "base" {
		t.Errorf("name = %v, want base", result["name"])
	}
	if result["version"] != "2.0" {
		t.Errorf("version = %v, want 2.0", result["version"])
	}
}

func TestMergeRFC7386_DeepMerge(t *testing.T) {
	base := map[string]any{
		"env": map[string]any{"A": "1", "B": "2"},
	}
	patch := map[string]any{
		"env": map[string]any{"B": "override", "C": "3"},
	}
	result := MergeRFC7386(base, patch)

	env, ok := result["env"].(map[string]any)
	if !ok {
		t.Fatal("env is not a map")
	}
	if env["A"] != "1" {
		t.Errorf("env.A = %v, want 1", env["A"])
	}
	if env["B"] != "override" {
		t.Errorf("env.B = %v, want override", env["B"])
	}
	if env["C"] != "3" {
		t.Errorf("env.C = %v, want 3", env["C"])
	}
}

func TestMergeRFC7386_NullTombstone(t *testing.T) {
	base := map[string]any{"name": "test", "remove_me": "value"}
	patch := map[string]any{"remove_me": nil}
	result := MergeRFC7386(base, patch)

	if _, exists := result["remove_me"]; exists {
		t.Error("remove_me should be deleted by null tombstone")
	}
	if result["name"] != "test" {
		t.Errorf("name = %v, want test", result["name"])
	}
}

func TestMergeRFC7386_ArrayReplacement(t *testing.T) {
	base := map[string]any{"items": []any{"a", "b"}}
	patch := map[string]any{"items": []any{"x"}}
	result := MergeRFC7386(base, patch)

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatal("items is not a slice")
	}
	if len(items) != 1 || items[0] != "x" {
		t.Errorf("items = %v, want [x]", items)
	}
}

func TestMergeRFC7386_NilBase(t *testing.T) {
	patch := map[string]any{"a": "1"}
	result := MergeRFC7386(nil, patch)
	if result["a"] != "1" {
		t.Errorf("a = %v, want 1", result["a"])
	}
}

func TestMergeAll(t *testing.T) {
	base := map[string]any{"a": "1"}
	p1 := map[string]any{"b": "2"}
	p2 := map[string]any{"a": "override"}
	result := MergeAll(base, p1, p2)

	if result["a"] != "override" {
		t.Errorf("a = %v, want override", result["a"])
	}
	if result["b"] != "2" {
		t.Errorf("b = %v, want 2", result["b"])
	}
}
