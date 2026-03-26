package worker

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveAbsPath_Relative(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	os.Mkdir(sub, 0o755)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	got, err := resolveAbsPath("child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.HasSuffix(got, "child") {
		t.Errorf("expected path ending with child, got %q", got)
	}
}

func TestResolveAbsPath_Absolute(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveAbsPath(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestResolveAbsPath_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	os.Mkdir(real, 0o755)
	link := filepath.Join(dir, "link")
	os.Symlink(real, link)

	got, err := resolveAbsPath(link)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	realResolved, _ := filepath.EvalSymlinks(real)
	if got != realResolved {
		t.Errorf("got %q, want %q (resolved symlink)", got, realResolved)
	}
}

func TestResolveAbsPath_NonExistent(t *testing.T) {
	// Non-existent path should still return an absolute path (best-effort symlink resolution)
	got, err := resolveAbsPath("/tmp/does-not-exist-" + t.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestResolveAbsPath_BrokenSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "broken")
	os.Symlink(filepath.Join(dir, "nonexistent-target"), link)

	got, err := resolveAbsPath(link)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the absolute path even though symlink target doesn't exist
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestResolveWorkDir_Success(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveWorkDir(dir, "test worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
}

func TestResolveWorkDir_ErrorWrapping(t *testing.T) {
	// Empty dir should still work (Abs of "" = cwd)
	got, err := resolveWorkDir("", "test worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	_ = got
}
