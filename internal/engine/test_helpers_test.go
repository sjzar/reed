package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/render"
)

// --- Test doubles ---

// memEventSink collects events in memory for test assertions.
type memEventSink struct {
	mu     sync.Mutex
	events []Event
}

func (s *memEventSink) Append(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *memEventSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Event, len(s.events))
	copy(cp, s.events)
	return cp
}

// mockIDGen generates predictable IDs.
type mockIDGen struct {
	mu       sync.Mutex
	runN     int
	stepRunN int
}

func (g *mockIDGen) NewRunID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.runN++
	return fmt.Sprintf("run_%04d", g.runN)
}

func (g *mockIDGen) NewStepRunID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stepRunN++
	return fmt.Sprintf("sr_%04d", g.stepRunN)
}

// echoWorker immediately succeeds with the step's With as outputs.
type echoWorker struct{}

func (w *echoWorker) Execute(_ context.Context, p StepPayload) StepRunResult {
	return StepRunResult{
		StepRunID: p.StepRunID,
		JobID:     p.JobID,
		StepID:    p.StepID,
		Status:    model.StepSucceeded,
		Outputs:   p.With,
	}
}

// slowWorker blocks until context is cancelled, then returns FAILED.
type slowWorker struct{}

func (w *slowWorker) Execute(ctx context.Context, p StepPayload) StepRunResult {
	<-ctx.Done()
	return StepRunResult{
		StepRunID:    p.StepRunID,
		JobID:        p.JobID,
		StepID:       p.StepID,
		Status:       model.StepFailed,
		ErrorMessage: "interrupted",
	}
}

// failingWorker always returns FAILED status.
type failingWorker struct{}

func (w *failingWorker) Execute(_ context.Context, p StepPayload) StepRunResult {
	return StepRunResult{
		StepRunID:    p.StepRunID,
		JobID:        p.JobID,
		StepID:       p.StepID,
		Status:       model.StepFailed,
		ErrorMessage: "deliberate failure",
	}
}

// captureWorker records all dispatched payloads and succeeds immediately.
type captureWorker struct {
	mu       sync.Mutex
	payloads []StepPayload
}

func (w *captureWorker) Execute(_ context.Context, p StepPayload) StepRunResult {
	w.mu.Lock()
	w.payloads = append(w.payloads, p)
	w.mu.Unlock()
	return StepRunResult{
		StepRunID: p.StepRunID,
		JobID:     p.JobID,
		StepID:    p.StepID,
		Status:    model.StepSucceeded,
		Outputs:   p.With,
	}
}

func (w *captureWorker) Payloads() []StepPayload {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]StepPayload, len(w.payloads))
	copy(out, w.payloads)
	return out
}

// selectiveWorker routes execution to different workers based on job ID.
type selectiveWorker struct {
	routes   map[string]Worker // jobID → worker
	fallback Worker
}

func (w *selectiveWorker) Execute(ctx context.Context, p StepPayload) StepRunResult {
	if worker, ok := w.routes[p.JobID]; ok {
		return worker.Execute(ctx, p)
	}
	return w.fallback.Execute(ctx, p)
}

// --- Workflow helpers ---

func simpleWorkflow() *model.Workflow {
	return &model.Workflow{
		Name: "test",
		Jobs: map[string]model.Job{
			"build": {
				ID: "build",
				Steps: []model.Step{
					{ID: "s1", Uses: "shell", With: map[string]any{"run": "echo ok"}},
				},
			},
		},
	}
}

func simpleRunRequest(wf *model.Workflow) *model.RunRequest {
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

// --- isTruthy tests ---

func TestIsTruthy(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"nil", nil, false},
		{"true", true, true},
		{"false", false, false},
		{"empty string", "", false},
		{"string false", "false", false},
		{"string 0", "0", false},
		{"nonempty string", "hello", true},
		{"int 0", int(0), false},
		{"int 1", int(1), true},
		{"int64 0", int64(0), false},
		{"int64 42", int64(42), true},
		{"float64 0", float64(0), false},
		{"float64 3.14", float64(3.14), true},
		{"map", map[string]any{}, true},
		{"slice", []int{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := render.IsTruthy(tt.val); got != tt.want {
				t.Errorf("IsTruthy(%v) = %v, want %v", tt.val, got, tt.want)
			}
		})
	}
}

// --- DeriveJobStatus tests ---

func TestDeriveJobStatus(t *testing.T) {
	tests := []struct {
		name     string
		statuses []model.StepStatus
		want     model.RunStatus
	}{
		{"all succeeded", []model.StepStatus{model.StepSucceeded, model.StepSucceeded}, model.RunSucceeded},
		{"has failed", []model.StepStatus{model.StepSucceeded, model.StepFailed}, model.RunFailed},
		{"has running", []model.StepStatus{model.StepSucceeded, model.StepRunning}, model.RunRunning},
		{"has pending", []model.StepStatus{model.StepSucceeded, model.StepPending}, model.RunStarting},
		{"has canceled", []model.StepStatus{model.StepSucceeded, model.StepCanceled}, model.RunCanceled},
		{"has skipped", []model.StepStatus{model.StepSucceeded, model.StepSkipped}, model.RunSucceeded},
		{"failed takes priority", []model.StepStatus{model.StepFailed, model.StepRunning, model.StepCanceled}, model.RunFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveJobStatus(tt.statuses)
			if got != tt.want {
				t.Errorf("DeriveJobStatus = %q, want %q", got, tt.want)
			}
		})
	}
}
