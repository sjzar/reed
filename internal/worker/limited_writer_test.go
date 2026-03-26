package worker

import (
	"testing"
)

func TestLimitedWriter_UnderLimit(t *testing.T) {
	w := newLimitedWriter(100)
	n, err := w.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if w.String() != "hello" {
		t.Errorf("buf = %q, want hello", w.String())
	}
	if w.Overflowed() {
		t.Error("should not overflow")
	}
}

func TestLimitedWriter_ExactLimit(t *testing.T) {
	w := newLimitedWriter(5)
	n, err := w.Write([]byte("12345"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if w.String() != "12345" {
		t.Errorf("buf = %q, want 12345", w.String())
	}
	if w.Overflowed() {
		t.Error("exact limit should not overflow")
	}
}

func TestLimitedWriter_OverLimit(t *testing.T) {
	w := newLimitedWriter(5)
	n, err := w.Write([]byte("1234567890"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 10 {
		t.Errorf("n = %d, want 10 (original len)", n)
	}
	if w.String() != "12345" {
		t.Errorf("buf = %q, want 12345", w.String())
	}
	if !w.Overflowed() {
		t.Error("should overflow")
	}
}

func TestLimitedWriter_MultiWriteCrossBoundary(t *testing.T) {
	w := newLimitedWriter(8)

	n1, _ := w.Write([]byte("abcde")) // 5 bytes, 3 remaining
	if n1 != 5 {
		t.Errorf("n1 = %d, want 5", n1)
	}

	n2, _ := w.Write([]byte("fghij")) // 5 bytes, only 3 fit
	if n2 != 5 {
		t.Errorf("n2 = %d, want 5 (original len)", n2)
	}
	if w.String() != "abcdefgh" {
		t.Errorf("buf = %q, want abcdefgh", w.String())
	}
	if !w.Overflowed() {
		t.Error("should overflow after crossing boundary")
	}

	// Further writes are fully discarded
	n3, _ := w.Write([]byte("more"))
	if n3 != 4 {
		t.Errorf("n3 = %d, want 4", n3)
	}
	if w.String() != "abcdefgh" {
		t.Errorf("buf should not grow: %q", w.String())
	}
}

func TestLimitedWriter_AlwaysReturnsLen(t *testing.T) {
	w := newLimitedWriter(1)
	data := make([]byte, 1024)
	for i := range data {
		data[i] = 'x'
	}
	n, err := w.Write(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1024 {
		t.Errorf("n = %d, want 1024 — must always return len(p) for exec.Cmd compatibility", n)
	}
}
