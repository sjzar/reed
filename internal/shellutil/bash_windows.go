//go:build windows

package shellutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveBash finds a bash binary on Windows.
// Checks known bash distributions (Git Bash, MSYS2, Cygwin) before PATH,
// and excludes the WSL bash launcher.
func ResolveBash() (string, error) {
	var candidates []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		candidates = append(candidates, filepath.Join(pf, "Git", "bin", "bash.exe"))
	}
	if pf86 := os.Getenv("ProgramFiles(x86)"); pf86 != "" {
		candidates = append(candidates, filepath.Join(pf86, "Git", "bin", "bash.exe"))
	}
	if sd := os.Getenv("SystemDrive"); sd != "" {
		candidates = append(candidates,
			filepath.Join(sd, "msys64", "usr", "bin", "bash.exe"),
			filepath.Join(sd, "cygwin64", "bin", "bash.exe"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	// Final fallback: check PATH (may find non-standard installations)
	if p, err := exec.LookPath("bash"); err == nil {
		if !IsWSLBash(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("bash not found: checked Git Bash, MSYS2, Cygwin, and PATH")
}

// IsWSLBash returns true if the path points to the Windows WSL bash launcher.
// WSL bash lives in System32/SysWOW64 and has incompatible path semantics.
func IsWSLBash(path string) bool {
	dir := strings.ToLower(filepath.Dir(path))
	return strings.HasSuffix(dir, `\system32`) || strings.HasSuffix(dir, `\syswow64`)
}
