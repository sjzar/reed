package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sjzar/reed/pkg/configpatch"
)

// --- LoadSetFile tests ---

func TestLoadSetFile_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "patch.yaml")
	if err := os.WriteFile(path, []byte("name: patched\nversion: '2.0'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	raw, err := LoadSetFile(path)
	if err != nil {
		t.Fatalf("LoadSetFile: %v", err)
	}
	if raw["name"] != "patched" {
		t.Errorf("name = %v, want patched", raw["name"])
	}
	if raw["version"] != "2.0" {
		t.Errorf("version = %v, want 2.0", raw["version"])
	}
}

func TestLoadSetFile_MissingFile(t *testing.T) {
	_, err := LoadSetFile("/nonexistent/patch.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadSetFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSetFile(path)
	if err == nil {
		t.Fatal("expected error for empty document")
	}
}

func TestLoadSetFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":\n  :\n    - [invalid"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSetFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// --- SetFile + RFC 7386 merge integration ---

func TestSetFileMerge_Integration(t *testing.T) {
	dir := t.TempDir()

	// Write base
	basePath := filepath.Join(dir, "base.yaml")
	baseContent := "name: base\nenv:\n  A: '1'\n  B: '2'\nitems:\n  - x\n  - y\n"
	if err := os.WriteFile(basePath, []byte(baseContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Write patch
	patchPath := filepath.Join(dir, "patch.yaml")
	patchContent := "env:\n  B: override\n  C: '3'\nitems:\n  - z\nremove_me: null\n"
	if err := os.WriteFile(patchPath, []byte(patchContent), 0644); err != nil {
		t.Fatal(err)
	}

	base, err := LoadBase(basePath)
	if err != nil {
		t.Fatalf("LoadBase: %v", err)
	}
	patch, err := LoadSetFile(patchPath)
	if err != nil {
		t.Fatalf("LoadSetFile: %v", err)
	}

	result := configpatch.MergeRFC7386(base, patch)

	// name preserved
	if result["name"] != "base" {
		t.Errorf("name = %v, want base", result["name"])
	}

	// env deep merged
	env := result["env"].(map[string]any)
	if env["A"] != "1" {
		t.Errorf("env.A = %v, want 1", env["A"])
	}
	if env["B"] != "override" {
		t.Errorf("env.B = %v, want override", env["B"])
	}
	if env["C"] != "3" {
		t.Errorf("env.C = %v, want 3", env["C"])
	}

	// items wholesale replaced
	items := result["items"].([]any)
	if len(items) != 1 || items[0] != "z" {
		t.Errorf("items = %v, want [z]", items)
	}

	// null tombstone
	if _, exists := result["remove_me"]; exists {
		t.Error("remove_me should be deleted by null tombstone")
	}
}

// --- Multiple set-files applied in order ---

func TestSetFileMerge_Order(t *testing.T) {
	dir := t.TempDir()

	basePath := filepath.Join(dir, "base.yaml")
	if err := os.WriteFile(basePath, []byte("name: base\nversion: '1'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	patch1Path := filepath.Join(dir, "p1.yaml")
	if err := os.WriteFile(patch1Path, []byte("version: '2'\nextra: from_p1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	patch2Path := filepath.Join(dir, "p2.yaml")
	if err := os.WriteFile(patch2Path, []byte("version: '3'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	base, _ := LoadBase(basePath)
	p1, _ := LoadSetFile(patch1Path)
	p2, _ := LoadSetFile(patch2Path)

	result := configpatch.MergeAll(base, p1, p2)

	if result["name"] != "base" {
		t.Errorf("name = %v, want base", result["name"])
	}
	if result["version"] != "3" {
		t.Errorf("version = %v, want 3 (last patch wins)", result["version"])
	}
	if result["extra"] != "from_p1" {
		t.Errorf("extra = %v, want from_p1", result["extra"])
	}
}

// --- Full pipeline: set-file + set + set-string ---

func TestFullPipeline(t *testing.T) {
	dir := t.TempDir()

	basePath := filepath.Join(dir, "base.yaml")
	if err := os.WriteFile(basePath, []byte("name: base\nenv:\n  A: '1'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(dir, "patch.yaml")
	if err := os.WriteFile(patchPath, []byte("env:\n  B: '2'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the pipeline
	raw, err := LoadBase(basePath)
	if err != nil {
		t.Fatal(err)
	}

	patch, err := LoadSetFile(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = configpatch.MergeRFC7386(raw, patch)

	raw, err = configpatch.ApplySet(raw, []string{"env.C=3,debug=true"})
	if err != nil {
		t.Fatal(err)
	}

	raw, err = configpatch.ApplySetString(raw, []string{"env.D=true"})
	if err != nil {
		t.Fatal(err)
	}

	env := raw["env"].(map[string]any)
	if env["A"] != "1" {
		t.Errorf("env.A = %v, want 1", env["A"])
	}
	if env["B"] != "2" {
		t.Errorf("env.B = %v, want 2", env["B"])
	}
	if env["C"] != 3 {
		t.Errorf("env.C = %v (%T), want 3 (int)", env["C"], env["C"])
	}
	if env["D"] != "true" {
		t.Errorf("env.D = %v (%T), want 'true' (string)", env["D"], env["D"])
	}
	if raw["debug"] != true {
		t.Errorf("debug = %v (%T), want true (bool)", raw["debug"], raw["debug"])
	}
}
