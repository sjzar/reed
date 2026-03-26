//go:build !windows

package shellutil

import (
	"fmt"
	"os/exec"
)

// ResolveBash finds a bash binary. Returns error if bash is not found.
// On non-Windows systems, this is a simple LookPath.
func ResolveBash() (string, error) {
	p, err := exec.LookPath("bash")
	if err != nil {
		return "", fmt.Errorf("bash not found: %w", err)
	}
	return p, nil
}

// IsWSLBash is always false on non-Windows systems.
func IsWSLBash(_ string) bool {
	return false
}
