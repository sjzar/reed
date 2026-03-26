package logutil

import (
	"path/filepath"
	"testing"

	"gopkg.in/natefinch/lumberjack.v2"
)

func TestNewRotatingWriter_Defaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	wc := NewRotatingWriter(path)
	defer wc.Close()

	l, ok := wc.(*lumberjack.Logger)
	if !ok {
		t.Fatal("expected *lumberjack.Logger")
	}
	if l.Filename != path {
		t.Errorf("expected Filename %q, got %q", path, l.Filename)
	}
	if l.MaxSize != DefaultMaxSizeMB {
		t.Errorf("expected MaxSize %d, got %d", DefaultMaxSizeMB, l.MaxSize)
	}
	if l.MaxAge != DefaultMaxAgeDays {
		t.Errorf("expected MaxAge %d, got %d", DefaultMaxAgeDays, l.MaxAge)
	}
	if l.MaxBackups != DefaultMaxBackups {
		t.Errorf("expected MaxBackups %d, got %d", DefaultMaxBackups, l.MaxBackups)
	}
	if !l.Compress {
		t.Error("expected Compress=true by default")
	}
}

func TestWithMaxSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	wc := NewRotatingWriter(path, WithMaxSize(10))
	defer wc.Close()

	l := wc.(*lumberjack.Logger)
	if l.MaxSize != 10 {
		t.Errorf("expected MaxSize 10, got %d", l.MaxSize)
	}
}

func TestWithMaxAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	wc := NewRotatingWriter(path, WithMaxAge(7))
	defer wc.Close()

	l := wc.(*lumberjack.Logger)
	if l.MaxAge != 7 {
		t.Errorf("expected MaxAge 7, got %d", l.MaxAge)
	}
}

func TestWithMaxBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")
	wc := NewRotatingWriter(path, WithMaxBackups(5))
	defer wc.Close()

	l := wc.(*lumberjack.Logger)
	if l.MaxBackups != 5 {
		t.Errorf("expected MaxBackups 5, got %d", l.MaxBackups)
	}
}

func TestWithCompress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.log")

	t.Run("disable compress", func(t *testing.T) {
		wc := NewRotatingWriter(path, WithCompress(false))
		defer wc.Close()
		l := wc.(*lumberjack.Logger)
		if l.Compress {
			t.Error("expected Compress=false")
		}
	})

	t.Run("enable compress", func(t *testing.T) {
		wc := NewRotatingWriter(path, WithCompress(true))
		defer wc.Close()
		l := wc.(*lumberjack.Logger)
		if !l.Compress {
			t.Error("expected Compress=true")
		}
	})
}
