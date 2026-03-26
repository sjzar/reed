package reed

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

func TestNew_NoOptions(t *testing.T) {
	cfg := &conf.Config{Home: t.TempDir()}
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.db != nil {
		t.Error("db should be nil without WithDB")
	}
	if m.engine != nil {
		t.Error("engine should be nil without WithEngine")
	}
}

func TestNew_WithDB(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	if m.db == nil {
		t.Error("db should not be nil with WithDB")
	}
}

func TestNew_WithDBAndEngine(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	if m.db == nil {
		t.Error("db should not be nil")
	}
	if m.engineWorker == nil {
		t.Error("engineWorker should not be nil")
	}
}

func TestNew_WithWorkflow(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	wf := &model.Workflow{
		Agents: map[string]model.AgentSpec{
			"coder": {Model: "mock/test", SystemPrompt: "You are a coder."},
		},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
		Jobs:       map[string]model.Job{},
	}

	m, err := New(cfg, WithDB(), WithWorkflow(wf, dir))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	if m.db == nil {
		t.Error("db should not be nil")
	}
	if m.engineWorker == nil {
		t.Error("engineWorker should not be nil with WithWorkflow")
	}
	if m.mcpPool == nil {
		t.Error("mcpPool should not be nil with WithWorkflow")
	}
}

func TestOpenRuntime_WithoutEngine(t *testing.T) {
	cfg := &conf.Config{Home: t.TempDir()}
	m, err := New(cfg, WithDB())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	m.wf = &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}

	_, err = m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err == nil {
		t.Fatal("expected error for OpenRuntime without WithEngine/WithWorkflow")
	}
}

func TestOpenRuntime_PersistsProcessRow(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	m.wf = &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}

	processID, err := m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	// Verify DB has the process row with RUNNING status
	repo := db.NewProcessRepo(m.db)
	row, err := repo.FindByID(context.Background(), processID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if row.Status != string(model.ProcessRunning) {
		t.Errorf("status = %q, want RUNNING", row.Status)
	}
	if row.Mode != string(model.ProcessModeCLI) {
		t.Errorf("mode = %q, want cli", row.Mode)
	}
}

func TestShutdown_PersistsFinalStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.wf = &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}}}},
		},
	}

	processID, err := m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	// Submit and wait
	req := &model.RunRequest{
		Workflow:       m.wf,
		WorkflowSource: "test.yaml",
		TriggerType:    model.TriggerCLI,
		Inputs:         make(map[string]any),
		Outputs:        make(map[string]string),
		Env:            make(map[string]string),
		Secrets:        make(map[string]string),
	}
	handle, _ := m.engine.Submit(req)
	m.engine.WaitRun(context.Background(), handle.ID())

	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Re-open DB to verify persisted status
	d, err := db.Open(cfg.DBDir())
	if err != nil {
		t.Fatalf("re-open DB: %v", err)
	}
	defer d.Close()
	repo := db.NewProcessRepo(d)
	row, err := repo.FindByID(context.Background(), processID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if row.Status != string(model.ProcessStopped) {
		t.Errorf("status = %q, want STOPPED", row.Status)
	}
}

func TestShutdown_FailedRunPersistsFailedStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.wf = &model.Workflow{
		Name: "fail-test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"run": "exit 1"}}}},
		},
	}

	processID, err := m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	req := &model.RunRequest{
		Workflow: m.wf, WorkflowSource: "test.yaml", TriggerType: model.TriggerCLI,
		Inputs: make(map[string]any), Outputs: make(map[string]string),
		Env: make(map[string]string), Secrets: make(map[string]string),
	}
	handle, _ := m.engine.Submit(req)
	m.engine.WaitRun(context.Background(), handle.ID())

	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	d, err := db.Open(cfg.DBDir())
	if err != nil {
		t.Fatalf("re-open DB: %v", err)
	}
	defer d.Close()
	repo := db.NewProcessRepo(d)
	row, err := repo.FindByID(context.Background(), processID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if row.Status != string(model.ProcessFailed) {
		t.Errorf("status = %q, want FAILED", row.Status)
	}
}

func TestShutdown_CanceledRunPersistsStoppedStatus(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	m.wf = &model.Workflow{
		Name: "cancel-test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"run": "sleep 60"}}}},
		},
	}

	processID, err := m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	req := &model.RunRequest{
		Workflow: m.wf, WorkflowSource: "test.yaml", TriggerType: model.TriggerCLI,
		Inputs: make(map[string]any), Outputs: make(map[string]string),
		Env: make(map[string]string), Secrets: make(map[string]string),
	}
	m.engine.Submit(req)

	// Let the run start, then stop it (will be CANCELED)
	time.Sleep(50 * time.Millisecond)
	m.engine.StopAll()

	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	d, err := db.Open(cfg.DBDir())
	if err != nil {
		t.Fatalf("re-open DB: %v", err)
	}
	defer d.Close()
	repo := db.NewProcessRepo(d)
	row, err := repo.FindByID(context.Background(), processID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	// CANCELED runs should NOT set process to FAILED — they map to STOPPED
	if row.Status != string(model.ProcessStopped) {
		t.Errorf("status = %q, want STOPPED (canceled runs are not failures)", row.Status)
	}
}

func TestOpenRuntime_EngineFailure_RollsBackDB(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg, WithDB(), WithEngine())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	// nil workflow will cause engine.New to fail
	m.wf = nil

	_, err = m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}

	// Verify the orphan DB row was marked FAILED, not left as RUNNING
	repo := db.NewProcessRepo(m.db)
	rows, err := repo.ListActive(context.Background())
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	for _, row := range rows {
		if row.Status == string(model.ProcessRunning) {
			t.Errorf("found orphan RUNNING row %q after engine.New failure", row.ID)
		}
	}
}

func TestOpenRuntime_NilDB(t *testing.T) {
	cfg := &conf.Config{Home: t.TempDir()}
	// Create manager without DB, but with a manual engine worker
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	// Manually set engine worker (bypass WithEngine which requires DB)
	m.engineWorker = &nopWorker{}
	m.wf = &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{
			"build": {ID: "build", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}

	// Should succeed without DB
	processID, err := m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime without DB: %v", err)
	}
	if processID == "" {
		t.Error("expected non-empty processID even without DB")
	}
}

// nopWorker is a minimal Worker for nil-DB tests.
type nopWorker struct{}

func (nopWorker) Execute(_ context.Context, p engine.StepPayload) engine.StepRunResult {
	return engine.StepRunResult{
		StepRunID: p.StepRunID, JobID: p.JobID, StepID: p.StepID,
		Status: model.StepSucceeded,
	}
}

func TestShutdown_NilSubsystems(t *testing.T) {
	cfg := &conf.Config{Home: t.TempDir()}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Shutdown with everything nil should not panic
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestInitEventLog(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := m.initEventLog("test_001"); err != nil {
		t.Fatalf("initEventLog: %v", err)
	}
	if m.eventLog == nil {
		t.Error("eventLog should not be nil after init")
	}
	m.eventLog.Close()
}

// --- GC tests ---

func TestCleanStaleFiles(t *testing.T) {
	dir := t.TempDir()
	log := zerolog.Nop()

	// Create a file with old mod time
	oldFile := filepath.Join(dir, "old.log")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Set mod time to 2 days ago
	twoDAysAgo := time.Now().Add(-48 * time.Hour)
	os.Chtimes(oldFile, twoDAysAgo, twoDAysAgo)

	// Create a fresh file
	newFile := filepath.Join(dir, "new.log")
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	CleanStaleFiles(dir, 24*time.Hour, log)

	// Old file should be removed
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should be removed")
	}
	// New file should remain
	if _, err := os.Stat(newFile); err != nil {
		t.Error("new file should not be removed")
	}
}

func TestCleanStaleFiles_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	log := zerolog.Nop()

	subDir := filepath.Join(dir, "subdir")
	os.Mkdir(subDir, 0o755)

	CleanStaleFiles(dir, 0, log)

	// Subdirectory should not be removed
	if _, err := os.Stat(subDir); err != nil {
		t.Error("subdirectory should not be removed")
	}
}

func TestCleanStaleSockets(t *testing.T) {
	dir := t.TempDir()
	log := zerolog.Nop()

	// Create socket files
	activeSocket := filepath.Join(dir, "proc_active_001.sock")
	staleSocket := filepath.Join(dir, "proc_stale_001.sock")
	nonSocket := filepath.Join(dir, "other.txt")

	os.WriteFile(activeSocket, []byte{}, 0o644)
	os.WriteFile(staleSocket, []byte{}, 0o644)
	os.WriteFile(nonSocket, []byte{}, 0o644)

	lookup := &mockLookup{
		active: map[string]bool{
			"proc_active_001": true,
			"proc_stale_001":  false,
		},
	}

	CleanStaleSockets(dir, lookup, log)

	// Active socket should remain
	if _, err := os.Stat(activeSocket); err != nil {
		t.Error("active socket should remain")
	}
	// Stale socket should be removed
	if _, err := os.Stat(staleSocket); !os.IsNotExist(err) {
		t.Error("stale socket should be removed")
	}
	// Non-socket file should remain
	if _, err := os.Stat(nonSocket); err != nil {
		t.Error("non-socket file should remain")
	}
}

func TestCleanStaleSockets_NonExistentDir(t *testing.T) {
	log := zerolog.Nop()
	// Should not panic with non-existent dir
	CleanStaleSockets("/nonexistent/path", &mockLookup{}, log)
}

// mockLookup implements ProcessLookup for testing.
type mockLookup struct {
	active map[string]bool
}

func (m *mockLookup) FindByID(_ context.Context, id string) (bool, error) {
	active, ok := m.active[id]
	if !ok {
		return false, nil
	}
	return active, nil
}
