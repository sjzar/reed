package engine_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/worker"
)

var e2eProcN int

func nextE2EProcessID() string {
	e2eProcN++
	return fmt.Sprintf("e2e_proc_%04d", e2eProcN)
}

func e2eSimpleRunRequest(wf *model.Workflow) *model.RunRequest {
	return &model.RunRequest{
		Workflow:       wf,
		WorkflowSource: "test.yaml",
		TriggerType:    model.TriggerCLI,
		Inputs:         make(map[string]any),
		Outputs:        make(map[string]string),
		Env:            wf.Env,
		Secrets:        make(map[string]string),
	}
}

func makeE2EEngine(t *testing.T, wf *model.Workflow, source string) *engine.Engine {
	t.Helper()
	router := worker.NewRouter()
	e, err := engine.New(router,
		engine.Config{
			ProcessID:      nextE2EProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: source,
			Bus:            bus.New(),
		},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func waitE2ERunTerminal(t *testing.T, e *engine.Engine, runID string, timeout time.Duration) *engine.RunView {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	view, err := e.WaitRun(ctx, runID)
	if err != nil {
		t.Fatalf("WaitRun(%s): %v", runID, err)
	}
	return view
}

// TestE2ENew_RealShellExecution runs a full workflow with real shell commands.
func TestE2ENew_RealShellExecution(t *testing.T) {
	wf := &model.Workflow{
		Name: "e2e-test",
		Jobs: map[string]model.Job{
			"setup": {
				ID: "setup",
				Steps: []model.Step{
					{ID: "echo", Uses: "shell", With: map[string]any{"run": "echo setup_done"}},
				},
			},
			"build": {
				ID:    "build",
				Needs: []string{"setup"},
				Steps: []model.Step{
					{ID: "compile", Uses: "bash", With: map[string]any{"run": "echo compiled"}},
				},
			},
		},
	}

	e := makeE2EEngine(t, wf, "e2e.yaml")
	handle, err := e.Submit(e2eSimpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)
	if view.Status != model.RunSucceeded {
		t.Errorf("run status = %s, want SUCCEEDED", view.Status)
	}
	if len(view.Jobs) != 2 {
		t.Errorf("jobs = %d, want 2", len(view.Jobs))
	}

	// DoneCh should close after Seal
	e.Seal()
	select {
	case <-e.DoneCh():
	case <-time.After(2 * time.Second):
		t.Fatal("DoneCh not closed")
	}

	// Close should succeed; HasFailedRuns should be false
	if err := e.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if e.HasFailedRuns() {
		t.Error("expected no failed runs")
	}
}

// TestE2ENew_RealShellWithEventLog tests event logging during real execution.
func TestE2ENew_RealShellWithEventLog(t *testing.T) {
	wf := &model.Workflow{
		Name: "event-log-test",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}

	logDir := t.TempDir()
	processID := nextE2EProcessID()
	w, err := engine.NewJSONLWriter(logDir, processID)
	if err != nil {
		t.Fatal(err)
	}

	router := worker.NewRouter()
	e, err := engine.New(router,
		engine.Config{
			ProcessID:      processID,
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "e2e.yaml",
			Bus:            bus.New(),
			EventSink:      w,
		},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	handle, _ := e.Submit(e2eSimpleRunRequest(wf))
	waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)
	e.Close(context.Background())
	w.Close()

	logPath := filepath.Join(logDir, processID+".events.jsonl")
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("event log file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("event log file is empty")
	}
}

// TestE2ENew_FailedStepPropagation tests that a failing shell command propagates correctly.
func TestE2ENew_FailedStepPropagation(t *testing.T) {
	wf := &model.Workflow{
		Name: "fail-test",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "exit 42"}},
				},
			},
			"deploy": {
				ID:    "deploy",
				Needs: []string{"build"},
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo should_not_run"}},
				},
			},
		},
	}

	e := makeE2EEngine(t, wf, "test.yaml")
	handle, _ := e.Submit(e2eSimpleRunRequest(wf))
	view := waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)

	if view.Status != model.RunFailed {
		t.Errorf("run status = %s, want FAILED", view.Status)
	}

	// build step FAILED, deploy step SKIPPED
	buildJob := view.Jobs["build"]
	for _, sv := range buildJob.Steps {
		if sv.Status != model.StepFailed {
			t.Errorf("build step status = %s, want FAILED", sv.Status)
		}
	}
	deployJob := view.Jobs["deploy"]
	for _, sv := range deployJob.Steps {
		if sv.Status != model.StepSkipped {
			t.Errorf("deploy step status = %s, want SKIPPED", sv.Status)
		}
	}

	// Close should succeed; HasFailedRuns should be true
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.HasFailedRuns() {
		t.Error("expected HasFailedRuns() = true")
	}
}

// TestE2ENew_RunTempDirExists verifies the run temp dir exists during step execution.
func TestE2ENew_RunTempDirExists(t *testing.T) {
	wf := &model.Workflow{
		Name: "tempdir-test",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "check", Uses: "shell", With: map[string]any{"run": "test -d $REED_RUN_TEMP_DIR && echo ok"}},
				},
			},
		},
	}

	e := makeE2EEngine(t, wf, "test.yaml")
	handle, _ := e.Submit(e2eSimpleRunRequest(wf))
	view := waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)

	if view.Status != model.RunSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", view.Status)
	}
	buildJob := view.Jobs["build"]
	for _, sv := range buildJob.Steps {
		stdout, _ := sv.Outputs["stdout"].(string)
		if stdout == "" || !strings.HasPrefix(stdout, "ok") {
			t.Errorf("stdout = %q, want ok", stdout)
		}
	}
}

// TestE2ENew_WorkflowEnvPassthrough verifies workflow-level env is passed to shell steps.
func TestE2ENew_WorkflowEnvPassthrough(t *testing.T) {
	wf := &model.Workflow{
		Name: "env-passthrough",
		Env:  map[string]string{"MY_APP": "reed"},
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "echo", Uses: "shell", With: map[string]any{"run": "echo $MY_APP"}},
				},
			},
		},
	}

	e := makeE2EEngine(t, wf, "test.yaml")
	req := e2eSimpleRunRequest(wf)
	handle, _ := e.Submit(req)
	view := waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)

	if view.Status != model.RunSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", view.Status)
	}
	buildJob := view.Jobs["build"]
	for _, sv := range buildJob.Steps {
		stdout, _ := sv.Outputs["stdout"].(string)
		if !strings.Contains(stdout, "reed") {
			t.Errorf("stdout = %q, want to contain 'reed'", stdout)
		}
	}
}

// TestE2ENew_SkippedStepUnblocksDownstream verifies that a step skipped via
// `if: false` counts as completed for DAG dependency resolution, so downstream
// jobs that `needs` the parent job are not permanently blocked.
func TestE2ENew_SkippedStepUnblocksDownstream(t *testing.T) {
	wf := &model.Workflow{
		Name: "skip-unblocks",
		Jobs: map[string]model.Job{
			"setup": {
				ID: "setup",
				Steps: []model.Step{
					{ID: "always", Uses: "shell", With: map[string]any{"run": "echo ok"}},
					{ID: "skipped", Uses: "shell", With: map[string]any{"run": "echo never"}, If: "false"},
				},
			},
			"downstream": {
				ID:    "downstream",
				Needs: []string{"setup"},
				Steps: []model.Step{
					{ID: "check", Uses: "shell", With: map[string]any{"run": "echo downstream_ok"}},
				},
			},
		},
	}

	e := makeE2EEngine(t, wf, "test.yaml")
	handle, err := e.Submit(e2eSimpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitE2ERunTerminal(t, e, handle.ID(), 5*time.Second)
	if view.Status != model.RunSucceeded {
		t.Fatalf("run status = %s, want SUCCEEDED (skipped step should not block downstream)", view.Status)
	}

	// Verify the skipped step
	setupJob, ok := view.Jobs["setup"]
	if !ok {
		t.Fatal("setup job not found in view")
	}
	for _, sv := range setupJob.Steps {
		if sv.StepID == "skipped" && sv.Status != model.StepSkipped {
			t.Errorf("setup/skipped status = %s, want SKIPPED", sv.Status)
		}
	}

	// Verify downstream ran
	downstreamJob, ok := view.Jobs["downstream"]
	if !ok {
		t.Fatal("downstream job not found in view")
	}
	for _, sv := range downstreamJob.Steps {
		if sv.Status != model.StepSucceeded {
			t.Errorf("downstream step status = %s, want SUCCEEDED", sv.Status)
		}
	}
}
