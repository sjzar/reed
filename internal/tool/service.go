package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// SessionAsyncBridge is the interface for async job lifecycle management.
type SessionAsyncBridge interface {
	RegisterPendingJob(ctx context.Context, sessionID, jobID string) error
	FinishPendingJob(ctx context.Context, sessionID, jobID string) error
	AppendInbox(ctx context.Context, sessionID string, entry model.SessionEntry) error
}

// EventEmitter emits lifecycle events.
type EventEmitter interface {
	Emit(event model.Event)
}

// BatchRequest is the input for a batch of tool calls.
type BatchRequest struct {
	SessionID string
	Calls     []CallRequest
	Context   RuntimeContext    // batch-level default
	Env       map[string]string // batch-level default
}

// BatchResponse is the output of a batch of tool calls.
type BatchResponse struct {
	Results []CallResult
}

// JobInfo describes an active async job.
type JobInfo struct {
	JobID      string
	SessionID  string
	ToolCallID string
	ToolName   string
	Status     JobStatus
	StartedAt  time.Time
}

// activeJob tracks a running async job.
type activeJob struct {
	mu        sync.Mutex
	info      JobInfo
	startMono time.Time // monotonic clock for duration measurement
	cancel    context.CancelFunc
	done      chan struct{}
}

func (j *activeJob) setStatus(s JobStatus) {
	j.mu.Lock()
	j.info.Status = s
	j.mu.Unlock()
}

func (j *activeJob) getInfo() JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.info
}

var idCounter atomic.Uint64

// Service is the unified tool execution service.
type Service struct {
	registry *Registry
	session  SessionAsyncBridge
	emitter  EventEmitter
	locks    *LockScheduler
	serial   chan struct{} // capacity 1 token for GlobalSerial
	jobs     sync.Map      // jobID → *activeJob
	idGen    func() string
}

// ServiceOption configures a Service.
type ServiceOption func(*Service)

// WithSession sets the session async bridge.
func WithSession(s SessionAsyncBridge) ServiceOption {
	return func(svc *Service) { svc.session = s }
}

// WithEmitter sets the event emitter.
func WithEmitter(e EventEmitter) ServiceOption {
	return func(svc *Service) { svc.emitter = e }
}

// WithEmitterFunc sets the event emitter from a function.
func WithEmitterFunc(fn func(model.Event)) ServiceOption {
	return func(svc *Service) { svc.emitter = emitterFunc(fn) }
}

type emitterFunc func(model.Event)

func (f emitterFunc) Emit(e model.Event) { f(e) }

// WithIDGen overrides the ID generator (for testing).
func WithIDGen(fn func() string) ServiceOption {
	return func(svc *Service) { svc.idGen = fn }
}

// NewService creates a new tool execution Service.
func NewService(reg *Registry, opts ...ServiceOption) *Service {
	svc := &Service{
		registry: reg,
		locks:    NewLockScheduler(),
		serial:   make(chan struct{}, 1),
		idGen: func() string {
			return fmt.Sprintf("id_%d", idCounter.Add(1))
		},
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

func (s *Service) emit(e model.Event) {
	if s.emitter != nil {
		s.emitter.Emit(e)
	}
}

// ExecBatch executes a batch of tool calls.
func (s *Service) ExecBatch(ctx context.Context, req BatchRequest) BatchResponse {
	batchID := s.idGen()
	results := make([]CallResult, len(req.Calls))

	var wg sync.WaitGroup
	for i, call := range req.Calls {
		// Normalize ToolCallID
		if call.ToolCallID == "" {
			call.ToolCallID = fmt.Sprintf("tc_%s_%d", batchID, i)
		}

		// Inject batch-level defaults before Prepare
		if !call.Context.Set {
			call.Context = req.Context
		}
		if len(call.Env) == 0 && len(req.Env) > 0 {
			call.Env = req.Env
		}

		// Look up tool
		t, ok := s.registry.Get(call.Name)
		if !ok {
			results[i] = CallResult{
				ToolCallID: call.ToolCallID,
				ToolName:   call.Name,
				Content:    model.TextContent(fmt.Sprintf("error: unknown tool %q", call.Name)),
				IsError:    true,
			}
			continue
		}

		// Prepare
		prepared, err := t.Prepare(ctx, call)
		if err != nil {
			results[i] = CallResult{
				ToolCallID: call.ToolCallID,
				ToolName:   call.Name,
				Content:    model.TextContent(fmt.Sprintf("error: prepare %s: %s", call.Name, err)),
				IsError:    true,
			}
			continue
		}

		// Backfill: if tool's Prepare didn't set Context/Env, inherit from call
		if !prepared.Context.Set {
			prepared.Context = call.Context
		}
		if len(prepared.Env) == 0 && len(call.Env) > 0 {
			prepared.Env = make(map[string]string, len(call.Env))
			for k, v := range call.Env {
				prepared.Env[k] = v
			}
		}

		// Async dispatch
		if prepared.Plan.Mode == ExecModeAsync {
			results[i] = s.dispatchAsync(ctx, req.SessionID, t, prepared)
			continue
		}

		// Sync dispatch — run concurrently
		idx := i
		tool := t
		pc := prepared
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[idx] = *s.runWithPlan(ctx, tool, pc)
		}()
	}
	wg.Wait()
	return BatchResponse{Results: results}
}

// runWithPlan executes a tool call with the appropriate concurrency policy.
func (s *Service) runWithPlan(ctx context.Context, t Tool, call *PreparedCall) *CallResult {
	start := time.Now()

	s.emit(model.Event{
		Type: model.EventToolStart, Timestamp: start.UTC(),
		Data: model.ToolStartData{ToolCallID: call.ToolCallID, ToolName: call.Name},
	})

	result := s.executeWithPolicy(ctx, t, call)
	result = sanitizeResult(result)

	cr := &CallResult{
		ToolCallID: call.ToolCallID,
		ToolName:   call.Name,
		Content:    result.Content,
		IsError:    result.IsError,
		DurationMs: time.Since(start).Milliseconds(),
	}

	s.emit(model.Event{
		Type: model.EventToolEnd, Timestamp: time.Now().UTC(),
		Data: model.ToolEndData{
			ToolCallID: call.ToolCallID, ToolName: call.Name,
			IsError: result.IsError, Duration: time.Since(start),
		},
	})

	return cr
}

func (s *Service) executeWithPolicy(ctx context.Context, t Tool, call *PreparedCall) *Result {
	switch call.Plan.Policy {
	case GlobalSerial:
		select {
		case s.serial <- struct{}{}:
			defer func() { <-s.serial }()
		case <-ctx.Done():
			return ErrorResult(fmt.Sprintf("error: context canceled waiting for serial lock: %s", ctx.Err()))
		}
	case Scoped:
		if len(call.Plan.Locks) == 0 {
			return ErrorResult("error: scoped policy requires at least one lock")
		}
		release, err := s.locks.Acquire(ctx, call.Plan.Locks)
		if err != nil {
			return ErrorResult(fmt.Sprintf("error: acquire locks: %s", err))
		}
		defer release()
	case ParallelSafe:
		// no locking
	}

	timeout := call.Plan.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := t.Execute(execCtx, call)
	if err != nil {
		return ErrorResult(fmt.Sprintf("error: %s", err))
	}
	return result
}

// dispatchAsync dispatches an async tool call.
func (s *Service) dispatchAsync(ctx context.Context, sessionID string, t Tool, call *PreparedCall) CallResult {
	if s.session == nil {
		return CallResult{
			ToolCallID: call.ToolCallID,
			ToolName:   call.Name,
			Content:    model.TextContent("error: async execution requires session bridge"),
			IsError:    true,
		}
	}

	jobID := fmt.Sprintf("job_%s", s.idGen())

	// Create job entry first
	jobCtx := context.WithoutCancel(ctx)
	jobCtx, jobCancel := context.WithCancel(jobCtx)
	done := make(chan struct{})

	now := time.Now()
	job := &activeJob{
		info: JobInfo{
			JobID:      jobID,
			SessionID:  sessionID,
			ToolCallID: call.ToolCallID,
			ToolName:   call.Name,
			Status:     JobQueued,
			StartedAt:  now.UTC(),
		},
		startMono: now,
		cancel:    jobCancel,
		done:      done,
	}
	s.jobs.Store(jobID, job)

	// Register with session
	if err := s.session.RegisterPendingJob(ctx, sessionID, jobID); err != nil {
		s.jobs.Delete(jobID)
		jobCancel()
		return CallResult{
			ToolCallID: call.ToolCallID,
			ToolName:   call.Name,
			Content:    model.TextContent(fmt.Sprintf("error: register pending job: %s", err)),
			IsError:    true,
		}
	}

	s.emit(model.Event{
		Type: model.EventToolDispatch, Timestamp: time.Now().UTC(),
		Data: model.ToolDispatchData{ToolCallID: call.ToolCallID, ToolName: call.Name, JobID: jobID},
	})

	// Launch background goroutine
	go s.runAsyncJob(jobCtx, sessionID, t, call, job)

	// Return ack
	ack, _ := json.Marshal(map[string]string{
		"status":     "dispatched",
		"jobID":      jobID,
		"toolCallID": call.ToolCallID,
		"toolName":   call.Name,
	})
	return CallResult{
		ToolCallID: call.ToolCallID,
		ToolName:   call.Name,
		Content:    model.TextContent(string(ack)),
	}
}

func (s *Service) runAsyncJob(ctx context.Context, sessionID string, t Tool, call *PreparedCall, job *activeJob) {
	defer close(job.done)
	cleanupCtx := context.WithoutCancel(ctx)
	info := job.getInfo()
	startMono := job.startMono

	// Unified cleanup: always finish pending + delete job + emit completion
	var callResult *CallResult
	defer func() {
		// Always attempt to finish pending job
		if err := s.session.FinishPendingJob(cleanupCtx, sessionID, info.JobID); err != nil {
			s.emit(model.Event{
				Type: model.EventError, Timestamp: time.Now().UTC(),
				Data: model.ErrorData{
					Error: fmt.Sprintf("async job %s: finish pending failed: %s", info.JobID, err),
				},
			})
		}

		// Determine final status
		isError := callResult == nil || callResult.IsError
		if isError {
			job.setStatus(JobFailed)
		} else {
			job.setStatus(JobCompleted)
		}

		// Emit completion event
		s.emit(model.Event{
			Type: model.EventToolComplete, Timestamp: time.Now().UTC(),
			Data: model.ToolCompleteData{
				JobID: info.JobID, ToolCallID: call.ToolCallID,
				ToolName: call.Name, IsError: isError,
				Duration: time.Since(startMono),
			},
		})

		// Always remove from active jobs
		s.jobs.Delete(info.JobID)
	}()

	// Update status
	job.setStatus(JobRunning)

	// Execute
	callResult = s.runWithPlan(ctx, t, call)

	// Build inbox payload with full Content (preserves tool message semantics)
	inboxPayload, _ := json.Marshal(map[string]any{
		"toolCallID": call.ToolCallID,
		"toolName":   call.Name,
		"isError":    callResult.IsError,
		"content":    callResult.Content,
		"durationMs": callResult.DurationMs,
	})

	// Write to inbox
	entry := model.NewCustomSessionEntry("inbox", map[string]any{
		"sessionID": sessionID,
		"jobID":     info.JobID,
		"payload":   string(inboxPayload),
	})
	if err := s.session.AppendInbox(cleanupCtx, sessionID, entry); err != nil {
		s.emit(model.Event{
			Type: model.EventError, Timestamp: time.Now().UTC(),
			Data: model.ErrorData{
				Error: fmt.Sprintf("async job %s: append inbox failed: %s", info.JobID, err),
			},
		})
		// Override callResult so defer marks JobFailed (not JobCompleted)
		callResult = &CallResult{
			ToolCallID: call.ToolCallID,
			ToolName:   call.Name,
			Content:    model.TextContent(fmt.Sprintf("tool result lost: inbox write failed: %s", err)),
			IsError:    true,
		}
	}
}

// KillJob cancels an active async job.
func (s *Service) KillJob(jobID string) string {
	val, ok := s.jobs.Load(jobID)
	if !ok {
		return fmt.Sprintf("error: job %s not found", jobID)
	}
	job := val.(*activeJob)
	job.cancel()
	job.setStatus(JobCancelling)

	info := job.getInfo()
	s.emit(model.Event{
		Type: model.EventToolCancel, Timestamp: time.Now().UTC(),
		Data: model.ToolCancelData{JobID: jobID, ToolName: info.ToolName},
	})

	// Wait up to 3 seconds
	select {
	case <-job.done:
		return fmt.Sprintf("job %s canceled", jobID)
	case <-time.After(3 * time.Second):
		return fmt.Sprintf("cancel requested for job %s, waiting for tool to respond", jobID)
	}
}

// ListJobs returns active jobs, optionally filtered by sessionID.
func (s *Service) ListJobs(sessionID string) []JobInfo {
	var jobs []JobInfo
	s.jobs.Range(func(_, val any) bool {
		job := val.(*activeJob)
		info := job.getInfo()
		if sessionID == "" || info.SessionID == sessionID {
			jobs = append(jobs, info)
		}
		return true
	})
	return jobs
}

// HasJobs returns true if there are active (non-terminal) async jobs for the given session.
func (s *Service) HasJobs(sessionID string) bool {
	found := false
	s.jobs.Range(func(_, val any) bool {
		job := val.(*activeJob)
		info := job.getInfo()
		if info.SessionID == sessionID && info.Status != JobFailed && info.Status != JobCompleted {
			found = true
			return false
		}
		return true
	})
	return found
}

// WaitJobs blocks until all non-terminal async jobs for the given session complete or ctx is canceled.
func (s *Service) WaitJobs(ctx context.Context, sessionID string) error {
	for {
		var ch <-chan struct{}
		s.jobs.Range(func(_, val any) bool {
			job := val.(*activeJob)
			info := job.getInfo()
			if info.SessionID == sessionID && info.Status != JobFailed && info.Status != JobCompleted {
				ch = job.done
				return false // stop at first match
			}
			return true
		})
		if ch == nil {
			return nil // no active jobs left
		}
		select {
		case <-ch:
			// one finished, loop to check for more
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Registry returns the underlying tool registry.
func (s *Service) Registry() *Registry {
	return s.registry
}
