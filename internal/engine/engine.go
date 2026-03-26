package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/render"
	"github.com/sjzar/reed/internal/workflow"
)

var (
	// ErrEngineClosed is returned when Submit is called on a closed Engine.
	ErrEngineClosed = errors.New("engine closed")
	// ErrRunNotFound is returned when a run ID is not found.
	ErrRunNotFound = errors.New("run not found")
)

// RunHandle is the control handle returned by Engine.Submit.
type RunHandle struct {
	id string
}

// ID returns the run ID.
func (h *RunHandle) ID() string { return h.id }

// MustNewRunHandle creates a RunHandle with the given ID.
// Intended for test stubs; panics if id is empty.
func MustNewRunHandle(id string) *RunHandle {
	if id == "" {
		panic("engine.MustNewRunHandle: empty id")
	}
	return &RunHandle{id: id}
}

// Engine is the unified stateful runtime for a single Process.
// It merges the former Service (stateless factory) and Runtime (stateful per-process).
type Engine struct {
	// Shared dependencies (formerly Service fields)
	worker           Worker
	now              func() time.Time
	newRunID         func() string
	newStepRunID     func() string
	renderString     func(string, map[string]any) (any, error)
	renderStringSafe func(string, map[string]any) (any, error) // nil-access tolerant, for `if` conditions

	// Per-process runtime state (formerly Runtime fields)
	mu           sync.RWMutex
	closed       bool
	submitted    bool
	sealed       bool // when true, DoneCh closes after all active runs terminate
	mode         model.ProcessMode
	processID    string
	wfSource     string
	pid          int
	status       model.ProcessStatus
	createdAt    time.Time
	workflow     *model.Workflow
	rootCtx      context.Context
	rootCancel   context.CancelFunc
	activeRuns   map[string]*runState
	terminalRuns map[string]RunView
	retention    RetentionPolicy
	listeners    []model.ListenerView
	sink         EventSink
	bus          *bus.Bus
	lifecycleSub *bus.Subscription
	persistDone  chan struct{} // closed when persistLifecycleEvents goroutine exits
	doneCh       chan struct{}
	doneClosed   bool // guards double-close; always accessed under mu.Lock
}

// New creates a new Engine for the given Process.
// The caller (Manager) is responsible for DB registration; Engine has no DB dependency.
func New(worker Worker, cfg Config, opts ...Option) (*Engine, error) {
	if cfg.ProcessID == "" {
		return nil, fmt.Errorf("ProcessID is required")
	}
	if cfg.Bus == nil {
		return nil, fmt.Errorf("Bus is required")
	}
	if cfg.Workflow == nil {
		return nil, fmt.Errorf("workflow is required")
	}
	switch cfg.Mode {
	case model.ProcessModeCLI, model.ProcessModeService, model.ProcessModeSchedule:
		// valid
	default:
		return nil, fmt.Errorf("invalid process mode: %q", cfg.Mode)
	}

	e := &Engine{
		worker:           worker,
		now:              func() time.Time { return time.Now().UTC() },
		newRunID:         defaultNewRunID,
		newStepRunID:     defaultNewStepRunID,
		renderString:     render.Render,
		renderStringSafe: render.RenderSafe,
	}
	for _, opt := range opts {
		opt(e)
	}

	now := e.now()
	rootCtx, rootCancel := context.WithCancel(context.Background())

	e.mode = cfg.Mode
	e.processID = cfg.ProcessID
	e.pid = cfg.PID
	e.wfSource = cfg.WorkflowSource
	e.status = model.ProcessRunning
	e.createdAt = now
	e.workflow = cfg.Workflow
	e.rootCtx = rootCtx
	e.rootCancel = rootCancel
	e.activeRuns = make(map[string]*runState)
	e.terminalRuns = make(map[string]RunView)
	e.retention = cfg.Retention
	e.sink = cfg.EventSink
	e.bus = cfg.Bus
	e.doneCh = make(chan struct{})

	if cfg.EventSink != nil {
		sub := e.bus.Subscribe(bus.TopicLifecycle, 256)
		e.lifecycleSub = sub
		e.persistDone = make(chan struct{})
		go func() {
			e.persistLifecycleEvents(sub, cfg.EventSink)
			close(e.persistDone)
		}()
	}

	return e, nil
}

// ProcessID returns the registered process ID.
func (e *Engine) ProcessID() string { return e.processID }

// Mode returns the process mode.
func (e *Engine) Mode() model.ProcessMode { return e.mode }

// AddListener registers a network listener for status reporting.
func (e *Engine) AddListener(lv model.ListenerView) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, lv)
}

// DoneCh returns a channel closed when all runs reach terminal state (only after Seal).
func (e *Engine) DoneCh() <-chan struct{} { return e.doneCh }

// Seal marks the engine as sealed: once all active runs terminate, DoneCh closes.
// Replaces the old mode-aware logic (CLI auto-close vs service keep-open).
// Submit is still allowed after Seal (e.g. schedule triggers), but DoneCh will
// only close once those runs also reach terminal state.
func (e *Engine) Seal() {
	e.mu.Lock()
	e.sealed = true
	if len(e.activeRuns) == 0 && !e.doneClosed {
		close(e.doneCh)
		e.doneClosed = true
	}
	e.mu.Unlock()
}

// HasFailedRuns returns true if any run ended in FAILED or CANCELED.
func (e *Engine) HasFailedRuns() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, rv := range e.terminalRuns {
		if rv.Status == model.RunFailed || rv.Status == model.RunCanceled {
			return true
		}
	}
	for _, rs := range e.activeRuns {
		rs.stateMu.RLock()
		s := rs.status
		rs.stateMu.RUnlock()
		if s == model.RunFailed || s == model.RunCanceled {
			return true
		}
	}
	return false
}

// StopAll requests graceful stop of all active runs.
func (e *Engine) StopAll() {
	e.mu.RLock()
	runs := make([]*runState, 0, len(e.activeRuns))
	for _, rs := range e.activeRuns {
		runs = append(runs, rs)
	}
	e.mu.RUnlock()
	for _, rs := range runs {
		rs.stop()
	}
}

// StopRun requests graceful stop of a specific run.
func (e *Engine) StopRun(runID string) bool {
	e.mu.RLock()
	rs, ok := e.activeRuns[runID]
	e.mu.RUnlock()
	if !ok {
		return false
	}
	rs.stop()
	return true
}

// Submit creates a new Run and starts its owner loop. Returns a RunHandle.
func (e *Engine) Submit(req *model.RunRequest) (*RunHandle, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrEngineClosed
	}
	if req == nil || req.Workflow == nil || len(req.Workflow.Jobs) == 0 {
		e.mu.Unlock()
		return nil, fmt.Errorf("invalid run request: workflow and jobs are required")
	}
	if req.Workflow != e.workflow {
		e.mu.Unlock()
		return nil, fmt.Errorf("workflow mismatch: runtime bound to %q, request has %q",
			e.wfSource, req.WorkflowSource)
	}

	// Compute effective jobs: subgraph filter if RunJobs specified
	jobs := req.Workflow.Jobs
	if len(req.RunJobs) > 0 {
		var err error
		jobs, err = workflow.SubgraphJobs(req.Workflow.Jobs, req.RunJobs)
		if err != nil {
			e.mu.Unlock()
			return nil, fmt.Errorf("run_jobs filter: %w", err)
		}
	}
	if len(jobs) == 0 {
		e.mu.Unlock()
		return nil, fmt.Errorf("invalid run request: no jobs to execute")
	}

	e.submitted = true

	rs := newRunState(e.rootCtx, e.processID, req, jobs, e)
	e.activeRuns[rs.id] = rs
	e.mu.Unlock()

	go func() {
		rs.loop()
		e.onRunDone(rs)
	}()

	return &RunHandle{id: rs.id}, nil
}

// GetRun returns a snapshot of a run by ID.
func (e *Engine) GetRun(runID string) (*RunView, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if rv, ok := e.terminalRuns[runID]; ok {
		cp := rv.deepCopy()
		return &cp, true
	}
	if rs, ok := e.activeRuns[runID]; ok {
		snap := rs.snapshot()
		return &snap, true
	}
	return nil, false
}

// WaitRun blocks until the run reaches terminal state or ctx is cancelled.
func (e *Engine) WaitRun(ctx context.Context, runID string) (*RunView, error) {
	e.mu.RLock()
	if rv, ok := e.terminalRuns[runID]; ok {
		e.mu.RUnlock()
		cp := rv.deepCopy()
		return &cp, nil
	}
	rs, ok := e.activeRuns[runID]
	e.mu.RUnlock()
	if !ok {
		return nil, ErrRunNotFound
	}

	select {
	case <-rs.doneCh:
		cp := rs.finalView.deepCopy()
		return &cp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Snapshot returns a read-only snapshot of the engine state.
func (e *Engine) Snapshot() ProcessView {
	e.mu.RLock()
	defer e.mu.RUnlock()

	view := ProcessView{
		ProcessID: e.processID,
		PID:       e.pid,
		Mode:      e.mode,
		Status:    e.status,
		CreatedAt: e.createdAt,
		Listeners: append([]model.ListenerView{}, e.listeners...),
	}

	for _, rs := range e.activeRuns {
		snap := rs.snapshot()
		view.ActiveRuns = append(view.ActiveRuns, snap)
	}
	for _, rv := range e.terminalRuns {
		cp := rv.deepCopy()
		view.TerminalRuns = append(view.TerminalRuns, cp)
	}

	return view
}

// Close shuts down the engine: rejects new Submits, cancels all runs, waits for completion.
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.rootCancel()

	// Collect doneChs from active runs
	doneChs := make([]<-chan struct{}, 0, len(e.activeRuns))
	for _, rs := range e.activeRuns {
		doneChs = append(doneChs, rs.doneCh)
	}
	e.mu.Unlock()

	// Wait for all runs to finish or ctx timeout
	allDone := make(chan struct{})
	go func() {
		for _, ch := range doneChs {
			<-ch
		}
		close(allDone)
	}()

	// Always unsubscribe lifecycle subscriber and wait for persist goroutine to drain,
	// even on timeout, to avoid leaking goroutines. Use a bounded wait so a stuck
	// sink.Append does not block Close forever.
	defer func() {
		if e.lifecycleSub != nil {
			e.lifecycleSub.Unsubscribe()
			select {
			case <-e.persistDone:
			case <-time.After(5 * time.Second):
			}
		}
	}()

	select {
	case <-allDone:
		// All runs terminated — update in-memory status
		e.mu.Lock()
		finalStatus := model.ProcessStopped
		for _, rv := range e.terminalRuns {
			if rv.Status == model.RunFailed {
				finalStatus = model.ProcessFailed
				break
			}
		}
		e.status = finalStatus
		e.mu.Unlock()
		return nil

	case <-ctx.Done():
		return ctx.Err()
	}
}

// persistLifecycleEvents drains lifecycle events from the bus subscription
// and writes them to the EventSink. One goroutine per Engine.
func (e *Engine) persistLifecycleEvents(sub *bus.Subscription, sink EventSink) {
	for {
		select {
		case msg := <-sub.Ch():
			if p, ok := bus.ParseLifecycle(msg); ok {
				if err := sink.Append(context.Background(), lifecycleToEvent(p)); err != nil {
					log.Error().Err(err).Msg("failed to persist lifecycle event")
				}
			}
		case <-sub.Done():
			// Drain remaining messages in the channel before exiting
			for {
				select {
				case msg := <-sub.Ch():
					if p, ok := bus.ParseLifecycle(msg); ok {
						if err := sink.Append(context.Background(), lifecycleToEvent(p)); err != nil {
							log.Error().Err(err).Msg("failed to persist lifecycle event")
						}
					}
				default:
					return
				}
			}
		}
	}
}

// onRunDone is called after a run's loop exits.
func (e *Engine) onRunDone(rs *runState) {
	view := rs.snapshot()
	e.moveToTerminal(rs.id, view)
	rs.finalView = view
	close(rs.doneCh)
}

// moveToTerminal moves a run from active to terminal and closes doneCh if sealed and all runs done.
func (e *Engine) moveToTerminal(runID string, view RunView) {
	e.mu.Lock()
	delete(e.activeRuns, runID)
	e.terminalRuns[runID] = view
	e.evictTerminal()
	if e.sealed && len(e.activeRuns) == 0 && !e.doneClosed {
		close(e.doneCh)
		e.doneClosed = true
	}
	e.mu.Unlock()
}

func defaultNewRunID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("run_%s", hex.EncodeToString(b))
}

func defaultNewStepRunID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("step_run_%s", hex.EncodeToString(b))
}
