package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSkillDir creates a skill directory with a SKILL.md containing valid front matter.
func writeSkillDir(t *testing.T, base, name, description string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\nBody content\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanInstalled_EmptyDirectories(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	result, err := ScanInstalled(context.Background(), workDir, homeDir, modDir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Effective) != 0 {
		t.Fatalf("expected empty effective map, got %d entries", len(result.Effective))
	}
}

func TestScanInstalled_NonExistentDirectory(t *testing.T) {
	result, err := ScanInstalled(context.Background(), "/nonexistent/work", "/nonexistent/home", "/nonexistent/mod", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Effective) != 0 {
		t.Fatalf("expected empty effective map, got %d entries", len(result.Effective))
	}
}

func TestScanInstalled_ProjectSkill(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "local-skill", "A local skill")

	result, err := ScanInstalled(context.Background(), workDir, homeDir, modDir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Effective) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Effective))
	}
	sk, ok := result.Effective["local-skill"]
	if !ok {
		t.Fatal("expected skill 'local-skill' in effective map")
	}
	if sk.Meta.Name != "local-skill" {
		t.Errorf("expected name 'local-skill', got %q", sk.Meta.Name)
	}
	if sk.Scope != ScopeProject {
		t.Errorf("expected scope %q, got %q", ScopeProject, sk.Scope)
	}
}

func TestScanInstalled_ManifestSkill(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	// Create a skill in modDir
	writeSkillDir(t, modDir, "remote-skill", "A remote skill")

	// Write local manifest pointing to it
	manifestPath := filepath.Join(workDir, ".reed", "skills.json")
	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"remote-skill": {
			ModPath: "remote-skill",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
			Commit:  "abc123",
		},
	}}
	if err := WriteManifest(manifestPath, mf); err != nil {
		t.Fatal(err)
	}

	result, err := ScanInstalled(context.Background(), workDir, homeDir, modDir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Effective) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Effective))
	}
	sk, ok := result.Effective["remote-skill"]
	if !ok {
		t.Fatal("expected skill 'remote-skill' in effective map")
	}
	if sk.Scope != ScopeLocal {
		t.Errorf("expected scope %q, got %q", ScopeLocal, sk.Scope)
	}
}

func TestScanInstalled_Shadowing(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	// Create a skill in modDir for global manifest
	writeSkillDir(t, modDir, "shared-skill", "Global version")

	// Write global manifest
	globalPath := filepath.Join(homeDir, "skills.json")
	globalMF := &ManifestFile{Skills: map[string]*ManifestEntry{
		"shared-skill": {
			ModPath: "shared-skill",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
		},
	}}
	if err := WriteManifest(globalPath, globalMF); err != nil {
		t.Fatal(err)
	}

	// Create same skill in project skills/ (higher priority)
	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "shared-skill", "Project version")

	result, err := ScanInstalled(context.Background(), workDir, homeDir, modDir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Effective should have the project version
	if len(result.Effective) != 1 {
		t.Fatalf("expected 1 effective skill, got %d", len(result.Effective))
	}
	sk := result.Effective["shared-skill"]
	if sk.Scope != ScopeProject {
		t.Errorf("expected project scope, got %q", sk.Scope)
	}

	// Entries should have both (one shadowed)
	var shadowCount int
	for _, e := range result.Entries {
		if e.IsShadowed {
			shadowCount++
		}
	}
	if shadowCount != 1 {
		t.Errorf("expected 1 shadowed entry, got %d", shadowCount)
	}
}

func TestScanInstalled_MissingModPath(t *testing.T) {
	homeDir := t.TempDir()
	workDir := t.TempDir()
	modDir := t.TempDir()

	// Write local manifest pointing to non-existent modDir path
	manifestPath := filepath.Join(workDir, ".reed", "skills.json")
	mf := &ManifestFile{Skills: map[string]*ManifestEntry{
		"missing-skill": {
			ModPath: "nonexistent-dir",
			Source:  "github.com/user/repo@main",
			Ref:     "main",
		},
	}}
	if err := WriteManifest(manifestPath, mf); err != nil {
		t.Fatal(err)
	}

	// Diagnostic mode: should record MISSING, not error
	result, err := ScanInstalled(context.Background(), workDir, homeDir, modDir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Effective) != 0 {
		t.Fatalf("expected 0 effective skills, got %d", len(result.Effective))
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if !result.Entries[0].IsMissing {
		t.Error("expected entry to be marked as missing")
	}
}

func TestScanDir_SubdirectoryFiles(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "my-skill")
	subDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillContent := "---\nname: my-skill\ndescription: A skill with subdirs\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "review.md"), []byte("template content"), 0644); err != nil {
		t.Fatal(err)
	}

	sk, err := scanDir(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have both SKILL.md and templates/review.md
	if len(sk.Files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(sk.Files))
	}

	found := make(map[string]bool)
	for _, f := range sk.Files {
		found[f.Path] = true
	}
	if !found["SKILL.md"] {
		t.Error("expected SKILL.md in files")
	}
	if !found[filepath.Join("templates", "review.md")] {
		t.Error("expected templates/review.md in files")
	}
}
