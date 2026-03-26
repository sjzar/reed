//go:build !windows

package procutil

import (
	"os"
	"testing"
)

func TestIsAlive_Self(t *testing.T) {
	if !IsAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}
}

func TestIsAlive_Invalid(t *testing.T) {
	if IsAlive(0) {
		t.Error("expected pid 0 to not be alive")
	}
	if IsAlive(-1) {
		t.Error("expected pid -1 to not be alive")
	}
}
