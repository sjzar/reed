package engine

import (
	"context"
	"sync"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
)

// runState is the internal state of a single Run execution.
type runState struct {
	id        string
	processID string
	request   *model.RunRequest
	jobs      map[string]model.Job // effective jobs (may be subgraph of workflow)

	rootCtx    context.Context
	rootCancel context.CancelFunc

	stepRunResultCh chan StepRunResult // buffered 64, only for worker goroutine results
	stopCh          chan struct{}      // buffered 1
	doneCh          chan struct{}      // closed by rt.onRunDone after terminalization
	drainCh         chan struct{}      // closed before wg.Wait, unblocks stuck senders
	finalView       RunView            // set by onRunDone before close(doneCh); read by WaitRun after <-doneCh
	wg              sync.WaitGroup     // tracks in-flight worker goroutines

	stateMu            sync.RWMutex
	status             model.RunStatus
	stopRequested      bool
	stepRuns           map[string]*model.StepRun
	stepRunIndex       map[string]*model.StepRun   // "jobID\x00stepID" → *StepRun
	stepRunsByJob      map[string][]*model.StepRun // jobID → ordered step runs
	outputRenderErr    error                       // accumulated output render errors
	renderedOutputs    map[string]any              // finalized workflow-level outputs
	renderedJobOutputs map[string]map[string]any   // finalized per-job outputs (set in finalize)
	errorCode          string
	errorMessage       string

	runTempDir string
	createdAt  time.Time
	startedAt  time.Time
	finishedAt *time.Time

	eng *Engine
}

const stepRunResultChBuffer = 64

// newRunState creates a runState wired to the runtime's root context.
func newRunState(
	parentCtx context.Context,
	processID string,
	req *model.RunRequest,
	jobs map[string]model.Job,
	eng *Engine,
) *runState {
	ctx, cancel := context.WithCancel(parentCtx)
	now := eng.now()
	return &runState{
		id:              eng.newRunID(),
		processID:       processID,
		request:         req,
		jobs:            jobs,
		rootCtx:         ctx,
		rootCancel:      cancel,
		stepRunResultCh: make(chan StepRunResult, stepRunResultChBuffer),
		stopCh:          make(chan struct{}, 1),
		doneCh:          make(chan struct{}),
		drainCh:         make(chan struct{}),
		status:          model.RunCreated,
		stepRuns:        make(map[string]*model.StepRun),
		stepRunIndex:    make(map[string]*model.StepRun),
		stepRunsByJob:   make(map[string][]*model.StepRun),
		eng:             eng,
		createdAt:       now,
	}
}

// stop requests graceful stop of this run.
func (rs *runState) stop() {
	select {
	case rs.stopCh <- struct{}{}:
	default:
	}
}

// setStatus updates the run status under write lock.
func (rs *runState) setStatus(s model.RunStatus) {
	rs.stateMu.Lock()
	defer rs.stateMu.Unlock()
	rs.status = s
	if s == model.RunRunning {
		rs.startedAt = rs.eng.now()
	}
}

// emitLifecycleTimeout is the max time to wait for a lifecycle event publish.
// Uses context.Background so cancel-path events (step_failed/CANCELED,
// step_finished/SKIPPED, run_finalized) are never dropped when rootCtx is cancelled.
const emitLifecycleTimeout = 5 * time.Second

// emitStepEvent publishes a step lifecycle event to the bus.
func (rs *runState) emitStepEvent(sr *model.StepRun, eventType string) {
	ctx, cancel := context.WithTimeout(context.Background(), emitLifecycleTimeout)
	defer cancel()
	_ = rs.eng.bus.PublishWait(ctx, bus.TopicLifecycle, bus.Message{
		Type: eventType,
		Payload: bus.LifecyclePayload{
			Timestamp: rs.eng.now().Format(time.RFC3339Nano),
			ProcessID: rs.processID,
			RunID:     rs.id,
			StepRunID: sr.ID,
			JobID:     sr.JobID,
			StepID:    sr.StepID,
			Type:      eventType,
			Status:    string(sr.Status),
		},
	})
}

// emitRunEvent publishes a run lifecycle event to the bus.
func (rs *runState) emitRunEvent(eventType, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), emitLifecycleTimeout)
	defer cancel()
	_ = rs.eng.bus.PublishWait(ctx, bus.TopicLifecycle, bus.Message{
		Type: eventType,
		Payload: bus.LifecyclePayload{
			Timestamp: rs.eng.now().Format(time.RFC3339Nano),
			ProcessID: rs.processID,
			RunID:     rs.id,
			Type:      eventType,
			Status:    string(rs.status),
			Message:   message,
		},
	})
}
