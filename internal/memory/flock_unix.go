//go:build !windows

package memory

import (
	"os"

	"golang.org/x/sys/unix"
)

// flockFile acquires an exclusive advisory lock on f.
func flockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

// funlockFile releases the advisory lock on f.
func funlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
