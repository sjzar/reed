package jsonutil

import (
	"testing"
)

func TestDeterministicMarshal(t *testing.T) {
	got := DeterministicMarshal(map[string]any{"b": 2, "a": 1})
	if got != `{"a":1,"b":2}` {
		t.Errorf("got %q", got)
	}
}

func TestDeterministicMarshal_Nil(t *testing.T) {
	if DeterministicMarshal(nil) != "{}" {
		t.Error("nil should return {}")
	}
}

func TestHashPrefix(t *testing.T) {
	h := HashPrefix("hello", 8)
	if len(h) != 8 {
		t.Errorf("length: got %d, want 8", len(h))
	}
	h2 := HashPrefix("hello", 8)
	if h != h2 {
		t.Error("same input should produce same hash")
	}
	h3 := HashPrefix("world", 8)
	if h == h3 {
		t.Error("different input should produce different hash")
	}
}

func TestHashPrefix_NegativeN(t *testing.T) {
	if got := HashPrefix("hello", -1); got != "" {
		t.Errorf("negative n: got %q, want empty", got)
	}
	if got := HashPrefix("hello", 0); got != "" {
		t.Errorf("zero n: got %q, want empty", got)
	}
}

func TestDeepClone_Map(t *testing.T) {
	orig := map[string]any{
		"a": "hello",
		"b": map[string]any{"nested": "value"},
	}
	cp := DeepClone(orig).(map[string]any)

	cp["a"] = "mutated"
	cp["b"].(map[string]any)["nested"] = "mutated"

	if orig["a"] != "hello" {
		t.Error("top-level string was mutated")
	}
	if orig["b"].(map[string]any)["nested"] != "value" {
		t.Error("nested map was mutated")
	}
}

func TestDeepClone_SliceAny(t *testing.T) {
	orig := []any{"a", map[string]any{"k": "v"}}
	cp := DeepClone(orig).([]any)

	cp[0] = "mutated"
	cp[1].(map[string]any)["k"] = "mutated"

	if orig[0] != "a" {
		t.Error("slice element was mutated")
	}
	if orig[1].(map[string]any)["k"] != "v" {
		t.Error("nested map in slice was mutated")
	}
}

func TestDeepClone_SliceString(t *testing.T) {
	orig := []string{"a", "b", "c"}
	cp := DeepClone(orig).([]string)

	cp[0] = "mutated"
	if orig[0] != "a" {
		t.Error("string slice was mutated")
	}
}

func TestDeepClone_Primitive(t *testing.T) {
	if DeepClone(42) != 42 {
		t.Error("int not preserved")
	}
	if DeepClone(3.14) != 3.14 {
		t.Error("float not preserved")
	}
	if DeepClone("hello") != "hello" {
		t.Error("string not preserved")
	}
	if DeepClone(true) != true {
		t.Error("bool not preserved")
	}
	if DeepClone(nil) != nil {
		t.Error("nil not preserved")
	}
}
