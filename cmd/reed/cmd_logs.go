package reed

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	reedmgr "github.com/sjzar/reed/internal/reed"
)

var logsCmd = &cobra.Command{
	Use:   "logs <pid-or-process-id>",
	Short: "Read logs for a process. Workflow events by default; -f to stream in real-time.",
	Long: `Read logs for a process.

Use when: you need workflow events (step starts, completions, outputs, errors) or error
history for a process.
Do not use when: you need the current state of a process or run; use "reed status" instead.

The argument accepts either a numeric OS PID or a Reed ProcessID (e.g. proc_ab12cd34).
By default shows workflow event logs only.
Use "--process" to also include Reed's own process log lines.
Use "-f" to keep streaming new entries in real-time.

Examples:
  reed logs proc_ab12cd34
  reed logs proc_ab12cd34 -n 50
  reed logs proc_ab12cd34 -f
  reed logs proc_ab12cd34 --process`,
	Args:          requireArgs(1),
	RunE:          runLogs,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	logsCmd.Flags().IntP("tail", "n", 0, "show only the last N lines (0 means show all)")
	logsCmd.Flags().BoolP("follow", "f", false, "keep streaming new log entries in real-time; blocks until interrupted")
	logsCmd.Flags().Bool("process", false, "also include Reed process lifecycle logs, not just workflow event logs")
	rootCmd.AddCommand(logsCmd)
}

func runLogs(cmd *cobra.Command, args []string) error {
	return withDBManager(cmd, func(m *reedmgr.Manager) error {
		follow, _ := cmd.Flags().GetBool("follow")
		showProcess, _ := cmd.Flags().GetBool("process")
		tailN, _ := cmd.Flags().GetInt("tail")

		return m.ReadLogs(context.Background(), reedmgr.LogReadOpts{
			Target:         args[0],
			Follow:         follow,
			IncludeProcess: showProcess,
			TailN:          tailN,
			Writer:         os.Stdout,
		})
	})
}
