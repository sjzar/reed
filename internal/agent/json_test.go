package agent

import (
	"testing"
)

func TestRepetitionDetector_NoRepetition(t *testing.T) {
	d := newRepetitionDetector(3)
	d.recordRound([]roundCall{{Name: "a", Input: "1", Output: "x"}})
	d.recordRound([]roundCall{{Name: "a", Input: "2", Output: "y"}})
	d.recordRound([]roundCall{{Name: "a", Input: "3", Output: "z"}})
	if d.detected() {
		t.Error("should not detect repetition with different rounds")
	}
}

func TestRepetitionDetector_Repetition(t *testing.T) {
	d := newRepetitionDetector(3)
	call := []roundCall{{Name: "a", Input: "1", Output: "x"}}
	d.recordRound(call)
	d.recordRound(call)
	if d.detected() {
		t.Error("should not detect with only 2 rounds")
	}
	d.recordRound(call)
	if !d.detected() {
		t.Error("should detect repetition after 3 identical rounds")
	}
}

func TestRepetitionDetector_WindowSliding(t *testing.T) {
	d := newRepetitionDetector(3)
	same := []roundCall{{Name: "a", Input: "1", Output: "x"}}
	diff := []roundCall{{Name: "b", Input: "2", Output: "y"}}

	d.recordRound(same)
	d.recordRound(same)
	d.recordRound(diff) // breaks the streak
	d.recordRound(same)
	if d.detected() {
		t.Error("should not detect after streak was broken")
	}
}

func TestCanonicalJSON(t *testing.T) {
	got := canonicalJSON(map[string]any{"b": 2, "a": 1})
	if got != `{"a":1,"b":2}` {
		t.Errorf("got %q", got)
	}
}

func TestCanonicalJSON_Nil(t *testing.T) {
	if canonicalJSON(nil) != "{}" {
		t.Error("nil should return {}")
	}
}

func TestHashPrefix(t *testing.T) {
	h := hashPrefix("hello", 8)
	if len(h) != 8 {
		t.Errorf("length: got %d, want 8", len(h))
	}
	h2 := hashPrefix("hello", 8)
	if h != h2 {
		t.Error("same input should produce same hash")
	}
	h3 := hashPrefix("world", 8)
	if h == h3 {
		t.Error("different input should produce different hash")
	}
}
