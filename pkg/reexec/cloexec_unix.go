//go:build !windows

package reexec

import "syscall"

// CloseOnExec sets the close-on-exec flag on the file descriptor.
func CloseOnExec(fd int) {
	syscall.CloseOnExec(fd)
}
