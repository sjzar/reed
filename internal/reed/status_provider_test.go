package reed

import (
	"testing"
	"time"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

type mockRuntimeSnapshotter struct {
	snap    engine.ProcessView
	runs    map[string]*engine.RunView
	stopped map[string]bool
}

func (m *mockRuntimeSnapshotter) Snapshot() engine.ProcessView {
	return m.snap
}

func (m *mockRuntimeSnapshotter) GetRun(runID string) (*engine.RunView, bool) {
	if m.runs == nil {
		return nil, false
	}
	rv, ok := m.runs[runID]
	return rv, ok
}

func (m *mockRuntimeSnapshotter) StopRun(runID string) bool {
	if m.stopped == nil {
		return false
	}
	_, ok := m.stopped[runID]
	return ok
}

func TestPingData(t *testing.T) {
	src := &mockRuntimeSnapshotter{snap: engine.ProcessView{
		ProcessID: "proc_test_0001",
		PID:       1234,
		Mode:      model.ProcessModeCLI,
	}}
	p := newStatusProvider(src)
	ping := p.PingData()
	if ping.ProcessID != "proc_test_0001" {
		t.Errorf("ProcessID = %q, want proc_test_0001", ping.ProcessID)
	}
	if ping.PID != 1234 {
		t.Errorf("PID = %d, want 1234", ping.PID)
	}
	if ping.Mode != "cli" {
		t.Errorf("Mode = %q, want cli", ping.Mode)
	}
}

func TestStatusData_NoProcess(t *testing.T) {
	src := &mockRuntimeSnapshotter{snap: engine.ProcessView{}}
	p := newStatusProvider(src)
	_, err := p.StatusData()
	if err == nil {
		t.Fatal("expected error for empty ProcessID")
	}
}

func TestStatusData_WithRuns(t *testing.T) {
	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	src := &mockRuntimeSnapshotter{snap: engine.ProcessView{
		ProcessID: "proc_test_0001",
		PID:       1234,
		Mode:      model.ProcessModeCLI,
		Status:    model.ProcessRunning,
		CreatedAt: now,
		ActiveRuns: []engine.RunView{
			{
				ID:             "run_0001",
				WorkflowSource: "test.yaml",
				Status:         model.RunRunning,
				CreatedAt:      now,
				StartedAt:      now,
				Jobs: map[string]engine.JobView{
					"build": {
						JobID:  "build",
						Status: model.RunSucceeded,
						Steps: map[string]engine.StepView{
							"s1": {StepID: "s1", StepRunID: "sr_01", Status: model.StepSucceeded, Outputs: map[string]any{"stdout": "ok"}},
						},
					},
					"deploy": {
						JobID:  "deploy",
						Status: model.RunRunning,
						Steps: map[string]engine.StepView{
							"s1": {StepID: "s1", StepRunID: "sr_02", Status: model.StepRunning},
						},
					},
				},
			},
		},
	}}
	p := newStatusProvider(src)
	data, err := p.StatusData()
	if err != nil {
		t.Fatalf("StatusData: %v", err)
	}
	view, ok := data.(model.StatusView)
	if !ok {
		t.Fatalf("data type = %T, want model.StatusView", data)
	}
	if len(view.ActiveRuns) != 1 {
		t.Fatalf("ActiveRuns = %d, want 1", len(view.ActiveRuns))
	}
	if len(view.ActiveRuns[0].Jobs) != 2 {
		t.Errorf("Jobs = %d, want 2", len(view.ActiveRuns[0].Jobs))
	}
}

func TestBuildJobViews_Empty(t *testing.T) {
	result := buildJobViews(nil)
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestBuildJobViews_Mapping(t *testing.T) {
	engineJobs := map[string]engine.JobView{
		"build": {
			JobID:  "build",
			Status: model.RunSucceeded,
			Steps: map[string]engine.StepView{
				"s1": {StepID: "s1", StepRunID: "sr_01", Status: model.StepSucceeded},
				"s2": {StepID: "s2", StepRunID: "sr_02", Status: model.StepSucceeded},
			},
		},
		"deploy": {
			JobID:  "deploy",
			Status: model.RunRunning,
			Steps: map[string]engine.StepView{
				"s1": {StepID: "s1", StepRunID: "sr_03", Status: model.StepRunning},
			},
		},
	}
	jobs := buildJobViews(engineJobs)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	buildJob, ok := jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if len(buildJob.Steps) != 2 {
		t.Errorf("build steps = %d, want 2", len(buildJob.Steps))
	}
	if buildJob.Status != string(model.RunSucceeded) {
		t.Errorf("build status = %q, want SUCCEEDED", buildJob.Status)
	}
	deployJob := jobs["deploy"]
	if deployJob.Status != string(model.RunRunning) {
		t.Errorf("deploy status = %q, want RUNNING", deployJob.Status)
	}
}
