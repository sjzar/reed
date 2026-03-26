package reed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/model"
)

// --- helpers ---

func insertRow(t *testing.T, repo *db.ProcessRepo, id string, pid int, status string) {
	t.Helper()
	now := time.Now().UTC()
	err := repo.Insert(context.Background(), &model.ProcessRow{
		ID: id, PID: pid,
		Mode: "cli", Status: status, WorkflowSource: "test.yaml",
		CreatedAt: now, UpdatedAt: now, MetadataJSON: "{}",
	})
	if err != nil {
		t.Fatalf("insert row: %v", err)
	}
}

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- resolveTarget ---

func TestResolveTarget_PID(t *testing.T) {
	d := openTestDB(t)
	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 1234, string(model.ProcessRunning))

	row, err := resolveTarget(context.Background(), repo, "1234")
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if row.ID != "proc_test_0001" {
		t.Errorf("got ID=%q, want proc_test_0001", row.ID)
	}
}

func TestResolveTarget_ProcessID(t *testing.T) {
	d := openTestDB(t)
	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 5678, string(model.ProcessRunning))

	row, err := resolveTarget(context.Background(), repo, "proc_test_0001")
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if row.PID != 5678 {
		t.Errorf("got PID=%d, want 5678", row.PID)
	}
}

func TestResolveTarget_Invalid(t *testing.T) {
	d := openTestDB(t)
	repo := db.NewProcessRepo(d)

	_, err := resolveTarget(context.Background(), repo, "foobar")
	if err == nil {
		t.Fatal("expected error for invalid target")
	}
}

// --- isActiveStatus ---

func TestIsActiveStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{string(model.ProcessStarting), true},
		{string(model.ProcessRunning), true},
		{string(model.ProcessStopped), false},
		{string(model.ProcessFailed), false},
	}
	for _, tt := range tests {
		if got := isActiveStatus(tt.status); got != tt.want {
			t.Errorf("isActiveStatus(%q) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// --- GetStatus ---

func TestGetStatus_SQLiteFallback(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 99999, string(model.ProcessRunning))

	result, err := m.GetStatus(context.Background(), "proc_test_0001", "")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if result.IsLive {
		t.Error("expected IsLive=false for nonexistent socket")
	}
	if result.Process == nil {
		t.Fatal("expected Process to be non-nil")
	}
	if result.Process.ProcessID != "proc_test_0001" {
		t.Errorf("ProcessID = %q, want proc_test_0001", result.Process.ProcessID)
	}
}

func TestGetStatus_LiveIPC(t *testing.T) {
	// Use /tmp for short socket path (Unix domain sockets have ~104 char limit)
	dir, err := os.MkdirTemp("", "reed-ipc")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dir)

	// cfg.SockPath("proc_test_0001") = <Home>/socks/proc_test_0001.sock
	// So we create the socks dir and listen there.
	socksDir := filepath.Join(dir, "socks")
	os.MkdirAll(socksDir, 0o755)
	sockPath := filepath.Join(socksDir, "proc_test_0001.sock")

	// Start a real UDS HTTP server
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"data": map[string]any{
				"processID": "proc_test_0001",
				"pid":       os.Getpid(),
				"mode":      "CLI",
				"status":    "RUNNING",
			},
		})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", os.Getpid(), string(model.ProcessRunning))

	result, err := m.GetStatus(context.Background(), "proc_test_0001", "")
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !result.IsLive {
		t.Error("expected IsLive=true for live IPC")
	}
	if result.Process == nil {
		t.Fatal("expected Process to be non-nil")
	}
	if result.Process.Status != "RUNNING" {
		t.Errorf("Status = %q, want RUNNING", result.Process.Status)
	}
	if result.Source != "test.yaml" {
		t.Errorf("Source = %q, want test.yaml", result.Source)
	}
}

func TestGetStatus_LiveIPC_DecodeError(t *testing.T) {
	dir, err := os.MkdirTemp("", "reed-ipc")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dir)

	socksDir := filepath.Join(dir, "socks")
	os.MkdirAll(socksDir, 0o755)
	sockPath := filepath.Join(socksDir, "proc_test_0001.sock")

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", os.Getpid(), string(model.ProcessRunning))

	_, err = m.GetStatus(context.Background(), "proc_test_0001", "")
	if err == nil {
		t.Fatal("expected status decode error, got nil")
	}
	if !strings.Contains(err.Error(), "returned unreadable status data") {
		t.Errorf("expected 'returned unreadable status data' in error, got %q", err.Error())
	}
}

// --- ReadLogs ---

func TestReadLogs_Dump(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 1234, string(model.ProcessRunning))

	// Write a fake event log
	logDir := cfg.LogDir()
	logPath := filepath.Join(logDir, "proc_test_0001.events.jsonl")
	os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0o644)

	var buf bytes.Buffer
	err = m.ReadLogs(context.Background(), LogReadOpts{
		Target: "proc_test_0001",
		Writer: &buf,
	})
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected output, got empty")
	}
}

func TestReadLogs_TailN(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 1234, string(model.ProcessRunning))

	logDir := cfg.LogDir()
	logPath := filepath.Join(logDir, "proc_test_0001.events.jsonl")
	os.WriteFile(logPath, []byte("a\nb\nc\nd\ne\n"), 0o644)

	var buf bytes.Buffer
	err = m.ReadLogs(context.Background(), LogReadOpts{
		Target: "proc_test_0001",
		TailN:  2,
		Writer: &buf,
	})
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2", len(lines))
	}
}

func TestReadLogs_MissingFile(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 1234, string(model.ProcessRunning))

	var buf bytes.Buffer
	err = m.ReadLogs(context.Background(), LogReadOpts{
		Target: "proc_test_0001",
		Writer: &buf,
	})
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output for missing file, got %d bytes", buf.Len())
	}
}

func TestReadLogs_IncludeProcess(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 1234, string(model.ProcessRunning))

	logDir := cfg.LogDir()
	os.WriteFile(filepath.Join(logDir, "proc_test_0001.events.jsonl"), []byte("event1\n"), 0o644)
	os.WriteFile(filepath.Join(logDir, "proc_test_0001.log"), []byte("process1\n"), 0o644)

	var buf bytes.Buffer
	err = m.ReadLogs(context.Background(), LogReadOpts{
		Target:         "proc_test_0001",
		IncludeProcess: true,
		Writer:         &buf,
	})
	if err != nil {
		t.Fatalf("ReadLogs: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Errorf("got %d lines, want 2 (event + process)", len(lines))
	}
}

// --- ListProcesses ---

func TestListProcesses_ActiveOnly(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	// Use current PID so isProcessAlive returns true
	insertRow(t, repo, "proc_running", os.Getpid(), string(model.ProcessRunning))
	insertRow(t, repo, "proc_stopped", 1, string(model.ProcessStopped))

	rows, err := m.ListProcesses(context.Background(), false)
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ID != "proc_running" {
		t.Errorf("got ID=%q, want proc_running", rows[0].ID)
	}
}

func TestListProcesses_ShowAll(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_running", os.Getpid(), string(model.ProcessRunning))
	insertRow(t, repo, "proc_stopped", 1, string(model.ProcessStopped))

	rows, err := m.ListProcesses(context.Background(), true)
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestListProcesses_LazyStale(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	repo := db.NewProcessRepo(d)
	// PID 999999999 should not exist
	insertRow(t, repo, "proc_dead", 999999999, string(model.ProcessRunning))

	rows, err := m.ListProcesses(context.Background(), false)
	if err != nil {
		t.Fatalf("ListProcesses: %v", err)
	}
	// Should have been flipped to STOPPED
	for _, row := range rows {
		if row.ID == "proc_dead" && row.Status != string(model.ProcessStopped) {
			t.Errorf("dead process status = %q, want STOPPED", row.Status)
		}
	}
}

// --- StopProcess (with signaler seam) ---

type mockSignaler struct {
	calls []mockSignalCall
	// probeAlwaysAlive: if true, Signal(pid, 0) always returns nil (process alive)
	probeAlwaysAlive bool
	probeCount       int
}

type mockSignalCall struct {
	PID int
	Sig syscall.Signal
}

func (s *mockSignaler) Signal(pid int, sig syscall.Signal) error {
	s.calls = append(s.calls, mockSignalCall{PID: pid, Sig: sig})
	if sig == 0 {
		s.probeCount++
		if s.probeAlwaysAlive {
			return nil
		}
		return fmt.Errorf("no such process")
	}
	return nil
}

func TestStopProcess_Graceful(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d
	sig := &mockSignaler{probeAlwaysAlive: false}
	m.signaler = sig

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 12345, string(model.ProcessRunning))

	result, err := m.StopProcess(context.Background(), "proc_test_0001")
	if err != nil {
		t.Fatalf("StopProcess: %v", err)
	}
	if result.Forced {
		t.Error("expected Forced=false for graceful stop")
	}
	// Should have sent SIGTERM then probe
	if len(sig.calls) < 2 {
		t.Fatalf("expected at least 2 signal calls, got %d", len(sig.calls))
	}
	if sig.calls[0].Sig != syscall.SIGTERM {
		t.Errorf("first signal = %v, want SIGTERM", sig.calls[0].Sig)
	}
}

func TestStopProcess_ForceKill(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d
	sig := &mockSignaler{probeAlwaysAlive: true}
	m.signaler = sig

	repo := db.NewProcessRepo(d)
	insertRow(t, repo, "proc_test_0001", 12345, string(model.ProcessRunning))

	result, err := m.StopProcess(context.Background(), "proc_test_0001")
	if err != nil {
		t.Fatalf("StopProcess: %v", err)
	}
	if !result.Forced {
		t.Error("expected Forced=true when process stays alive")
	}
	// Verify SIGKILL was sent as the last signal
	last := sig.calls[len(sig.calls)-1]
	if last.Sig != syscall.SIGKILL {
		t.Errorf("last signal = %v, want SIGKILL", last.Sig)
	}
}

func TestStopProcess_ResolveError(t *testing.T) {
	dir := t.TempDir()
	d := openTestDB(t)
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m.db = d

	_, err = m.StopProcess(context.Background(), "foobar")
	if err == nil {
		t.Fatal("expected error for invalid target")
	}
}
