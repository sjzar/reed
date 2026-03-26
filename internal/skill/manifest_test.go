package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadManifest_NonExistent(t *testing.T) {
	mf, err := ReadManifest("/nonexistent/path/skills.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mf.Skills) != 0 {
		t.Errorf("expected empty skills, got %d", len(mf.Skills))
	}
}

func TestReadManifest_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadManifest(path)
	if err == nil {
		t.Fatal("expected error for corrupt manifest")
	}
}

func TestReadManifest_NullEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skills.json")
	content := `{"skills":{"foo":null,"bar":{"mod_path":"bar-dir","source":"github.com/x/y@main","ref":"main"}}}`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	mf, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// null entry should be stripped
	if _, ok := mf.Skills["foo"]; ok {
		t.Error("expected null entry 'foo' to be stripped")
	}
	if _, ok := mf.Skills["bar"]; !ok {
		t.Error("expected entry 'bar' to be present")
	}
}

func TestWriteManifest_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "skills.json")

	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"test-skill": {
			ModPath: "some/path",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
			Commit:  "abc123",
		},
	}}

	if err := WriteManifest(path, mf); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(got.Skills))
	}
	entry := got.Skills["test-skill"]
	if entry.ModPath != "some/path" {
		t.Errorf("expected mod_path 'some/path', got %q", entry.ModPath)
	}
	if entry.Commit != "abc123" {
		t.Errorf("expected commit 'abc123', got %q", entry.Commit)
	}
}

func TestResolveModPath_Valid(t *testing.T) {
	modDir := t.TempDir()
	subDir := filepath.Join(modDir, "my-skill")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveModPath(modDir, "my-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(modDir, "my-skill") {
		t.Errorf("expected %q, got %q", filepath.Join(modDir, "my-skill"), got)
	}
}

func TestResolveModPath_AbsoluteRejected(t *testing.T) {
	_, err := resolveModPath(t.TempDir(), "/etc/passwd")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestResolveModPath_TraversalRejected(t *testing.T) {
	_, err := resolveModPath(t.TempDir(), "../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestResolveModPath_ForwardSlash(t *testing.T) {
	modDir := t.TempDir()
	subDir := filepath.Join(modDir, "github_com_user_repo_abc123", "skills", "review")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveModPath(modDir, "github_com_user_repo_abc123/skills/review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(modDir, "github_com_user_repo_abc123", "skills", "review")
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestResolveModPath_SymlinkEscape(t *testing.T) {
	modDir := t.TempDir()
	outsideDir := t.TempDir()

	// Create a symlink inside modDir that points outside
	linkPath := filepath.Join(modDir, "evil")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	_, err := resolveModPath(modDir, "evil")
	if err == nil {
		t.Fatal("expected error for symlink escape")
	}
}

func TestManifestPath(t *testing.T) {
	got := ManifestPath(ScopeLocal, "/work", "/home")
	if got != filepath.Join("/work", ".reed", "skills.json") {
		t.Errorf("local: got %q", got)
	}

	got = ManifestPath(ScopeGlobal, "/work", "/home")
	if got != filepath.Join("/home", "skills.json") {
		t.Errorf("global: got %q", got)
	}

	got = ManifestPath(ScopeProject, "/work", "/home")
	if got != "" {
		t.Errorf("project: expected empty, got %q", got)
	}
}
