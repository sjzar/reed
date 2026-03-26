package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// realTempDir returns a canonicalized temp dir (resolves macOS /tmp → /private/var symlink).
func realTempDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	real, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestResolvePath(t *testing.T) {
	tmp := realTempDir(t)

	t.Run("absolute path", func(t *testing.T) {
		p := filepath.Join(tmp, "file.txt")
		os.WriteFile(p, []byte("x"), 0o644)
		got, err := ResolvePath("", p)
		if err != nil {
			t.Fatal(err)
		}
		if got != p {
			t.Errorf("got %q, want %q", got, p)
		}
	})

	t.Run("relative path", func(t *testing.T) {
		os.WriteFile(filepath.Join(tmp, "rel.txt"), []byte("x"), 0o644)
		got, err := ResolvePath(tmp, "rel.txt")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(tmp, "rel.txt")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("dot-dot traversal", func(t *testing.T) {
		sub := filepath.Join(tmp, "sub")
		os.MkdirAll(sub, 0o755)
		got, err := ResolvePath(sub, "../rel.txt")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(tmp, "rel.txt")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty path error", func(t *testing.T) {
		_, err := ResolvePath(tmp, "")
		if err == nil {
			t.Fatal("expected error for empty path")
		}
	})

	t.Run("relative path with empty cwd error", func(t *testing.T) {
		_, err := ResolvePath("", "file.txt")
		if err == nil {
			t.Fatal("expected error for relative path with empty cwd")
		}
	})

	t.Run("non-existent path resolves via ancestor", func(t *testing.T) {
		got, err := ResolvePath(tmp, "nonexistent/deep/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(tmp, "nonexistent", "deep", "file.txt")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestLockKey(t *testing.T) {
	got := LockKey("/tmp/file.txt")
	want := "fs:/tmp/file.txt"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolvePath_RelativeCwd(t *testing.T) {
	// Even with a relative cwd, ResolvePath should produce an absolute path
	// (canonicalize now converts relative to absolute)
	got, err := ResolvePath(".", "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}
