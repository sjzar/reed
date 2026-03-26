//go:build !windows

package shellutil

import (
	"os/exec"
	"testing"
)

func TestResolveBash_Found(t *testing.T) {
	// On macOS/Linux, bash should be available
	p, err := ResolveBash()
	if err != nil {
		t.Skipf("bash not found on this system: %v", err)
	}
	if p == "" {
		t.Fatal("ResolveBash returned empty path")
	}
	// Verify the path is actually executable
	if _, err := exec.LookPath(p); err != nil {
		t.Errorf("resolved path %q is not executable: %v", p, err)
	}
}

func TestIsWSLBash_AlwaysFalse(t *testing.T) {
	// On non-Windows, IsWSLBash should always return false
	if IsWSLBash("/usr/bin/bash") {
		t.Error("IsWSLBash should be false on non-Windows")
	}
	if IsWSLBash("/bin/bash") {
		t.Error("IsWSLBash should be false on non-Windows")
	}
}
