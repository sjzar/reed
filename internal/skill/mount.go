package skill

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureMounted ensures a resolved skill is mounted at <runRoot>/skills/<id>/.
// Uses symlink if possible, falls back to file copy. Idempotent.
// Returns the absolute mount directory path.
func EnsureMounted(runRoot string, sk *ResolvedSkill) (string, error) {
	if runRoot == "" {
		return "", fmt.Errorf("skill %q: runRoot is empty", sk.ID)
	}

	mountDir := filepath.Join(runRoot, "skills", sk.ID)

	// Idempotent: already mounted
	if _, err := os.Stat(mountDir); err == nil {
		return mountDir, nil
	}

	if err := os.MkdirAll(filepath.Dir(mountDir), 0755); err != nil {
		return "", fmt.Errorf("skill %q: create mount parent: %w", sk.ID, err)
	}

	// Try symlink first (fast, no copy)
	if sk.BackingDir != "" {
		if err := os.Symlink(sk.BackingDir, mountDir); err == nil {
			return mountDir, nil
		}
		// Symlink failed — fall through to copy
	}

	// Fallback: copy files
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return "", fmt.Errorf("skill %q: create mount dir: %w", sk.ID, err)
	}

	for _, f := range sk.Files {
		dst := filepath.Join(mountDir, f.Path)
		if dir := filepath.Dir(dst); dir != mountDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return "", fmt.Errorf("skill %q: create subdir for %s: %w", sk.ID, f.Path, err)
			}
		}
		if err := os.WriteFile(dst, f.Content, 0644); err != nil {
			return "", fmt.Errorf("skill %q: write %s: %w", sk.ID, f.Path, err)
		}
	}

	return mountDir, nil
}
