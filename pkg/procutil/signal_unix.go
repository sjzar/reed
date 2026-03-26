//go:build !windows

package procutil

import (
	"errors"
	"fmt"
	"syscall"
)

// IsAlive reports whether a process with the given PID exists.
// Returns true when the process exists but is not signalable (EPERM).
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// Terminate sends SIGTERM to the process.
func Terminate(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

// Kill sends SIGKILL to the process.
func Kill(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

// Signal sends an arbitrary signal to the process.
func Signal(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	return syscall.Kill(pid, sig)
}
