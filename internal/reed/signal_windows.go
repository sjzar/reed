//go:build windows

package reed

import (
	"syscall"

	"github.com/sjzar/reed/pkg/procutil"
)

type osSignaler struct{}

func (osSignaler) Signal(pid int, sig syscall.Signal) error { return procutil.Signal(pid, sig) }

func isProcessAlive(pid int) bool { return procutil.IsAlive(pid) }
