package reed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sjzar/reed/internal/db"
	reedhttp "github.com/sjzar/reed/internal/http"
	"github.com/sjzar/reed/internal/model"
)

// signaler abstracts syscall.Kill for testability.
type signaler interface {
	Signal(pid int, sig syscall.Signal) error
}

// osSignaler is defined in signal_unix.go / signal_windows.go.

// StatusResult holds the status query outcome.
// Exactly one of Process or Run is non-nil.
type StatusResult struct {
	Process *model.StatusView    // non-nil for process-level queries
	Run     *model.ActiveRunView // non-nil for run-level queries (--run flag)
	IsLive  bool                 // true if data came from IPC (process is running)
	Source  string               // workflow source (always populated from DB row)
}

// StopResult holds the outcome of a stop operation.
type StopResult struct {
	ProcessID string
	PID       int
	Forced    bool
}

func resolveTarget(ctx context.Context, repo *db.ProcessRepo, target string) (*model.ProcessRow, error) {
	if n, err := strconv.Atoi(target); err == nil {
		row, err := repo.FindByPIDLatest(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("no process found with PID %d; run \"reed ps --all\" to list known processes", n)
		}
		return row, nil
	}
	if strings.HasPrefix(target, "proc_") {
		row, err := repo.FindByID(ctx, target)
		if err != nil {
			return nil, fmt.Errorf("process %q not found; run \"reed ps --all\" to list known processes", target)
		}
		return row, nil
	}
	return nil, fmt.Errorf("invalid target %q: expected a PID like 12345 or a process ID like proc_ab12cd34; run \"reed ps\" to find valid targets", target)
}

// isProcessAlive is defined in signal_unix.go / signal_windows.go.

func isActiveStatus(status string) bool {
	return status == string(model.ProcessStarting) || status == string(model.ProcessRunning)
}

// GetStatus resolves the target, tries IPC for live data, falls back to DB.
// If runID is non-empty, queries a specific run via IPC (requires live process).
func (m *Manager) GetStatus(ctx context.Context, target, runID string) (*StatusResult, error) {
	repo, err := m.processRepo()
	if err != nil {
		return nil, err
	}
	row, err := resolveTarget(ctx, repo, target)
	if err != nil {
		return nil, err
	}

	sockPath := m.cfg.SockPath(row.ID)
	client := reedhttp.Dial(sockPath)

	var result *StatusResult
	if runID != "" {
		result, err = m.getRunStatusIPC(client, row, runID)
	} else {
		result, err = m.getProcessStatus(client, row)
	}
	if err != nil {
		return nil, err
	}
	result.Source = row.WorkflowSource
	return result, nil
}

// getProcessStatus tries IPC for live StatusView, falls back to building one from the DB row.
// Falls back only on transport failure (process unreachable). Server errors and decode failures
// are reported as errors so the user sees the real problem.
func (m *Manager) getProcessStatus(client *http.Client, row *model.ProcessRow) (*StatusResult, error) {
	resp, err := client.Get("http://localhost/v1/status")
	if err != nil {
		// Transport failure — process not reachable, fall back to DB.
		return &StatusResult{Process: statusViewFromRow(row)}, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not read status for process %s: %s; run \"reed logs %s --process\" for detail", row.ID, extractErrorMessage(body), row.ID)
	}

	var envelope struct {
		Data model.StatusView `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("process %s returned unreadable status data; try again or run \"reed logs %s --process\" for detail", row.ID, row.ID)
	}
	return &StatusResult{Process: &envelope.Data, IsLive: true}, nil
}

// getRunStatusIPC queries a specific run via IPC. Returns an error if the process is not reachable.
func (m *Manager) getRunStatusIPC(client *http.Client, row *model.ProcessRow, runID string) (*StatusResult, error) {
	url := fmt.Sprintf("http://localhost/v1/runs/%s", runID)
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("process %s is not running; run \"reed ps --all\" to confirm, or omit \"--run\" to view last known process status", row.ID)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("could not read run %s from process %s: %s; run \"reed status %s\" to inspect the process", runID, row.ID, extractErrorMessage(body), row.ID)
	}

	var envelope struct {
		Data model.ActiveRunView `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("process %s returned unreadable data for run %s; try again or run \"reed status %s\" without \"--run\"", row.ID, runID, row.ID)
	}
	return &StatusResult{Run: &envelope.Data, IsLive: true}, nil
}

// statusViewFromRow builds a StatusView from a DB ProcessRow (offline fallback).
func statusViewFromRow(row *model.ProcessRow) *model.StatusView {
	return &model.StatusView{
		ProcessID: row.ID,
		PID:       row.PID,
		Mode:      row.Mode,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
	}
}

// extractErrorMessage parses a JSON error envelope or falls back to raw body text.
func extractErrorMessage(body []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Message != "" {
		return envelope.Message
	}
	return strings.TrimSpace(string(body))
}

// StopRun resolves the target, sends a stop request for a specific run via IPC.
func (m *Manager) StopRun(ctx context.Context, target, runID string) (*StopResult, error) {
	repo, err := m.processRepo()
	if err != nil {
		return nil, err
	}
	row, err := resolveTarget(ctx, repo, target)
	if err != nil {
		return nil, err
	}

	sockPath := m.cfg.SockPath(row.ID)
	client := reedhttp.Dial(sockPath)
	url := fmt.Sprintf("http://localhost/v1/runs/%s/stop", runID)
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return nil, fmt.Errorf("process %s is not running; run \"reed ps --all\" to confirm", row.ID)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("run %s not found or already finished in process %s; run \"reed status %s\" to check active runs", runID, row.ID, row.ID)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("could not stop run %s in process %s: %s; run \"reed status %s\" to inspect active runs", runID, row.ID, extractErrorMessage(body), row.ID)
	}

	return &StopResult{ProcessID: row.ID, PID: row.PID}, nil
}

// StopProcess resolves the target, sends SIGTERM, waits up to 3s, then SIGKILL if needed.
func (m *Manager) StopProcess(ctx context.Context, target string) (*StopResult, error) {
	repo, err := m.processRepo()
	if err != nil {
		return nil, err
	}
	row, err := resolveTarget(ctx, repo, target)
	if err != nil {
		return nil, err
	}

	if err := m.signaler.Signal(row.PID, syscall.SIGTERM); err != nil {
		return nil, fmt.Errorf("could not stop process %s (PID %d): %w; it may already be stopped. Run \"reed ps --all\" to confirm", row.ID, row.PID, err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := m.signaler.Signal(row.PID, 0); err != nil {
			return &StopResult{ProcessID: row.ID, PID: row.PID, Forced: false}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = m.signaler.Signal(row.PID, syscall.SIGKILL)
	return &StopResult{ProcessID: row.ID, PID: row.PID, Forced: true}, nil
}

// ListProcesses returns processes, optionally including inactive ones.
// Dead processes are lazily marked as STOPPED in the DB.
func (m *Manager) ListProcesses(ctx context.Context, showAll bool) ([]*model.ProcessRow, error) {
	repo, err := m.processRepo()
	if err != nil {
		return nil, err
	}
	var rows []*model.ProcessRow
	if showAll {
		rows, err = repo.ListAll(ctx)
	} else {
		rows, err = repo.ListActive(ctx)
	}
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		if !isActiveStatus(row.Status) {
			continue
		}
		if !isProcessAlive(row.PID) {
			_ = repo.UpdateStatus(ctx, row.ID, string(model.ProcessStopped), "{}")
			row.Status = string(model.ProcessStopped)
		}
	}
	return rows, nil
}
