package tool

import (
	"fmt"
	"path/filepath"

	"github.com/sjzar/reed/internal/security"
)

// ResolvePath resolves rawPath relative to cwd, canonicalizes via EvalSymlinks
// on the deepest existing ancestor, and returns a clean absolute path.
func ResolvePath(cwd, rawPath string) (string, error) {
	if rawPath == "" {
		return "", fmt.Errorf("empty path")
	}
	var abs string
	if filepath.IsAbs(rawPath) {
		abs = rawPath
	} else {
		if cwd == "" {
			return "", fmt.Errorf("relative path %q with empty cwd", rawPath)
		}
		abs = filepath.Join(cwd, rawPath)
	}
	return security.Canonicalize(abs)
}

// LockKey returns a lock key for file-system operations on the given resolved path.
func LockKey(resolved string) string {
	return "fs:" + resolved
}
