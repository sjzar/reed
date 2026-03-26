package reed

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	reedmgr "github.com/sjzar/reed/internal/reed"
)

var stopCmd = &cobra.Command{
	Use:   "stop <pid-or-process-id>",
	Short: "Stop a running process. Use --run to stop one active run without stopping the process.",
	Long: `Stop a running Reed process or cancel one active run inside it.

Use when: you want to stop a background process started by "reed run -d", or cancel one active run.
Do not use when: the target has already stopped; check "reed ps" first.

The argument accepts either a numeric OS PID or a Reed ProcessID (e.g. proc_ab12cd34).
Use "--run <runID>" only when you want to cancel one active run and keep the process alive.

Examples:
  reed stop proc_ab12cd34
  reed stop 54321
  reed stop proc_ab12cd34 --run run_ab12cd34`,
	Args:          requireArgs(1),
	RunE:          runStop,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	stopCmd.Flags().StringP("run", "r", "", "stop one active run (run_<hash>) in the target process and keep the process running")
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
	return withDBManager(cmd, func(m *reedmgr.Manager) error {
		runID, _ := cmd.Flags().GetString("run")
		if runID != "" {
			result, err := m.StopRun(context.Background(), args[0], runID)
			if err != nil {
				return err
			}
			fmt.Printf("run %s stopped in process %s\n", runID, result.ProcessID)
			return nil
		}

		result, err := m.StopProcess(context.Background(), args[0])
		if err != nil {
			return err
		}

		if result.Forced {
			fmt.Printf("process %s (PID %d) force killed after 3s grace\n", result.ProcessID, result.PID)
		} else {
			fmt.Printf("process %s (PID %d) stopped\n", result.ProcessID, result.PID)
		}
		return nil
	})
}
