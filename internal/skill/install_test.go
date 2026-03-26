package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstall_LocalPathRejected(t *testing.T) {
	_, err := Install(context.Background(), "./local/path", ScopeLocal, t.TempDir(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for local path")
	}
}

func TestInstall_RelativePathRejected(t *testing.T) {
	_, err := Install(context.Background(), "../relative/path", ScopeLocal, t.TempDir(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestInstall_InvalidScope(t *testing.T) {
	_, err := Install(context.Background(), "github.com/user/repo", SkillScope("invalid"), t.TempDir(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestUninstall_NotFound(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()

	err := Uninstall("nonexistent", ScopeLocal, workDir, homeDir)
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestUninstall_Success(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()

	// Create a manifest with a skill
	manifestPath := ManifestPath(ScopeLocal, workDir, homeDir)
	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"test-skill": {
			ModPath: "test-skill",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
		},
	}}
	if err := WriteManifest(manifestPath, mf); err != nil {
		t.Fatal(err)
	}

	// Uninstall
	if err := Uninstall("test-skill", ScopeLocal, workDir, homeDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's gone
	got, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Skills["test-skill"]; ok {
		t.Error("expected skill to be removed from manifest")
	}
}

func TestUninstall_InvalidScope(t *testing.T) {
	err := Uninstall("skill", SkillScope("invalid"), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestTidy_AllPresent(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	// Create skill in modDir
	writeSkillDir(t, modDir, "present-skill", "A present skill")

	// Create manifest pointing to it
	manifestPath := ManifestPath(ScopeLocal, workDir, homeDir)
	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"present-skill": {
			ModPath: "present-skill",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
		},
	}}
	if err := WriteManifest(manifestPath, mf); err != nil {
		t.Fatal(err)
	}

	result, err := Tidy(context.Background(), ScopeLocal, workDir, homeDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Fixed) != 0 {
		t.Errorf("expected no fixes, got %v", result.Fixed)
	}
	if len(result.Failed) != 0 {
		t.Errorf("expected no failures, got %v", result.Failed)
	}
}

func TestTidy_MissingFailsGracefully(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	// Create manifest pointing to non-existent skill
	manifestPath := ManifestPath(ScopeLocal, workDir, homeDir)
	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"missing-skill": {
			ModPath: "missing-dir",
			Source:  "github.com/nonexistent/repo@main",
			Ref:     "main",
		},
	}}
	if err := WriteManifest(manifestPath, mf); err != nil {
		t.Fatal(err)
	}

	// Tidy should fail gracefully (network error) and record failure
	result, err := Tidy(context.Background(), ScopeLocal, workDir, homeDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Failed) != 1 {
		t.Errorf("expected 1 failure, got %d", len(result.Failed))
	}
}

func TestTidy_InvalidScope(t *testing.T) {
	_, err := Tidy(context.Background(), SkillScope("invalid"), t.TempDir(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestRefString(t *testing.T) {
	tests := []struct {
		ref  *RemoteRef
		want string
	}{
		{nil, ""},
		{&RemoteRef{Kind: RemoteHTTP, RawURL: "https://example.com/SKILL.md"}, "http"},
		{&RemoteRef{Kind: RemoteGitHub, Ref: "v1.0"}, "v1.0"},
		{&RemoteRef{Kind: RemoteGitHub, Ref: "main"}, "main"},
	}
	for _, tt := range tests {
		got := refString(tt.ref)
		if got != tt.want {
			t.Errorf("refString(%v) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestDeriveSubPath(t *testing.T) {
	// With explicit SubPath on ref
	ref := &RemoteRef{Kind: RemoteGitHub, SubPath: "skills/review"}
	sk := &ResolvedSkill{}
	if got := deriveSubPath(sk, ref); got != "skills/review" {
		t.Errorf("expected 'skills/review', got %q", got)
	}

	// Non-GitHub ref
	httpRef := &RemoteRef{Kind: RemoteHTTP}
	if got := deriveSubPath(sk, httpRef); got != "" {
		t.Errorf("expected empty for HTTP, got %q", got)
	}

	// Nil ref
	if got := deriveSubPath(sk, nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}

	// Fan-out: SourceDir with skills/ parent
	modDir := t.TempDir()
	skillDir := filepath.Join(modDir, "cache_abc123", "skills", "review")
	os.MkdirAll(skillDir, 0755)
	sk2 := &ResolvedSkill{Source: SkillSource{SourceDir: skillDir}}
	ref2 := &RemoteRef{Kind: RemoteGitHub, Ref: "main"} // no SubPath
	if got := deriveSubPath(sk2, ref2); got != "skills/review" {
		t.Errorf("expected 'skills/review', got %q", got)
	}
}
