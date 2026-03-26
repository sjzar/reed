package reed

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/conf"
	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/model"
)

// mockResolver implements RunResolver for testing.
type mockResolver struct{}

func (mockResolver) ResolveRunRequest(_ context.Context, wf *model.Workflow, wfSource string, _ model.TriggerParams) (*model.RunRequest, error) {
	return &model.RunRequest{
		Workflow:       wf,
		WorkflowSource: wfSource,
		TriggerType:    model.TriggerCLI,
		Inputs:         make(map[string]any),
		Outputs:        make(map[string]string),
		Env:            wf.Env,
		Secrets:        make(map[string]string),
	}, nil
}

func TestOpenRuntime(t *testing.T) {
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
	if processID == "" {
		t.Error("expected non-empty processID")
	}
	if m.ProcessID() != processID {
		t.Errorf("ProcessID() = %q, want %q", m.ProcessID(), processID)
	}
	// Verify process is in DB
	repo := db.NewProcessRepo(m.db)
	row, err := repo.FindByID(context.Background(), processID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if row.Status != string(model.ProcessRunning) {
		t.Errorf("DB status = %q, want RUNNING", row.Status)
	}
}

func TestInitRuntime_CLIMode(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}

	wf := &model.Workflow{
		Name:       "cli-test",
		Agents:     map[string]model.AgentSpec{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}

	m, err := New(cfg,
		WithDB(),
		WithEngine(),
		WithResolver(mockResolver{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	m.wf = wf
	_, err = m.OpenRuntime(context.Background(), model.ProcessModeCLI, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	req := &model.RunRequest{
		Workflow:       wf,
		WorkflowSource: "test.yaml",
		TriggerType:    model.TriggerCLI,
		Inputs:         make(map[string]any),
		Outputs:        make(map[string]string),
		Env:            make(map[string]string),
		Secrets:        make(map[string]string),
	}

	err = m.InitRuntime(context.Background(), req, model.ProcessModeCLI)
	if err != nil {
		t.Fatalf("InitRuntime: %v", err)
	}

	// Wait for run to complete
	m.engine.Seal()
	<-m.engine.DoneCh()

	snap := m.engine.Snapshot()
	if len(snap.TerminalRuns) != 1 {
		t.Fatalf("TerminalRuns = %d, want 1", len(snap.TerminalRuns))
	}
}

func TestInitRuntime_ServiceMode(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}

	wf := &model.Workflow{
		Name:       "svc-test",
		Source:     "test.yaml",
		Agents:     map[string]model.AgentSpec{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
		On: model.OnSpec{
			Service: &model.ServiceTrigger{
				Port: 9999,
				HTTP: []model.HTTPRoute{{Path: "/test", Method: "POST"}},
			},
		},
		Jobs: map[string]model.Job{
			"handle": {
				ID: "handle",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}

	m, err := New(cfg,
		WithDB(),
		WithWorkflow(wf, dir),
		WithHTTP(9999),
		WithResolver(mockResolver{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	_, err = m.OpenRuntime(context.Background(), model.ProcessModeService, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	// Service mode: no initial run request
	err = m.InitRuntime(context.Background(), nil, model.ProcessModeService)
	if err != nil {
		t.Fatalf("InitRuntime: %v", err)
	}

	// Verify HTTP trigger routes were actually registered by setupHTTPTriggers.
	routes := m.http.Engine().Routes()
	found := false
	for _, r := range routes {
		if r.Path == "/test" && r.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected POST /test route to be registered by setupHTTPTriggers")
	}
}

func TestInitRuntime_ScheduleMode(t *testing.T) {
	dir := t.TempDir()
	cfg := &conf.Config{Home: dir}

	wf := &model.Workflow{
		Name:       "sched-test",
		Source:     "test.yaml",
		Agents:     map[string]model.AgentSpec{},
		Skills:     map[string]model.SkillSpec{},
		MCPServers: map[string]model.MCPServerSpec{},
		On: model.OnSpec{
			Schedule: []model.ScheduleRule{
				{Cron: "*/5 * * * *"},
			},
		},
		Jobs: map[string]model.Job{
			"tick": {
				ID: "tick",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo tick"}},
				},
			},
		},
	}

	m, err := New(cfg,
		WithDB(),
		WithWorkflow(wf, dir),
		WithResolver(mockResolver{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Shutdown()

	_, err = m.OpenRuntime(context.Background(), model.ProcessModeSchedule, "test.yaml")
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}

	err = m.InitRuntime(context.Background(), nil, model.ProcessModeSchedule)
	if err != nil {
		t.Fatalf("InitRuntime: %v", err)
	}

	if m.scheduler == nil {
		t.Error("expected scheduler to be initialized for schedule mode")
	}
}
