package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureMounted(t *testing.T) {
	t.Run("successful mount with symlink", func(t *testing.T) {
		tmp := t.TempDir()
		runRoot := filepath.Join(tmp, "run")
		backingDir := filepath.Join(tmp, "backing")

		// Create backing directory with a file
		if err := os.MkdirAll(backingDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backingDir, "SKILL.md"), []byte("# test"), 0644); err != nil {
			t.Fatal(err)
		}

		sk := &ResolvedSkill{
			ID:         "test-skill",
			BackingDir: backingDir,
			Files: []ResolvedFile{
				{Path: "SKILL.md", Content: []byte("# test")},
			},
		}

		mountDir, err := EnsureMounted(runRoot, sk)
		if err != nil {
			t.Fatalf("EnsureMounted failed: %v", err)
		}

		expected := filepath.Join(runRoot, "skills", "test-skill")
		if mountDir != expected {
			t.Errorf("mount dir = %q, want %q", mountDir, expected)
		}

		// Verify the symlink target file is accessible
		content, err := os.ReadFile(filepath.Join(mountDir, "SKILL.md"))
		if err != nil {
			t.Fatalf("read mounted file: %v", err)
		}
		if string(content) != "# test" {
			t.Errorf("content = %q, want %q", string(content), "# test")
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		tmp := t.TempDir()
		runRoot := filepath.Join(tmp, "run")
		backingDir := filepath.Join(tmp, "backing")

		if err := os.MkdirAll(backingDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backingDir, "SKILL.md"), []byte("# test"), 0644); err != nil {
			t.Fatal(err)
		}

		sk := &ResolvedSkill{
			ID:         "idem-skill",
			BackingDir: backingDir,
			Files: []ResolvedFile{
				{Path: "SKILL.md", Content: []byte("# test")},
			},
		}

		dir1, err := EnsureMounted(runRoot, sk)
		if err != nil {
			t.Fatalf("first call failed: %v", err)
		}

		dir2, err := EnsureMounted(runRoot, sk)
		if err != nil {
			t.Fatalf("second call failed: %v", err)
		}

		if dir1 != dir2 {
			t.Errorf("idempotent check: first = %q, second = %q", dir1, dir2)
		}
	})

	t.Run("empty runRoot returns error", func(t *testing.T) {
		sk := &ResolvedSkill{
			ID:    "err-skill",
			Files: []ResolvedFile{{Path: "a.txt", Content: []byte("x")}},
		}

		_, err := EnsureMounted("", sk)
		if err == nil {
			t.Fatal("expected error for empty runRoot")
		}
	})

	t.Run("multiple files all copied", func(t *testing.T) {
		tmp := t.TempDir()
		runRoot := filepath.Join(tmp, "run")

		sk := &ResolvedSkill{
			ID: "multi-skill",
			// No BackingDir — forces file copy path
			Files: []ResolvedFile{
				{Path: "SKILL.md", Content: []byte("# skill")},
				{Path: "helper.py", Content: []byte("print('hi')")},
				{Path: "data.json", Content: []byte(`{"key":"val"}`)},
			},
		}

		mountDir, err := EnsureMounted(runRoot, sk)
		if err != nil {
			t.Fatalf("EnsureMounted failed: %v", err)
		}

		for _, f := range sk.Files {
			got, err := os.ReadFile(filepath.Join(mountDir, f.Path))
			if err != nil {
				t.Errorf("read %s: %v", f.Path, err)
				continue
			}
			if string(got) != string(f.Content) {
				t.Errorf("file %s: got %q, want %q", f.Path, string(got), string(f.Content))
			}
		}
	})

	t.Run("nested file paths create subdirectories", func(t *testing.T) {
		tmp := t.TempDir()
		runRoot := filepath.Join(tmp, "run")

		sk := &ResolvedSkill{
			ID: "nested-skill",
			Files: []ResolvedFile{
				{Path: "SKILL.md", Content: []byte("# root")},
				{Path: "subdir/file.txt", Content: []byte("nested content")},
				{Path: "a/b/deep.txt", Content: []byte("deep content")},
			},
		}

		mountDir, err := EnsureMounted(runRoot, sk)
		if err != nil {
			t.Fatalf("EnsureMounted failed: %v", err)
		}

		for _, f := range sk.Files {
			got, err := os.ReadFile(filepath.Join(mountDir, f.Path))
			if err != nil {
				t.Errorf("read %s: %v", f.Path, err)
				continue
			}
			if string(got) != string(f.Content) {
				t.Errorf("file %s: got %q, want %q", f.Path, string(got), string(f.Content))
			}
		}

		// Verify subdirectories were created
		info, err := os.Stat(filepath.Join(mountDir, "subdir"))
		if err != nil {
			t.Fatalf("subdir not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("subdir is not a directory")
		}

		info, err = os.Stat(filepath.Join(mountDir, "a", "b"))
		if err != nil {
			t.Fatalf("a/b not created: %v", err)
		}
		if !info.IsDir() {
			t.Error("a/b is not a directory")
		}
	})
}
