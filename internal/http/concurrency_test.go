package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/model"
)

type concurrencyTestResolver struct {
	workflow *model.Workflow
}

func (r concurrencyTestResolver) ResolveRunRequest(_ context.Context, wf *model.Workflow, wfSource string, params model.TriggerParams) (*model.RunRequest, error) {
	if wf == nil {
		wf = r.workflow
	}
	return &model.RunRequest{
		Workflow:       wf,
		WorkflowSource: wfSource,
		TriggerType:    params.TriggerType,
		TriggerMeta:    params.TriggerMeta,
		Inputs:         params.InputValues,
		Outputs:        map[string]string{},
		Env:            map[string]string{},
		Secrets:        map[string]string{},
	}, nil
}

// stubRunSubmitter implements RunSubmitter for concurrency tests.
type stubRunSubmitter struct {
	mu      sync.Mutex
	runs    map[string]*stubRun
	started chan string
}

type stubRun struct {
	id      string
	label   string
	doneCh  chan struct{}
	outputs map[string]any

	mu     sync.Mutex
	status model.RunStatus
}

func newStubRunSubmitter() *stubRunSubmitter {
	return &stubRunSubmitter{
		runs:    make(map[string]*stubRun),
		started: make(chan string, 16),
	}
}

func (s *stubRunSubmitter) Submit(req *model.RunRequest) (*engine.RunHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	label, _ := req.Inputs["request_id"].(string)
	id := "run-" + label
	run := &stubRun{
		id:      id,
		label:   label,
		doneCh:  make(chan struct{}),
		outputs: map[string]any{"request_id": label},
		status:  model.RunRunning,
	}
	s.runs[id] = run
	s.started <- label
	return engine.MustNewRunHandle(id), nil
}

func (s *stubRunSubmitter) WaitRun(ctx context.Context, runID string) (*engine.RunView, error) {
	s.mu.Lock()
	run, ok := s.runs[runID]
	s.mu.Unlock()
	if !ok {
		return nil, engine.ErrRunNotFound
	}

	select {
	case <-run.doneCh:
		run.mu.Lock()
		status := run.status
		run.mu.Unlock()
		return &engine.RunView{
			ID:      run.id,
			Status:  status,
			Outputs: run.outputs,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *stubRunSubmitter) waitStarted(t *testing.T, label string) *stubRun {
	t.Helper()
	select {
	case got := <-s.started:
		if got != label {
			t.Fatalf("started label = %q, want %q", got, label)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for run %q to start", label)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.label == label {
			return run
		}
	}
	t.Fatalf("run %q not found", label)
	return nil
}

func (s *stubRunSubmitter) assertNoAdditionalStart(t *testing.T) {
	t.Helper()
	select {
	case got := <-s.started:
		t.Fatalf("unexpected additional run start: %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func (r *stubRun) succeed() {
	r.mu.Lock()
	r.status = model.RunSucceeded
	r.mu.Unlock()
	close(r.doneCh)
}

func TestHTTPTriggerConcurrencyQueue(t *testing.T) {
	route := model.HTTPRoute{
		Path:   "/run",
		Method: "POST",
		Concurrency: &model.ConcurrencySpec{
			Group:    "${{ inputs.session_id }}",
			Behavior: model.ConcurrencyBehaviorQueue,
		},
	}
	service := New(Config{Debug: false})
	wf := &model.Workflow{}
	submitter := newStubRunSubmitter()
	dispatcher := NewHTTPTriggerDispatcher(concurrencyTestResolver{workflow: wf}, submitter, wf, "test.yaml", "", nil)
	service.RegisterWorkflowRoutes([]model.HTTPRoute{route}, dispatcher)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	secondDone := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		firstDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "first"})
	}()
	firstRun := submitter.waitStarted(t, "first")

	go func() {
		secondDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "second"})
	}()
	submitter.assertNoAdditionalStart(t)

	firstRun.succeed()
	assertStatusCode(t, <-firstDone, http.StatusOK)

	secondRun := submitter.waitStarted(t, "second")
	secondRun.succeed()
	assertStatusCode(t, <-secondDone, http.StatusOK)
}

func TestHTTPTriggerConcurrencySkip(t *testing.T) {
	route := model.HTTPRoute{
		Path:   "/run",
		Method: "POST",
		Concurrency: &model.ConcurrencySpec{
			Group:    "${{ inputs.session_id }}",
			Behavior: model.ConcurrencyBehaviorSkip,
		},
	}
	service := New(Config{Debug: false})
	wf := &model.Workflow{}
	submitter := newStubRunSubmitter()
	dispatcher := NewHTTPTriggerDispatcher(concurrencyTestResolver{workflow: wf}, submitter, wf, "test.yaml", "", nil)
	service.RegisterWorkflowRoutes([]model.HTTPRoute{route}, dispatcher)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "first"})
	}()
	firstRun := submitter.waitStarted(t, "first")

	secondResp := performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "second"})
	assertStatusCode(t, secondResp, http.StatusConflict)
	assertJSONErrorContains(t, secondResp, "busy")

	firstRun.succeed()
	assertStatusCode(t, <-firstDone, http.StatusOK)
}

func TestHTTPTriggerConcurrencyReplacePending(t *testing.T) {
	route := model.HTTPRoute{
		Path:   "/run",
		Method: "POST",
		Concurrency: &model.ConcurrencySpec{
			Group:    "${{ inputs.session_id }}",
			Behavior: model.ConcurrencyBehaviorReplacePending,
		},
	}
	service := New(Config{Debug: false})
	wf := &model.Workflow{}
	submitter := newStubRunSubmitter()
	dispatcher := NewHTTPTriggerDispatcher(concurrencyTestResolver{workflow: wf}, submitter, wf, "test.yaml", "", nil)
	service.RegisterWorkflowRoutes([]model.HTTPRoute{route}, dispatcher)

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	thirdDone := make(chan *httptest.ResponseRecorder, 1)

	go func() {
		firstDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "first"})
	}()
	firstRun := submitter.waitStarted(t, "first")

	go func() {
		secondDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "second"})
	}()
	submitter.assertNoAdditionalStart(t)

	go func() {
		thirdDone <- performJSONRequest(service, "/run", map[string]any{"session_id": "s1", "request_id": "third"})
	}()

	secondResp := <-secondDone
	assertStatusCode(t, secondResp, http.StatusConflict)
	assertJSONErrorContains(t, secondResp, "replaced")
	submitter.assertNoAdditionalStart(t)

	firstRun.succeed()
	assertStatusCode(t, <-firstDone, http.StatusOK)

	thirdRun := submitter.waitStarted(t, "third")
	thirdRun.succeed()
	assertStatusCode(t, <-thirdDone, http.StatusOK)
}

func performJSONRequest(service *Service, path string, body map[string]any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	service.Engine().ServeHTTP(w, req)
	return w
}

func assertStatusCode(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status = %d, want %d, body=%s", recorder.Code, want, recorder.Body.String())
	}
}

func assertJSONErrorContains(t *testing.T, recorder *httptest.ResponseRecorder, substr string) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	msg, _ := payload["message"].(string)
	if msg == "" {
		t.Fatalf("missing message field in body: %s", recorder.Body.String())
	}
	if !bytes.Contains([]byte(msg), []byte(substr)) {
		t.Fatalf("message %q does not contain %q", msg, substr)
	}
}
