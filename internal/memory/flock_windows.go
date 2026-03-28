package memory

import (
	"os"

	"golang.org/x/sys/windows"
)

// flockFile acquires an exclusive lock on f using Windows LockFileEx.
func flockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol)
}

// funlockFile releases the lock on f using Windows UnlockFileEx.
func funlockFile(f *os.File) error {
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	return windows.UnlockFileEx(h, 0, 1, 0, ol)
}
