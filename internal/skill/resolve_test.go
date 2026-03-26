package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestLoadWorkflow_UsesDirectory(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()
	skillDir := filepath.Join(workflowDir, "skills", "my-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: Dir skill\n---\nBody here\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"my-skill": {Uses: "skills/my-skill"},
	}

	result, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result))
	}
	sk := result["my-skill"]
	if sk.ID != "my-skill" {
		t.Errorf("expected ID 'my-skill', got %q", sk.ID)
	}
	if sk.Meta.Description != "Dir skill" {
		t.Errorf("expected description 'Dir skill', got %q", sk.Meta.Description)
	}
	if sk.Source.Kind != SourceWorkflowPath {
		t.Errorf("expected source kind %q, got %q", SourceWorkflowPath, sk.Source.Kind)
	}
}

func TestLoadWorkflow_UsesSingleFile(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()
	content := "---\nname: file-skill\ndescription: File skill\n---\nFile body\n"
	filePath := filepath.Join(workflowDir, "SKILL.md")
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"file-skill": {Uses: "SKILL.md"},
	}

	result, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sk := result["file-skill"]
	if sk.ID != "file-skill" {
		t.Errorf("expected ID 'file-skill', got %q", sk.ID)
	}
	if sk.Body == "" {
		t.Error("expected non-empty body")
	}
}

func TestLoadWorkflow_UsesSingleFile_WrongFilename(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()
	content := "---\nname: my-skill\ndescription: Skill\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "my-skill.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"my-skill": {Uses: "my-skill.md"},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for non-SKILL.md filename, got nil")
	}
	if !strings.Contains(err.Error(), "must be named SKILL.md") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadWorkflow_InlineResources(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"inline-skill": {
			Resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: inline-skill\ndescription: Inline\n---\nInline body\n"},
			},
		},
	}

	result, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sk := result["inline-skill"]
	if sk.ID != "inline-skill" {
		t.Errorf("expected ID 'inline-skill', got %q", sk.ID)
	}
	if sk.Meta.Description != "Inline" {
		t.Errorf("expected description 'Inline', got %q", sk.Meta.Description)
	}
	if sk.Backing != BackingMod {
		t.Errorf("expected backing %q, got %q", BackingMod, sk.Backing)
	}
}

func TestLoadWorkflow_InlineResources_MissingSkillMD(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"bad-skill": {
			Resources: []model.SkillResourceSpec{
				{Path: "helper.py", Content: "print('hi')"},
			},
		},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md in resources, got nil")
	}
}

func TestLoadWorkflow_MissingUsesPath(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"missing": {Uses: "nonexistent/path"},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for missing uses path, got nil")
	}
}

func TestLoadWorkflow_NameMismatch_DirUses(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	// Dir name is "code-review", front matter name is "code-review",
	// but workflow key is "review" — should fail name != id
	skillDir := filepath.Join(workflowDir, "skills", "code-review")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: code-review\ndescription: Reviews code\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"review": {Uses: "skills/code-review"},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for name mismatch (name != skill id), got nil")
	}
	if !strings.Contains(err.Error(), "does not match ID") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadWorkflow_NameMismatch_InlineResources(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"review": {
			Resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: code-review\ndescription: Reviews code\n---\nBody\n"},
			},
		},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for name mismatch in inline resources, got nil")
	}
}

func TestLoadWorkflow_ResourcePathPreservesDir(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	// Create templates/review.md on disk
	tmplDir := filepath.Join(workflowDir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "review.md"), []byte("review template"), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"my-skill": {
			Resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: my-skill\ndescription: Test\n---\nBody\n"},
				{Path: "templates/review.md", Content: "review template"},
			},
		},
	}

	result, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sk := result["my-skill"]
	// Find the file-based resource and verify path is preserved
	found := false
	for _, f := range sk.Files {
		if f.Path == "templates/review.md" {
			found = true
			break
		}
	}
	if !found {
		paths := make([]string, len(sk.Files))
		for i, f := range sk.Files {
			paths[i] = f.Path
		}
		t.Errorf("expected file with path 'templates/review.md', got paths: %v", paths)
	}
}

func TestLoadWorkflow_DuplicateSkillMD(t *testing.T) {
	workflowDir := t.TempDir()
	modDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"bad-skill": {
			Resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: bad-skill\ndescription: First\n---\nBody1\n"},
				{Path: "SKILL.md", Content: "---\nname: bad-skill\ndescription: Second\n---\nBody2\n"},
			},
		},
	}

	_, err := LoadWorkflow(context.Background(), specs, workflowDir, modDir)
	if err == nil {
		t.Fatal("expected error for duplicate SKILL.md, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateWorkflowSkills_PathBasedSkillMD(t *testing.T) {
	workflowDir := t.TempDir()

	// Write a SKILL.md with invalid front matter (missing description)
	if err := os.WriteFile(filepath.Join(workflowDir, "SKILL.md"), []byte("---\nname: my-skill\n---\nBody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"my-skill": {
			Resources: []model.SkillResourceSpec{
				{Path: "SKILL.md", Content: "---\nname: my-skill\n---\nBody\n"},
			},
		},
	}

	err := ValidateWorkflowSkills(specs, workflowDir)
	if err == nil {
		t.Fatal("expected error for path-based SKILL.md with invalid front matter")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Errorf("expected description validation error, got: %v", err)
	}
}

func TestValidateWorkflowSkills_ValidSpecs(t *testing.T) {
	workflowDir := t.TempDir()
	skillDir := filepath.Join(workflowDir, "skills", "valid-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: valid-skill\ndescription: Valid\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"valid-skill": {Uses: "skills/valid-skill"},
	}

	if err := ValidateWorkflowSkills(specs, workflowDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWorkflowSkills_MissingPath(t *testing.T) {
	workflowDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"missing": {Uses: "nonexistent"},
	}

	err := ValidateWorkflowSkills(specs, workflowDir)
	if err == nil {
		t.Fatal("expected error for missing path, got nil")
	}
}

func TestValidateWorkflowSkills_ResourcesMissingSkillMD(t *testing.T) {
	workflowDir := t.TempDir()

	specs := map[string]model.SkillSpec{
		"bad": {
			Resources: []model.SkillResourceSpec{
				{Path: "helper.py", Content: "print('hi')"},
			},
		},
	}

	err := ValidateWorkflowSkills(specs, workflowDir)
	if err == nil {
		t.Fatal("expected error for missing SKILL.md in resources, got nil")
	}
}
