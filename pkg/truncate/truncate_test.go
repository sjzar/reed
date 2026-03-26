package truncate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHead(t *testing.T) {
	t.Run("no truncation needed", func(t *testing.T) {
		text := "line1\nline2\nline3"
		got, info := Head(text, 10, 0)
		if got != text {
			t.Errorf("got %q, want %q", got, text)
		}
		if info.Truncated {
			t.Error("should not be truncated")
		}
		if info.TotalLines != 3 || info.ShownLines != 3 {
			t.Errorf("lines: total=%d shown=%d", info.TotalLines, info.ShownLines)
		}
	})

	t.Run("truncate by lines", func(t *testing.T) {
		text := "a\nb\nc\nd\ne"
		got, info := Head(text, 3, 0)
		if got != "a\nb\nc" {
			t.Errorf("got %q", got)
		}
		if !info.Truncated {
			t.Error("should be truncated")
		}
		if info.TotalLines != 5 || info.ShownLines != 3 {
			t.Errorf("lines: total=%d shown=%d", info.TotalLines, info.ShownLines)
		}
	})

	t.Run("truncate by bytes", func(t *testing.T) {
		text := "abcdefghij"
		got, info := Head(text, 0, 5)
		if !info.Truncated {
			t.Error("should be truncated")
		}
		if !info.ByteLimitHit {
			t.Error("ByteLimitHit should be true for single-line byte truncation")
		}
		if !strings.Contains(got, "truncated") {
			t.Errorf("expected placeholder, got %q", got)
		}
	})

	t.Run("both limits", func(t *testing.T) {
		text := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 100)
		_, info := Head(text, 10, 50)
		if !info.Truncated {
			t.Error("should be truncated")
		}
	})

	t.Run("empty text", func(t *testing.T) {
		got, info := Head("", 10, 100)
		if got != "" {
			t.Errorf("got %q", got)
		}
		if info.Truncated {
			t.Error("should not be truncated")
		}
	})
}

func TestTail(t *testing.T) {
	t.Run("no truncation needed", func(t *testing.T) {
		text := "line1\nline2\nline3"
		got, info := Tail(text, 10, 0)
		if got != text {
			t.Errorf("got %q, want %q", got, text)
		}
		if info.Truncated {
			t.Error("should not be truncated")
		}
	})

	t.Run("truncate by lines keeps tail", func(t *testing.T) {
		text := "a\nb\nc\nd\ne"
		got, info := Tail(text, 3, 0)
		if got != "c\nd\ne" {
			t.Errorf("got %q", got)
		}
		if !info.Truncated {
			t.Error("should be truncated")
		}
		if info.TotalLines != 5 || info.ShownLines != 3 {
			t.Errorf("lines: total=%d shown=%d", info.TotalLines, info.ShownLines)
		}
	})

	t.Run("truncate by bytes keeps tail", func(t *testing.T) {
		text := "abcdefghij"
		got, info := Tail(text, 0, 5)
		if !info.Truncated {
			t.Error("should be truncated")
		}
		if !info.ByteLimitHit {
			t.Error("ByteLimitHit should be true for single-line byte truncation")
		}
		if !strings.Contains(got, "truncated") {
			t.Errorf("expected placeholder, got %q", got)
		}
	})

	t.Run("empty text", func(t *testing.T) {
		got, info := Tail("", 10, 100)
		if got != "" {
			t.Errorf("got %q", got)
		}
		if info.Truncated {
			t.Error("should not be truncated")
		}
	})
}

func TestHead_UTF8Safety(t *testing.T) {
	text := "Hello, 世界"
	got, info := Head(text, 0, 9)
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
	if !info.Truncated {
		t.Error("should be truncated")
	}
}

func TestTail_UTF8Safety(t *testing.T) {
	text := "世界Hello"
	got, info := Tail(text, 0, 8)
	if !utf8.ValidString(got) {
		t.Errorf("result is not valid UTF-8: %q", got)
	}
	if !info.Truncated {
		t.Error("should be truncated")
	}
}

func TestHead_LineSnap(t *testing.T) {
	text := "line1\nline2\nline3\nline4"
	got, info := Head(text, 0, 13)
	if !info.Truncated {
		t.Error("should be truncated")
	}
	if strings.Contains(got, "line3") {
		t.Errorf("should not contain partial line3, got %q", got)
	}
	if !strings.HasSuffix(got, "line2") && !strings.HasSuffix(got, "line1") {
		if !strings.HasPrefix(got, "[content truncated:") {
			t.Errorf("expected line-boundary snap, got %q", got)
		}
	}
}

func TestHead_SingleLineTooLong(t *testing.T) {
	text := strings.Repeat("x", 200)
	got, info := Head(text, 0, 50)
	if !info.Truncated {
		t.Error("should be truncated")
	}
	if !info.ByteLimitHit {
		t.Error("ByteLimitHit should be true")
	}
	if info.ShownLines != 0 {
		t.Errorf("ShownLines should be 0, got %d", info.ShownLines)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected placeholder message, got %q", got)
	}
}

func TestHead_EmptyStringCountsAsOneLine(t *testing.T) {
	_, info := Head("", 10, 100)
	if info.TotalLines != 1 {
		t.Errorf("empty string should count as 1 line, got %d", info.TotalLines)
	}
}

func TestHead_TrailingNewlineCountsExtraLine(t *testing.T) {
	_, info := Head("a\nb\n", 10, 0)
	if info.TotalLines != 3 {
		t.Errorf("trailing newline should produce 3 lines, got %d", info.TotalLines)
	}
}

func TestHead_CRLFNotNormalized(t *testing.T) {
	text := "line1\r\nline2\r\nline3"
	got, info := Head(text, 2, 0)
	if !info.Truncated {
		t.Error("should be truncated")
	}
	if !strings.Contains(got, "\r") {
		t.Errorf("CRLF should leave \\r in output, got %q", got)
	}
}

func TestTail_CRLFNotNormalized(t *testing.T) {
	text := "line1\r\nline2\r\nline3"
	got, info := Tail(text, 2, 0)
	if !info.Truncated {
		t.Error("should be truncated")
	}
	if !strings.Contains(got, "line3") {
		t.Errorf("tail should contain line3, got %q", got)
	}
}
