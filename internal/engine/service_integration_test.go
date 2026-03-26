package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
)

// --- Multi-step, if-condition, cross-job, parallel tests ---

func TestEngine_MultiStepJob(t *testing.T) {
	wf := &model.Workflow{
		Name: "multi-step",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo step1"}},
					{ID: "s2", Uses: "shell", With: map[string]any{"run": "echo step2"}},
					{ID: "s3", Uses: "shell", With: map[string]any{"run": "echo step3"}},
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	handle, _ := e.Submit(req)
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
	buildJob, ok := view.Jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if len(buildJob.Steps) != 3 {
		t.Errorf("steps = %d, want 3", len(buildJob.Steps))
	}
}

func TestEngine_IfCondition_Skip(t *testing.T) {
	wf := &model.Workflow{
		Name: "if-skip",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}, If: "${{ false }}"},
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	handle, _ := e.Submit(req)
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
}

func TestEngine_IfCondition_SkipStillRendersJobOutputs(t *testing.T) {
	wf := &model.Workflow{
		Name: "if-skip-outputs",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "classify", Uses: "shell", With: map[string]any{"result": map[string]any{"intent": "skip"}}},
					{ID: "qa", Uses: "shell", With: map[string]any{"output": "hello"}, If: "${{ steps.classify.outputs.result.intent == 'qa' }}"},
				},
				Outputs: map[string]string{
					"intent": "${{ steps.classify.outputs.result.intent }}",
					"answer": "${{ default(steps.qa.outputs.output, '') }}",
				},
			},
		},
		Outputs: map[string]string{
			"intent": "${{ jobs.build.outputs.intent }}",
			"answer": "${{ jobs.build.outputs.answer }}",
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	handle, _ := e.Submit(req)
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Fatalf("status = %s, want SUCCEEDED", view.Status)
	}
	buildJob, ok := view.Jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if buildJob.Outputs == nil {
		t.Fatalf("build job outputs = nil, view = %#v", view)
	}
	if got := buildJob.Outputs["intent"]; got != "skip" {
		t.Fatalf("job output intent = %v, want skip", got)
	}
	if got := view.Outputs["intent"]; got != "skip" {
		t.Fatalf("workflow outputs = %#v, workflow output intent = %v, want skip", view.Outputs, got)
	}
	if got := view.Outputs["answer"]; got != "" {
		t.Fatalf("workflow output answer = %v, want empty string", got)
	}
}

func TestEngine_IfCondition_Run(t *testing.T) {
	wf := &model.Workflow{
		Name: "if-run",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}, If: "${{ true }}"},
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
}

func TestEngine_CrossJobOutputs(t *testing.T) {
	wf := &model.Workflow{
		Name: "cross-job",
		Jobs: map[string]model.Job{
			"a": {
				ID: "a",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "hello"}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
			"b": {
				ID:    "b",
				Needs: []string{"a"},
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "${{ jobs.a.outputs.result }}"}},
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
	bJob, ok := view.Jobs["b"]
	if !ok {
		t.Fatal("missing job b")
	}
	for _, sv := range bJob.Steps {
		if got, ok := sv.Outputs["run"]; ok && got != "hello" {
			t.Errorf("job b step output run = %v, want hello", got)
		}
	}
}

func TestEngine_ParallelJobs(t *testing.T) {
	wf := &model.Workflow{
		Name: "parallel",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"b": {ID: "b", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"c": {ID: "c", Needs: []string{"a", "b"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
	if len(view.Jobs) != 3 {
		t.Errorf("jobs = %d, want 3", len(view.Jobs))
	}
}

func TestEngine_FailedStep(t *testing.T) {
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, &failingWorker{}, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunFailed {
		t.Errorf("status = %s, want FAILED", view.Status)
	}
}

func TestEngine_Shutdown_ProcessStatus_Succeeded(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if err := e.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close, HasFailedRuns should be false for all-succeeded runs
	if e.HasFailedRuns() {
		t.Error("expected HasFailedRuns() = false after all succeeded")
	}
}

func TestEngine_Shutdown_ProcessStatus_Failed(t *testing.T) {
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, &failingWorker{}, wf)

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if err := e.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close, HasFailedRuns should be true
	if !e.HasFailedRuns() {
		t.Error("expected HasFailedRuns() = true after failed run")
	}
}

func TestEngine_StopAll(t *testing.T) {
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, &slowWorker{}, wf)
	defer e.Close(context.Background())

	h1, _ := e.Submit(simpleRunRequest(wf))
	h2, _ := e.Submit(simpleRunRequest(wf))

	time.Sleep(50 * time.Millisecond)
	e.StopAll()

	waitRunTerminal(t, e, h1.ID(), 5*time.Second)
	waitRunTerminal(t, e, h2.ID(), 5*time.Second)
}

func TestEngine_AddListener(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	lv := model.ListenerView{Protocol: "ipc", Address: "/tmp/test.sock"}
	e.AddListener(lv)

	snap := e.Snapshot()
	if len(snap.Listeners) != 1 {
		t.Fatalf("listeners = %d, want 1", len(snap.Listeners))
	}
	if snap.Listeners[0].Address != "/tmp/test.sock" {
		t.Errorf("address = %q", snap.Listeners[0].Address)
	}
}

func TestEngine_Snapshot_Basic(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	snap := e.Snapshot()
	if snap.ProcessID == "" {
		t.Error("expected non-empty ProcessID")
	}
	if snap.Mode != model.ProcessModeCLI {
		t.Errorf("Mode = %q, want cli", snap.Mode)
	}
}

func TestEngine_Snapshot_WithRun(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	snap := e.Snapshot()
	if len(snap.ActiveRuns) != 0 {
		t.Fatalf("ActiveRuns = %d, want 0 (terminal runs should not be in ActiveRuns)", len(snap.ActiveRuns))
	}
	if len(snap.TerminalRuns) != 1 {
		t.Fatalf("TerminalRuns = %d, want 1", len(snap.TerminalRuns))
	}
	if snap.TerminalRuns[0].Status != model.RunSucceeded {
		t.Errorf("run status = %s, want SUCCEEDED", snap.TerminalRuns[0].Status)
	}
}

func TestEngine_EnvMerge(t *testing.T) {
	cw := &captureWorker{}
	wf := &model.Workflow{
		Name: "env-merge",
		Env:  map[string]string{"APP": "reed"},
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", Env: map[string]string{"STEP_VAR": "x"}},
				},
			},
		},
	}
	e := makeEngineWithWorker(t, cw, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	payloads := cw.Payloads()
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}
	env := payloads[0].Env
	if env["APP"] != "reed" {
		t.Errorf("env[APP] = %q, want reed", env["APP"])
	}
	if env["STEP_VAR"] != "x" {
		t.Errorf("env[STEP_VAR] = %q, want x", env["STEP_VAR"])
	}
}

func TestEngine_RunTempDirInjected(t *testing.T) {
	cw := &captureWorker{}
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, cw, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	env := cw.Payloads()[0].Env
	val := env["REED_RUN_TEMP_DIR"]
	if val == "" {
		t.Fatal("REED_RUN_TEMP_DIR not set")
	}
	if !strings.Contains(val, "reedruns") {
		t.Errorf("REED_RUN_TEMP_DIR = %q, want path containing reedruns", val)
	}
}

func TestEngine_EnvRendering(t *testing.T) {
	wf := &model.Workflow{
		Name: "env-test",
		Env:  map[string]string{"APP_NAME": "reed"},
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "${{ env.APP_NAME }}"}},
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
	buildJob := view.Jobs["build"]
	for _, sv := range buildJob.Steps {
		if got, ok := sv.Outputs["run"]; ok && got != "reed" {
			t.Errorf("rendered run = %v, want reed", got)
		}
	}
}

func TestEngine_HasFailedRuns(t *testing.T) {
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, &failingWorker{}, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if !e.HasFailedRuns() {
		t.Error("expected HasFailedRuns() = true")
	}
}

func TestEngine_ContextCancel_SkipsPending(t *testing.T) {
	wf := &model.Workflow{
		Name: "skip-test",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	e := makeEngineWithWorker(t, &slowWorker{}, wf)

	handle, _ := e.Submit(simpleRunRequest(wf))

	// Wait for run to be active
	time.Sleep(50 * time.Millisecond)

	// Close engine (cancels root context)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.Close(ctx)

	view, _ := e.GetRun(handle.ID())
	if view == nil {
		t.Fatal("run not found after close")
	}
	// Job b should have SKIPPED steps
	bJob, ok := view.Jobs["b"]
	if ok {
		for _, sv := range bJob.Steps {
			if sv.Status != model.StepSkipped {
				t.Errorf("job b step status = %s, want SKIPPED", sv.Status)
			}
		}
	}
}

func TestEngine_SetEventLog(t *testing.T) {
	wf := simpleWorkflow()
	dir := t.TempDir()
	w, err := NewJSONLWriter(dir, "test_001")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	idGen := &mockIDGen{}
	e, err := New(&echoWorker{},
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
			EventSink:      w,
		},
		WithNowFunc(func() time.Time { return now }),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)
}

func TestEngine_JobOutputsInSnapshot(t *testing.T) {
	wf := &model.Workflow{
		Name: "job-outputs",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "hello"}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	buildJob, ok := view.Jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if buildJob.Outputs == nil {
		t.Fatal("build job Outputs is nil, want non-nil")
	}
	if got, ok := buildJob.Outputs["result"]; !ok || got != "hello" {
		t.Errorf("build job Outputs[result] = %v, want hello", got)
	}
}

func TestEngine_ActiveSnapshot_NoJobOutputs(t *testing.T) {
	wf := &model.Workflow{
		Name: "active-no-outputs",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell"},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
		},
	}
	e := makeEngineWithWorker(t, &slowWorker{}, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	time.Sleep(50 * time.Millisecond)

	view, ok := e.GetRun(handle.ID())
	if !ok {
		t.Fatal("run not found")
	}
	buildJob, ok := view.Jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if buildJob.Outputs != nil {
		t.Errorf("active snapshot build job Outputs = %v, want nil", buildJob.Outputs)
	}
}

func TestEngine_FailedJobNoOutputs(t *testing.T) {
	wf := &model.Workflow{
		Name: "failed-no-outputs",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell"},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
		},
	}
	e := makeEngineWithWorker(t, &failingWorker{}, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	buildJob, ok := view.Jobs["build"]
	if !ok {
		t.Fatal("missing build job")
	}
	if buildJob.Outputs != nil {
		t.Errorf("failed job Outputs = %v, want nil", buildJob.Outputs)
	}
}

func TestEngine_WorkflowOutputs_NoStepCollision(t *testing.T) {
	wf := &model.Workflow{
		Name: "no-collision",
		Jobs: map[string]model.Job{
			"a": {
				ID: "a",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "from_a"}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
			"b": {
				ID: "b",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "from_b"}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
		},
		Outputs: map[string]string{
			"a_result": "${{ jobs.a.outputs.result }}",
			"b_result": "${{ jobs.b.outputs.result }}",
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	handle, _ := e.Submit(req)
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Outputs == nil {
		t.Fatal("workflow Outputs is nil")
	}
	if got := view.Outputs["a_result"]; got != "from_a" {
		t.Errorf("a_result = %v, want from_a", got)
	}
	if got := view.Outputs["b_result"]; got != "from_b" {
		t.Errorf("b_result = %v, want from_b", got)
	}
}

func TestEngine_WorkflowOutputs_ConsistentWithJobOutputs(t *testing.T) {
	wf := &model.Workflow{
		Name: "consistent",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"val": "42"}},
				},
				Outputs: map[string]string{
					"val": "${{ steps.s1.outputs.val }}",
				},
			},
		},
		Outputs: map[string]string{
			"build_val": "${{ jobs.build.outputs.val }}",
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	handle, _ := e.Submit(req)
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	jobVal := view.Jobs["build"].Outputs["val"]
	wfVal := view.Outputs["build_val"]
	if jobVal != wfVal {
		t.Errorf("job output val = %v, workflow output build_val = %v, want equal", jobVal, wfVal)
	}
}

func TestEngine_CanceledRun_SucceededJobOutputs(t *testing.T) {
	sw := &selectiveWorker{
		routes: map[string]Worker{
			"a": &echoWorker{},
			"b": &slowWorker{},
		},
		fallback: &echoWorker{},
	}
	wf := &model.Workflow{
		Name: "cancel-outputs",
		Jobs: map[string]model.Job{
			"a": {
				ID: "a",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "hello"}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
			"b": {
				ID:    "b",
				Needs: []string{"a"},
				Steps: []model.Step{
					{ID: "s1", Uses: "shell"},
				},
			},
		},
	}
	e := makeEngineWithWorker(t, sw, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	// Poll until job b is RUNNING
	deadline := time.After(5 * time.Second)
pollB:
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job b to start running")
		default:
		}
		view, ok := e.GetRun(handle.ID())
		if ok {
			if bJob, hasB := view.Jobs["b"]; hasB {
				if bJob.Status == model.RunRunning {
					break pollB
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	e.StopAll()
	view := waitRunTerminal(t, e, handle.ID(), 5*time.Second)

	if view.Status != model.RunCanceled {
		t.Errorf("status = %s, want CANCELED", view.Status)
	}
	aJob, ok := view.Jobs["a"]
	if !ok {
		t.Fatal("missing job a")
	}
	if aJob.Outputs == nil {
		t.Fatal("job a Outputs is nil, want non-nil for succeeded job in canceled run")
	}
	if got := aJob.Outputs["result"]; got != "hello" {
		t.Errorf("job a Outputs[result] = %v, want hello", got)
	}
}

func TestEngine_TerminalSnapshot_Immutability(t *testing.T) {
	wf := &model.Workflow{
		Name: "immutable",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"result": "original", "nested": map[string]any{"inner": "deep"}}},
				},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
		},
		Outputs: map[string]string{
			"build_result": "${{ jobs.build.outputs.result }}",
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.Outputs = wf.Outputs
	req.TriggerMeta = map[string]any{"key": "value"}
	handle, _ := e.Submit(req)
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	// Get first copy and mutate everything
	v1, _ := e.GetRun(handle.ID())
	v1.Outputs["build_result"] = "MUTATED"
	v1.TriggerMeta["key"] = "MUTATED"
	v1.Jobs["build"].Outputs["result"] = "MUTATED"
	for stepID, sv := range v1.Jobs["build"].Steps {
		sv.Outputs["result"] = "MUTATED"
		if nested, ok := sv.Outputs["nested"].(map[string]any); ok {
			nested["inner"] = "MUTATED"
		}
		v1.Jobs["build"].Steps[stepID] = sv
	}

	// Get second copy — must be unaffected
	v2, _ := e.GetRun(handle.ID())
	if v2.Outputs["build_result"] != "original" {
		t.Errorf("workflow output mutated: got %v, want original", v2.Outputs["build_result"])
	}
	if v2.TriggerMeta["key"] != "value" {
		t.Errorf("TriggerMeta mutated: got %v, want value", v2.TriggerMeta["key"])
	}
	if v2.Jobs["build"].Outputs["result"] != "original" {
		t.Errorf("job output mutated: got %v, want original", v2.Jobs["build"].Outputs["result"])
	}
	for _, sv := range v2.Jobs["build"].Steps {
		if sv.Outputs["result"] != "original" {
			t.Errorf("step output mutated: got %v, want original", sv.Outputs["result"])
		}
		if nested, ok := sv.Outputs["nested"].(map[string]any); ok {
			if nested["inner"] != "deep" {
				t.Errorf("nested step output mutated: got %v, want deep", nested["inner"])
			}
		}
	}

	// Also verify Snapshot path
	snap := e.Snapshot()
	for _, rv := range snap.TerminalRuns {
		if rv.ID == handle.ID() {
			if rv.Outputs["build_result"] != "original" {
				t.Errorf("Snapshot workflow output mutated: got %v", rv.Outputs["build_result"])
			}
		}
	}
}

// makeRenderErrEngine creates an engine with a custom render function that fails on bad_expr.
func makeRenderErrEngine(t *testing.T, wf *model.Workflow) *Engine {
	t.Helper()
	idGen := &mockIDGen{}
	e, err := New(&echoWorker{},
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
		},
		WithNowFunc(func() time.Time { return time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC) }),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
		WithRenderString(func(tmpl string, ctx map[string]any) (any, error) {
			if strings.Contains(tmpl, "bad_expr") {
				return nil, fmt.Errorf("render error: unresolved expression")
			}
			return tmpl, nil
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestEngine_AgentRenderError_DoesNotHang(t *testing.T) {
	wf := &model.Workflow{
		Name: "agent-render-fail",
		Agents: map[string]model.AgentSpec{
			"myagent": {Model: "${{ bad_expr }}"},
		},
		Jobs: map[string]model.Job{
			"run": {
				ID: "run",
				Steps: []model.Step{
					{ID: "s1", Uses: "agent", With: map[string]any{"agent": "myagent", "prompt": "hello"}},
				},
			},
		},
	}
	e := makeRenderErrEngine(t, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitRunTerminal(t, e, handle.ID(), 3*time.Second)
	if view.Status != model.RunFailed {
		t.Errorf("status = %s, want FAILED", view.Status)
	}
}

func TestEngine_AgentRenderError_MultiStep(t *testing.T) {
	wf := &model.Workflow{
		Name: "agent-render-fail-multi",
		Agents: map[string]model.AgentSpec{
			"myagent": {Model: "${{ bad_expr }}"},
		},
		Jobs: map[string]model.Job{
			"run": {
				ID: "run",
				Steps: []model.Step{
					{ID: "s1", Uses: "agent", With: map[string]any{"agent": "myagent", "prompt": "hello"}},
					{ID: "s2", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}
	e := makeRenderErrEngine(t, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitRunTerminal(t, e, handle.ID(), 3*time.Second)
	if view.Status != model.RunFailed {
		t.Errorf("status = %s, want FAILED (parallel jobs)", view.Status)
	}
}

func TestEngine_AgentRenderError_DependentJob(t *testing.T) {
	wf := &model.Workflow{
		Name: "dependent-render-fail",
		Agents: map[string]model.AgentSpec{
			"qa_expert": {Model: "${{ bad_expr }}"},
		},
		Jobs: map[string]model.Job{
			"gather_context": {
				ID: "gather_context",
				Steps: []model.Step{
					{ID: "list_files", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
			"answer": {
				ID:    "answer",
				Needs: []string{"gather_context"},
				Steps: []model.Step{
					{ID: "qa", Uses: "agent", With: map[string]any{"agent": "qa_expert", "prompt": "hello"}},
				},
			},
		},
	}
	e := makeRenderErrEngine(t, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitRunTerminal(t, e, handle.ID(), 3*time.Second)
	if view.Status != model.RunFailed {
		t.Errorf("status = %s, want FAILED", view.Status)
	}
}

func TestEngine_AgentRenderError_ParallelJobs(t *testing.T) {
	wf := &model.Workflow{
		Name: "parallel-render-fail",
		Agents: map[string]model.AgentSpec{
			"myagent": {Model: "${{ bad_expr }}"},
		},
		Jobs: map[string]model.Job{
			"failing": {
				ID: "failing",
				Steps: []model.Step{
					{ID: "s1", Uses: "agent", With: map[string]any{"agent": "myagent", "prompt": "hello"}},
				},
			},
			"passing": {
				ID: "passing",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}
	e := makeRenderErrEngine(t, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	view := waitRunTerminal(t, e, handle.ID(), 3*time.Second)
	if view.Status != model.RunFailed {
		t.Errorf("status = %s, want FAILED", view.Status)
	}
}

func TestEngine_RunJobs_SubgraphFilter(t *testing.T) {
	wf := &model.Workflow{
		Name: "run-jobs",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"result": "from_a"}}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"result": "from_b"}}}},
			"c": {ID: "c", Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"result": "from_c"}}}},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.RunJobs = []string{"b"} // should run a (dep) + b, skip c

	handle, err := e.Submit(req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
	// Should have jobs a and b, but not c
	if _, ok := view.Jobs["a"]; !ok {
		t.Error("missing job a (dependency of b)")
	}
	if _, ok := view.Jobs["b"]; !ok {
		t.Error("missing job b (root)")
	}
	if _, ok := view.Jobs["c"]; ok {
		t.Error("job c should not be in run (filtered by RunJobs)")
	}
}

func TestEngine_RunJobs_InvalidRoot(t *testing.T) {
	wf := &model.Workflow{
		Name: "run-jobs-invalid",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	req := simpleRunRequest(wf)
	req.RunJobs = []string{"nonexistent"}

	_, err := e.Submit(req)
	if err == nil {
		t.Fatal("expected error for invalid RunJobs root")
	}
	if !strings.Contains(err.Error(), "run_jobs filter") {
		t.Errorf("error = %q, want containing 'run_jobs filter'", err.Error())
	}
}

func TestEngine_RunJobs_WorkflowPointerUnchanged(t *testing.T) {
	wf := &model.Workflow{
		Name: "pointer-check",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"c": {ID: "c", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	// Submit with RunJobs — workflow pointer must match engine's workflow
	req := simpleRunRequest(wf)
	req.RunJobs = []string{"b"}

	handle, err := e.Submit(req)
	if err != nil {
		t.Fatalf("Submit with RunJobs should not fail with workflow mismatch: %v", err)
	}
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)
	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
}
