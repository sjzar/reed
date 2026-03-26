package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitignoreBasic(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644)
	m := loadGitignore(dir)
	if m == nil {
		t.Fatal("expected non-nil matcher")
	}
	if !m.Match("debug.log", false) {
		t.Error("expected *.log to match debug.log")
	}
	if m.Match("app.go", false) {
		t.Error("expected *.log to not match app.go")
	}
}

func TestGitignoreNegate(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n!important.log\n"), 0o644)
	m := loadGitignore(dir)
	if m == nil {
		t.Fatal("expected non-nil matcher")
	}
	if !m.Match("debug.log", false) {
		t.Error("expected debug.log to be ignored")
	}
	if m.Match("important.log", false) {
		t.Error("expected important.log to NOT be ignored (negated)")
	}
}

func TestGitignoreDirOnly(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0o644)
	m := loadGitignore(dir)
	if m == nil {
		t.Fatal("expected non-nil matcher")
	}
	if !m.Match("build", true) {
		t.Error("expected build/ to match directory")
	}
	if m.Match("build", false) {
		t.Error("expected build/ to NOT match file")
	}
}

func TestGitignoreNil(t *testing.T) {
	var m *gitignoreMatcher
	if m.Match("anything.txt", false) {
		t.Error("nil matcher should not match")
	}
}

func TestGitignoreNoFile(t *testing.T) {
	dir := t.TempDir()
	m := loadGitignore(dir)
	if m != nil {
		t.Error("expected nil matcher when no .gitignore exists")
	}
}
