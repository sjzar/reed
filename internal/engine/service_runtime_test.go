package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
)

// --- Engine test helpers ---

var testProcN int

func nextTestProcessID() string {
	testProcN++
	return fmt.Sprintf("proc_%04d", testProcN)
}

func makeEngine(t *testing.T, wf *model.Workflow) (*Engine, *sync.Mutex, *time.Time) {
	t.Helper()
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	mu := &sync.Mutex{}
	idGen := &mockIDGen{}
	e, err := New(&echoWorker{},
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
		},
		WithNowFunc(func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		}),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, mu, &now
}

func makeEngineWithWorker(t *testing.T, w Worker, wf *model.Workflow) *Engine {
	t.Helper()
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	idGen := &mockIDGen{}
	e, err := New(w,
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
		},
		WithNowFunc(func() time.Time { return now }),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func waitRunTerminal(t *testing.T, e *Engine, runID string, timeout time.Duration) *RunView {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	view, err := e.WaitRun(ctx, runID)
	if err != nil {
		t.Fatalf("WaitRun(%s): %v", runID, err)
	}
	return view
}

// --- Engine tests ---

func TestEngine_New(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	if e.ProcessID() == "" {
		t.Error("expected non-empty processID")
	}
}

func TestEngine_Submit_SimpleWorkflow(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if handle.ID() == "" {
		t.Error("expected non-empty RunID")
	}

	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)
	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
}

func TestEngine_Submit_DAGOrdering(t *testing.T) {
	wf := &model.Workflow{
		Name: "dag-test",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"c": {ID: "c", Needs: []string{"b"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
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

func TestEngine_StopRun(t *testing.T) {
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, &slowWorker{}, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))

	// Wait for run to be RUNNING
	time.Sleep(50 * time.Millisecond)

	ok := e.StopRun(handle.ID())
	if !ok {
		t.Error("StopRun returned false for existing run")
	}
	ok = e.StopRun("nonexistent")
	if ok {
		t.Error("StopRun returned true for nonexistent run")
	}

	view := waitRunTerminal(t, e, handle.ID(), 5*time.Second)
	if view.Status != model.RunCanceled && view.Status != model.RunFailed {
		t.Errorf("status = %s, want CANCELED or FAILED", view.Status)
	}
}

func TestEngine_GetRun(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	got, ok := e.GetRun(handle.ID())
	if !ok || got.ID != handle.ID() {
		t.Errorf("GetRun(%q) failed", handle.ID())
	}
	_, ok = e.GetRun("nonexistent")
	if ok {
		t.Error("GetRun returned true for nonexistent run")
	}
}

func TestEngine_WaitRun_NotFound(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	_, err := e.WaitRun(context.Background(), "nonexistent")
	if err != ErrRunNotFound {
		t.Errorf("err = %v, want ErrRunNotFound", err)
	}
}

func TestEngine_Submit_Closed(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	e.Close(context.Background())

	_, err := e.Submit(simpleRunRequest(simpleWorkflow()))
	if err != ErrEngineClosed {
		t.Errorf("err = %v, want ErrEngineClosed", err)
	}
}

func TestEngine_New_RequiresBus(t *testing.T) {
	_, err := New(&echoWorker{},
		Config{
			ProcessID:      "proc_test",
			Mode:           model.ProcessModeCLI,
			Workflow:       simpleWorkflow(),
			WorkflowSource: "test.yaml",
			// Bus intentionally nil
		},
	)
	if err == nil {
		t.Fatal("expected error for nil Bus")
	}
}

func TestEngine_DoneCh_ClosesOnCompletion(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	e.Submit(simpleRunRequest(wf))
	e.Seal()

	select {
	case <-e.DoneCh():
	case <-time.After(2 * time.Second):
		t.Fatal("DoneCh not closed after run completion")
	}
}

func TestEngine_DoneCh_MultipleRuns(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	e.Submit(simpleRunRequest(wf))
	e.Submit(simpleRunRequest(wf))
	e.Seal()

	select {
	case <-e.DoneCh():
	case <-time.After(3 * time.Second):
		t.Fatal("DoneCh not closed after all runs completed")
	}
}

func TestEngine_Submit_WorkflowMismatch(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	otherWf := &model.Workflow{
		Name: "other",
		Jobs: map[string]model.Job{
			"x": {ID: "x", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	req := &model.RunRequest{
		Workflow:       otherWf,
		WorkflowSource: "other.yaml",
		TriggerType:    model.TriggerCLI,
		Inputs:         make(map[string]any),
		Outputs:        make(map[string]string),
		Env:            nil,
		Secrets:        make(map[string]string),
	}
	_, err := e.Submit(req)
	if err == nil {
		t.Fatal("expected error for workflow mismatch")
	}
	if !strings.Contains(err.Error(), "workflow mismatch") {
		t.Errorf("error = %q, want containing 'workflow mismatch'", err.Error())
	}
}

func TestEngine_Snapshot_SeparatesActiveAndTerminal(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	snap := e.Snapshot()
	if len(snap.ActiveRuns) != 0 {
		t.Errorf("ActiveRuns = %d, want 0", len(snap.ActiveRuns))
	}
	if len(snap.TerminalRuns) != 1 {
		t.Errorf("TerminalRuns = %d, want 1", len(snap.TerminalRuns))
	}
	if snap.TerminalRuns[0].ID != handle.ID() {
		t.Errorf("TerminalRuns[0].ID = %q, want %q", snap.TerminalRuns[0].ID, handle.ID())
	}
}

// --- New tests for engine fixes ---

func TestEngine_New_NilWorkflow(t *testing.T) {
	_, err := New(&echoWorker{},
		Config{
			ProcessID:      "proc_test",
			Mode:           model.ProcessModeCLI,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
		},
	)
	if err == nil {
		t.Fatal("expected error for nil workflow")
	}
}

func TestEngine_New_InvalidMode(t *testing.T) {
	_, err := New(&echoWorker{},
		Config{
			ProcessID: "proc_test",
			Mode:      model.ProcessMode("invalid"),
			Workflow:  simpleWorkflow(),
			Bus:       bus.New(),
		},
	)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestEngine_ToolAccess_Default(t *testing.T) {
	cw := &captureWorker{}
	wf := simpleWorkflow()
	e := makeEngineWithWorker(t, cw, wf)
	defer e.Close(context.Background())
	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)
	if got := cw.Payloads()[0].ToolAccess; got != model.ToolAccessWorkDir {
		t.Errorf("ToolAccess = %q, want workdir", got)
	}
}

func TestEngine_ToolAccess_Explicit(t *testing.T) {
	cw := &captureWorker{}
	wf := simpleWorkflow()
	wf.ToolAccess = "full"
	e := makeEngineWithWorker(t, cw, wf)
	defer e.Close(context.Background())
	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)
	if got := cw.Payloads()[0].ToolAccess; got != model.ToolAccessFull {
		t.Errorf("ToolAccess = %q, want full", got)
	}
}

func TestEngine_WaitRun_RetentionResilience(t *testing.T) {
	wf := simpleWorkflow()
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	idGen := &mockIDGen{}
	e, err := New(&slowWorker{},
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Retention:      RetentionPolicy{MaxTerminalRuns: 1},
			Bus:            bus.New(),
		},
		WithNowFunc(func() time.Time { return now }),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer e.Close(context.Background())

	h1, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit h1: %v", err)
	}
	h2, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit h2: %v", err)
	}

	var wg sync.WaitGroup
	var v1, v2 *RunView
	var err1, err2 error
	wg.Add(2)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v1, err1 = e.WaitRun(ctx, h1.ID())
	}()
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v2, err2 = e.WaitRun(ctx, h2.ID())
	}()

	time.Sleep(50 * time.Millisecond)
	e.StopAll()
	wg.Wait()

	if err1 != nil {
		t.Fatalf("WaitRun(h1): %v", err1)
	}
	if err2 != nil {
		t.Fatalf("WaitRun(h2): %v", err2)
	}
	if v1 == nil || v2 == nil {
		t.Fatal("nil view")
	}
}

func TestEngine_DoneCh_ConcurrentClose_Stress(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		func() {
			wf := &model.Workflow{
				Name: "stress",
				Jobs: map[string]model.Job{
					"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
					"b": {ID: "b", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
					"c": {ID: "c", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
				},
			}
			e := makeEngineWithWorker(t, &slowWorker{}, wf)
			defer e.Close(context.Background())

			for i := 0; i < 3; i++ {
				e.Submit(simpleRunRequest(wf))
			}
			e.Seal()
			time.Sleep(10 * time.Millisecond)
			e.StopAll()

			select {
			case <-e.DoneCh():
			case <-time.After(5 * time.Second):
				t.Fatal("DoneCh not closed")
			}
		}()
	}
}

func TestEngine_CancelAllPending_Events(t *testing.T) {
	sink := &memEventSink{}
	wf := &model.Workflow{
		Name: "cancel-events",
		Jobs: map[string]model.Job{
			"a": {ID: "a", Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
			"b": {ID: "b", Needs: []string{"a"}, Steps: []model.Step{{ID: "s1", Uses: "shell"}}},
		},
	}
	now := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	idGen := &mockIDGen{}
	e, err := New(&slowWorker{},
		Config{
			ProcessID:      nextTestProcessID(),
			Mode:           model.ProcessModeCLI,
			Workflow:       wf,
			WorkflowSource: "test.yaml",
			Bus:            bus.New(),
			EventSink:      sink,
		},
		WithNowFunc(func() time.Time { return now }),
		WithNewRunID(idGen.NewRunID),
		WithNewStepRunID(idGen.NewStepRunID),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	handle, _ := e.Submit(simpleRunRequest(wf))
	time.Sleep(50 * time.Millisecond)
	e.StopAll()
	waitRunTerminal(t, e, handle.ID(), 5*time.Second)

	// Close engine to ensure the persistence goroutine drains all buffered events
	e.Close(context.Background())

	events := sink.Events()

	hasCanceled := false
	for _, ev := range events {
		if ev.Type == EventStepFailed && ev.Status == string(model.StepCanceled) {
			hasCanceled = true
		}
	}
	if !hasCanceled {
		t.Error("expected step_failed event with CANCELED status for running step")
	}

	hasSkipped := false
	for _, ev := range events {
		if ev.Type == EventStepFinished && ev.Status == string(model.StepSkipped) {
			hasSkipped = true
		}
	}
	if !hasSkipped {
		t.Error("expected step_finished event with SKIPPED status for pending step")
	}
}

func TestEngine_OutputRenderErr_NotSetDuringDispatch(t *testing.T) {
	wf := &model.Workflow{
		Name: "stale-err",
		Jobs: map[string]model.Job{
			"a": {
				ID:    "a",
				Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"result": "hello"}}},
				Outputs: map[string]string{
					"result": "${{ steps.s1.outputs.result }}",
				},
			},
			"b": {
				ID:    "b",
				Needs: []string{"a"},
				Steps: []model.Step{{ID: "s1", Uses: "shell", With: map[string]any{"val": "${{ jobs.a.outputs.result }}"}}},
			},
		},
	}
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())
	handle, _ := e.Submit(simpleRunRequest(wf))
	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED (no stale outputRenderErr)", view.Status)
	}
	if view.ErrorCode != "" {
		t.Errorf("errorCode = %q, want empty", view.ErrorCode)
	}
}

// --- Seal edge-case tests ---

func TestEngine_Seal_BeforeSubmit(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	// Seal before any Submit — DoneCh should close immediately
	e.Seal()

	select {
	case <-e.DoneCh():
	case <-time.After(1 * time.Second):
		t.Fatal("DoneCh not closed after Seal with no runs")
	}
}

func TestEngine_Seal_AfterClose(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)

	e.Close(context.Background())
	// Seal after Close should not panic
	e.Seal()

	select {
	case <-e.DoneCh():
	case <-time.After(1 * time.Second):
		t.Fatal("DoneCh not closed after Close+Seal")
	}
}

func TestEngine_Submit_AfterSeal(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	// Seal first, then submit — run should still execute and DoneCh re-close
	e.Seal()
	<-e.DoneCh() // already closed

	handle, err := e.Submit(simpleRunRequest(wf))
	if err != nil {
		t.Fatalf("Submit after Seal: %v", err)
	}

	view := waitRunTerminal(t, e, handle.ID(), 2*time.Second)
	if view.Status != model.RunSucceeded {
		t.Errorf("status = %s, want SUCCEEDED", view.Status)
	}
}

func TestEngine_Unsealed_DoneCh_StaysOpen(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	// Submit without Seal — DoneCh should NOT close even after run completes
	handle, _ := e.Submit(simpleRunRequest(wf))
	waitRunTerminal(t, e, handle.ID(), 2*time.Second)

	select {
	case <-e.DoneCh():
		t.Fatal("DoneCh closed without Seal — should stay open for service/schedule mode")
	case <-time.After(200 * time.Millisecond):
		// expected: DoneCh stays open
	}
}

func TestEngine_Mode_Accessor(t *testing.T) {
	wf := simpleWorkflow()
	e, _, _ := makeEngine(t, wf)
	defer e.Close(context.Background())

	if e.Mode() != model.ProcessModeCLI {
		t.Errorf("Mode() = %s, want cli", e.Mode())
	}
}
