package reed

import (
	"fmt"

	"github.com/sjzar/reed/pkg/reexec"
)

// detachProcess re-executes the current command in the background.
// Delegates to reexec.Detach for the pipe-based readiness protocol.
func detachProcess(argv []string) error {
	pid, processID, err := reexec.Detach(argv)
	if err != nil {
		return err
	}
	fmt.Printf("detached: PID %d, ProcessID %s\n", pid, processID)
	return nil
}
