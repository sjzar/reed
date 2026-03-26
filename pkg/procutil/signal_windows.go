//go:build windows

package procutil

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// IsAlive reports whether a process with the given PID exists.
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return checkAlive(pid) == nil
}

// Terminate sends a termination request to the process.
// On Windows, this maps to TerminateProcess (no graceful signal available).
func Terminate(pid int) error {
	return terminateProcess(pid)
}

// Kill forcefully terminates the process.
// On Windows, this is identical to Terminate.
func Kill(pid int) error {
	return terminateProcess(pid)
}

// Signal sends an arbitrary signal to the process.
// sig == 0 checks liveness; any other signal terminates.
func Signal(pid int, sig syscall.Signal) error {
	if sig == 0 {
		return checkAlive(pid)
	}
	return terminateProcess(pid)
}

func terminateProcess(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func checkAlive(pid int) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
		return err
	}
	windows.CloseHandle(h)
	return nil
}
