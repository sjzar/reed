package reed

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	reedmgr "github.com/sjzar/reed/internal/reed"
)

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List Reed processes. Shows running processes by default; use --all to include completed and stopped.",
	Long: `List Reed processes.

Use when: you need to find a process ID or PID for use with "reed status", "reed logs",
or "reed stop", or to see which workflows are running.
Do not use when: you need detailed status for one process; use "reed status" instead.

Output columns: PROCESS ID, PID, MODE (cli/service/schedule), STATUS, WORKFLOW, CREATED.
By default, shows running processes only. Use "--all" to include completed and stopped processes.

Examples:
  reed ps
  reed ps --all`,
	RunE: runPS,
}

func init() {
	psCmd.Flags().Bool("all", false, "include completed and stopped processes in output")
	rootCmd.AddCommand(psCmd)
}

func runPS(cmd *cobra.Command, args []string) error {
	return withDBManager(cmd, func(m *reedmgr.Manager) error {
		ctx := context.Background()
		showAll, _ := cmd.Flags().GetBool("all")
		rows, err := m.ListProcesses(ctx, showAll)
		if err != nil {
			return err
		}

		m.CleanStale()

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROCESS ID\tPID\tMODE\tSTATUS\tWORKFLOW\tCREATED")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n",
				r.ID, r.PID, r.Mode, r.Status,
				r.WorkflowSource, r.CreatedAt.Local().Format("2006-01-02 15:04:05"))
		}
		return w.Flush()
	})
}
