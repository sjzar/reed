package reed

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	reedmgr "github.com/sjzar/reed/internal/reed"
)

var statusCmd = &cobra.Command{
	Use:   "status <pid-or-process-id>",
	Short: "Show current status of a process, including its runs, jobs, and steps.",
	Long: `Show the current status of a process, or one active run inside it.

Use when: you need to know whether a process is running, inspect per-job/per-step statuses,
or get JSON output for programmatic use.
Do not use when: you need historical event output; use "reed logs" instead.

The argument accepts either a numeric OS PID or a Reed ProcessID (e.g. proc_ab12cd34).
Without "--run", returns process metadata plus all active runs.
With "--run <runID>", returns only that run (requires the process to be live).
If the process is no longer reachable, Reed shows the last known state from the database
and marks the output as offline.

Examples:
  reed status proc_ab12cd34
  reed status proc_ab12cd34 --json
  reed status proc_ab12cd34 --run run_ab12cd34`,
	Args:          requireArgs(1),
	RunE:          runStatus,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	statusCmd.Flags().Bool("json", false, "output JSON instead of human-readable text")
	statusCmd.Flags().StringP("run", "r", "", "show one active run by run ID (run_<hash>) instead of full process status; requires a live process")
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	return withDBManager(cmd, func(m *reedmgr.Manager) error {
		runID, _ := cmd.Flags().GetString("run")
		result, err := m.GetStatus(context.Background(), args[0], runID)
		if err != nil {
			return err
		}

		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			var v any
			if result.Run != nil {
				v = result.Run
			} else {
				v = result.Process
			}
			data, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		return formatStatusText(os.Stdout, result)
	})
}
