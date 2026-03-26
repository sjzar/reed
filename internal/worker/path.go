package worker

import (
	"fmt"
	"path/filepath"
)

// resolveAbsPath converts dir to an absolute path and attempts to resolve
// symlinks on a best-effort basis. If symlink resolution fails (e.g. the
// target does not exist), the absolute path is returned without error.
func resolveAbsPath(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		dir = abs
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir, nil
}

// resolveWorkDir resolves dir to an absolute, symlink-resolved path
// with a descriptive error wrapper for the given worker context.
func resolveWorkDir(dir, workerName string) (string, error) {
	resolved, err := resolveAbsPath(dir)
	if err != nil {
		return "", fmt.Errorf("%s: bad workdir: %w", workerName, err)
	}
	return resolved, nil
}
