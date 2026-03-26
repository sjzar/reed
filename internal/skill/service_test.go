package skill

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestNew_EmptyCatalog(t *testing.T) {
	svc := New("/tmp/home", "/tmp/mod")
	ids := svc.AllIDs()
	if len(ids) != 0 {
		t.Fatalf("expected empty catalog, got %d entries", len(ids))
	}
}

func TestService_ScanInstalled(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	// Create a skill in project skills/ dir
	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "home-skill", "Home skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids := svc.AllIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(ids))
	}
	if ids[0] != "home-skill" {
		t.Errorf("expected 'home-skill', got %q", ids[0])
	}
}

func TestService_LoadWorkflow(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workflowDir := t.TempDir()

	// Create a skill directory for the uses path
	skillDir := filepath.Join(workflowDir, "skills", "wf-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: wf-skill\ndescription: Workflow skill\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	svc := New(homeDir, modDir)
	specs := map[string]model.SkillSpec{
		"wf-skill": {Uses: "skills/wf-skill"},
	}
	if err := svc.LoadWorkflow(context.Background(), specs, workflowDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids := svc.AllIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(ids))
	}
}

func TestService_Get(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "get-skill", "Get skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	sk, ok := svc.Get("get-skill")
	if !ok {
		t.Fatal("expected to find 'get-skill'")
	}
	if sk.Meta.Name != "get-skill" {
		t.Errorf("expected name 'get-skill', got %q", sk.Meta.Name)
	}

	_, ok = svc.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find 'nonexistent'")
	}
}

func TestService_AllIDs(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "alpha-skill", "Alpha")
	writeSkillDir(t, projectSkillsDir, "beta-skill", "Beta")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	ids := svc.AllIDs()
	sort.Strings(ids)
	if len(ids) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(ids))
	}
	if ids[0] != "alpha-skill" || ids[1] != "beta-skill" {
		t.Errorf("expected [alpha-skill, beta-skill], got %v", ids)
	}
}

func TestService_ListAndMount(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()
	runRoot := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "mount-skill", "Mount skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	infos, err := svc.ListAndMount(runRoot, []string{"mount-skill"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 info, got %d", len(infos))
	}
	if infos[0].ID != "mount-skill" {
		t.Errorf("expected ID 'mount-skill', got %q", infos[0].ID)
	}
	if infos[0].MountDir == "" {
		t.Error("expected non-empty MountDir")
	}
	// Verify mount directory exists
	expectedMount := filepath.Join(runRoot, "skills", "mount-skill")
	if _, err := os.Lstat(expectedMount); err != nil {
		t.Errorf("expected mount dir to exist at %s: %v", expectedMount, err)
	}
}

func TestService_ListAndMount_UnknownID(t *testing.T) {
	svc := New(t.TempDir(), t.TempDir())

	_, err := svc.ListAndMount(t.TempDir(), []string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown skill ID, got nil")
	}
}

func TestService_ResolveEnabled(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "resolve-skill", "Resolve skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	resolved, err := svc.ResolveEnabled([]string{"resolve-skill"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved skill, got %d", len(resolved))
	}
	if resolved[0].ID != "resolve-skill" {
		t.Errorf("expected ID 'resolve-skill', got %q", resolved[0].ID)
	}
}

func TestService_EnsureMounted(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()
	runRoot := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "ensure-skill", "Ensure skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	mountDir, err := svc.EnsureMounted(runRoot, "ensure-skill")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mountDir == "" {
		t.Error("expected non-empty mount dir")
	}
	if _, err := os.Lstat(mountDir); err != nil {
		t.Errorf("expected mount dir to exist: %v", err)
	}
}

func TestService_LoadWorkflow_ConflictWithInstalled(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()
	workflowDir := t.TempDir()

	// Create an installed skill in project skills/
	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "my-skill", "Installed skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatalf("scan installed: %v", err)
	}

	// Create a workflow skill with the same ID
	wfSkillDir := filepath.Join(workflowDir, "skills", "my-skill")
	if err := os.MkdirAll(wfSkillDir, 0755); err != nil {
		t.Fatal(err)
	}
	wfContent := "---\nname: my-skill\ndescription: Workflow skill\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(wfSkillDir, "SKILL.md"), []byte(wfContent), 0644); err != nil {
		t.Fatal(err)
	}

	specs := map[string]model.SkillSpec{
		"my-skill": {Uses: "skills/my-skill"},
	}
	err := svc.LoadWorkflow(context.Background(), specs, workflowDir)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict error, got: %v", err)
	}
}

func TestService_ResolveCLISkills_LocalOnly(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "cli-skill", "CLI skill")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	ids, err := svc.ResolveCLISkills(context.Background(), []string{"cli-skill"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "cli-skill" {
		t.Errorf("expected [cli-skill], got %v", ids)
	}
}

func TestService_ResolveCLISkills_DuplicateDerivedID(t *testing.T) {
	svc := New(t.TempDir(), t.TempDir())

	// Two remote URLs that derive the same ID "review"
	_, err := svc.ResolveCLISkills(context.Background(), []string{
		"github.com/a/repo/skills/review@v1",
		"github.com/b/repo/skills/review@v2",
	})
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	if !strings.Contains(err.Error(), "collision") {
		t.Errorf("expected collision error, got: %v", err)
	}
}

func TestService_ResolveCLISkills_ConflictWithInstalled(t *testing.T) {
	homeDir := t.TempDir()
	modDir := t.TempDir()
	workDir := t.TempDir()

	// Install a skill named "review" in project skills/
	projectSkillsDir := filepath.Join(workDir, "skills")
	if err := os.MkdirAll(projectSkillsDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, projectSkillsDir, "review", "Installed review")

	svc := New(homeDir, modDir)
	if err := svc.ScanInstalled(context.Background(), workDir); err != nil {
		t.Fatal(err)
	}

	// Try resolving a local ref with the same name — should pass (it's just a local ID)
	ids, err := svc.ResolveCLISkills(context.Background(), []string{"review"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "review" {
		t.Errorf("expected [review], got %v", ids)
	}
}

func TestService_ResolveCLISkills_InvalidRef(t *testing.T) {
	svc := New(t.TempDir(), t.TempDir())

	_, err := svc.ResolveCLISkills(context.Background(), []string{
		"http://insecure.example.com/SKILL.md",
	})
	if err == nil {
		t.Fatal("expected error for plaintext http, got nil")
	}
}
