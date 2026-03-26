package reed

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sjzar/reed/internal/model"
	reedmgr "github.com/sjzar/reed/internal/reed"
)

// formatStatusText renders a StatusResult as human-readable text.
func formatStatusText(w io.Writer, result *reedmgr.StatusResult) error {
	if result.Run != nil {
		return renderActiveRunView(w, result.Run)
	}
	return renderStatusView(w, result)
}

func renderStatusView(w io.Writer, result *reedmgr.StatusResult) error {
	sv := result.Process
	fmt.Fprintf(w, "Process:   %s\n", sv.ProcessID)
	fmt.Fprintf(w, "PID:       %d\n", sv.PID)
	fmt.Fprintf(w, "Mode:      %s\n", sv.Mode)
	fmt.Fprintf(w, "Status:    %s\n", sv.Status)
	if sv.Uptime != "" {
		fmt.Fprintf(w, "Uptime:    %s\n", sv.Uptime)
	}
	fmt.Fprintf(w, "Created:   %s\n", sv.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	if result.Source != "" {
		fmt.Fprintf(w, "Source:    %s\n", result.Source)
	}

	if len(sv.Listeners) > 0 {
		fmt.Fprintln(w, "\nListeners:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  PROTOCOL\tADDRESS\tROUTES")
		for _, l := range sv.Listeners {
			fmt.Fprintf(tw, "  %s\t%s\t%d\n", l.Protocol, l.Address, l.RouteCount)
		}
		tw.Flush()
	}

	if len(sv.ActiveRuns) > 0 {
		fmt.Fprintln(w, "\nActive Runs:")
		for _, r := range sv.ActiveRuns {
			started := r.StartedAt.Local().Format("2006-01-02 15:04:05")
			fmt.Fprintf(w, "  %s  %s  started %s\n", r.RunID, r.Status, started)
			if len(r.Jobs) > 0 {
				renderJobs(w, r.Jobs)
			}
		}
	}

	if !result.IsLive {
		fmt.Fprintln(w, "\n(offline — showing last known state from database)")
	}
	return nil
}

func renderActiveRunView(w io.Writer, rv *model.ActiveRunView) error {
	fmt.Fprintf(w, "Run:       %s\n", rv.RunID)
	fmt.Fprintf(w, "Source:    %s\n", rv.WorkflowSource)
	fmt.Fprintf(w, "Status:    %s\n", rv.Status)
	fmt.Fprintf(w, "Started:   %s\n", rv.StartedAt.Local().Format("2006-01-02 15:04:05"))
	if rv.FinishedAt != nil {
		fmt.Fprintf(w, "Finished:  %s\n", rv.FinishedAt.Local().Format("2006-01-02 15:04:05"))
	}
	if len(rv.Jobs) > 0 {
		fmt.Fprintln(w, "\nJobs:")
		renderJobs(w, rv.Jobs)
	}
	return nil
}

// printRunSummary writes a concise run summary to w.
// Shows: status line, failed step details, and workflow outputs.
func printRunSummary(w io.Writer, rv reedmgr.TerminalRunInfo) {
	dur := ""
	if rv.FinishedAt != nil {
		dur = fmt.Sprintf("in %s", rv.FinishedAt.Sub(rv.StartedAt).Truncate(time.Millisecond))
	}

	fmt.Fprintf(w, "\n\n[%s] %s %s\n", rv.ID, rv.Status, dur)

	if rv.Status == model.RunCanceled {
		return
	}

	// Show failed steps with error + stderr
	for jobID, jv := range rv.Jobs {
		for stepID, sv := range jv.Steps {
			if sv.Status != model.StepFailed {
				continue
			}
			fmt.Fprintf(w, "FAILED %s/%s", jobID, stepID)
			if sv.ErrorMessage != "" {
				fmt.Fprintf(w, ": %s", sv.ErrorMessage)
			}
			fmt.Fprintln(w)
			if stderr, ok := sv.Outputs["stderr"]; ok {
				if s, ok := stderr.(string); ok && s != "" {
					fmt.Fprintf(w, "stderr: %s\n", strings.TrimRight(s, "\n"))
				}
			}
		}
	}

	// Show workflow outputs
	for k, v := range rv.Outputs {
		if v == nil {
			continue
		}
		fmt.Fprintf(w, "[%s]\n", k)
		fmt.Fprintf(w, "%v\n", v)
	}
}

func renderJobs(w io.Writer, jobs map[string]model.APIJobView) {
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "    JOB\tSTATUS")
	for _, id := range ids {
		j := jobs[id]
		fmt.Fprintf(tw, "    %s\t%s\n", id, j.Status)
	}
	tw.Flush()
}
