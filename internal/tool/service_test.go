package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// syncTool is a test tool with configurable behavior.
type syncTool struct {
	def    model.ToolDef
	plan   ExecutionPlan
	execFn func(ctx context.Context, call *PreparedCall) (*Result, error)
}

func (t *syncTool) Def() model.ToolDef { return t.def }
func (t *syncTool) Prepare(_ context.Context, req CallRequest) (*PreparedCall, error) {
	return &PreparedCall{
		ToolCallID: req.ToolCallID, Name: req.Name, RawArgs: req.RawArgs,
		Plan: t.plan,
	}, nil
}
func (t *syncTool) Execute(ctx context.Context, call *PreparedCall) (*Result, error) {
	if t.execFn != nil {
		return t.execFn(ctx, call)
	}
	return TextResult("ok"), nil
}

// mockSession implements SessionAsyncBridge for testing.
type mockSession struct {
	mu             sync.Mutex
	registered     []string
	finished       []string
	inbox          []model.SessionEntry
	registerErr    error
	finishErr      error
	appendInboxErr error
}

func (m *mockSession) RegisterPendingJob(_ context.Context, _, jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered = append(m.registered, jobID)
	return m.registerErr
}
func (m *mockSession) FinishPendingJob(_ context.Context, _, jobID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finished = append(m.finished, jobID)
	return m.finishErr
}
func (m *mockSession) AppendInbox(_ context.Context, _ string, entry model.SessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inbox = append(m.inbox, entry)
	return m.appendInboxErr
}

func newTestService(tools ...Tool) *Service {
	reg := NewRegistry()
	_ = reg.Register(tools...)
	seq := int64(0)
	return NewService(reg, WithIDGen(func() string {
		return fmt.Sprintf("test_%d", atomic.AddInt64(&seq, 1))
	}))
}

func TestService_SyncOrder(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "echo"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: ParallelSafe},
		execFn: func(_ context.Context, call *PreparedCall) (*Result, error) {
			return TextResult("hello"), nil
		},
	}
	svc := newTestService(tool)
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{
			{ToolCallID: "tc1", Name: "echo"},
			{ToolCallID: "tc2", Name: "echo"},
		},
	})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if resp.Results[0].ToolCallID != "tc1" || resp.Results[1].ToolCallID != "tc2" {
		t.Fatal("results out of order")
	}
}

func TestService_UnknownTool(t *testing.T) {
	svc := newTestService()
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{{ToolCallID: "tc1", Name: "nonexistent"}},
	})
	if !resp.Results[0].IsError {
		t.Fatal("expected error for unknown tool")
	}
}

func TestService_AsyncAck(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "slow"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: GlobalSerial},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			return TextResult("done"), nil
		},
	}
	sess := &mockSession{}
	svc := newTestService(tool)
	svc.session = sess

	resp := svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "slow"}},
	})

	// Should get ack JSON
	if resp.Results[0].IsError {
		t.Fatalf("expected no error, got: %+v", resp.Results[0])
	}
	var ack map[string]string
	text := resp.Results[0].Content[0].Text
	if err := json.Unmarshal([]byte(text), &ack); err != nil {
		t.Fatalf("ack not valid JSON: %s", text)
	}
	if ack["status"] != "dispatched" {
		t.Fatalf("expected dispatched, got %s", ack["status"])
	}

	// Wait for async completion
	time.Sleep(200 * time.Millisecond)
	sess.mu.Lock()
	if len(sess.finished) == 0 {
		t.Fatal("expected FinishPendingJob to be called")
	}
	sess.mu.Unlock()
}

func TestService_AsyncNoSession(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "async_tool"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: GlobalSerial},
	}
	svc := newTestService(tool)
	// No session set
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{{ToolCallID: "tc1", Name: "async_tool"}},
	})
	if !resp.Results[0].IsError {
		t.Fatal("expected error when session is nil")
	}
}

func TestService_RegisterPendingJobFails(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "async_tool"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: GlobalSerial},
	}
	sess := &mockSession{registerErr: fmt.Errorf("db error")}
	svc := newTestService(tool)
	svc.session = sess

	resp := svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "async_tool"}},
	})
	if !resp.Results[0].IsError {
		t.Fatal("expected error when RegisterPendingJob fails")
	}
	// Job should be cleaned up
	if len(svc.ListJobs("")) != 0 {
		t.Fatal("expected no active jobs after failure")
	}
}

func TestService_GlobalSerial(t *testing.T) {
	var running int32
	var maxConcurrent int32

	tool := &syncTool{
		def:  model.ToolDef{Name: "serial"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: GlobalSerial},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			cur := atomic.AddInt32(&running, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur > old {
					if atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
						break
					}
				} else {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return TextResult("ok"), nil
		},
	}
	svc := newTestService(tool)
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{
			{ToolCallID: "tc1", Name: "serial"},
			{ToolCallID: "tc2", Name: "serial"},
			{ToolCallID: "tc3", Name: "serial"},
		},
	})
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}
	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Fatalf("GlobalSerial allowed %d concurrent executions", maxConcurrent)
	}
}

func TestService_ParallelSafe(t *testing.T) {
	var maxConcurrent int32
	var running int32

	tool := &syncTool{
		def:  model.ToolDef{Name: "parallel"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: ParallelSafe},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			cur := atomic.AddInt32(&running, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur > old {
					if atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
						break
					}
				} else {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&running, -1)
			return TextResult("ok"), nil
		},
	}
	svc := newTestService(tool)
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{
			{ToolCallID: "tc1", Name: "parallel"},
			{ToolCallID: "tc2", Name: "parallel"},
		},
	})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if atomic.LoadInt32(&maxConcurrent) < 2 {
		t.Fatal("ParallelSafe should allow concurrent execution")
	}
}

func TestService_ScopedLocking(t *testing.T) {
	var running int32
	var maxConcurrent int32

	makeTool := func(name string, lockKey string, lockMode LockMode) *syncTool {
		return &syncTool{
			def: model.ToolDef{Name: name},
			plan: ExecutionPlan{
				Mode: ExecModeSync, Policy: Scoped,
				Locks: []ResourceLock{{Key: lockKey, Mode: lockMode}},
			},
			execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
				cur := atomic.AddInt32(&running, 1)
				for {
					old := atomic.LoadInt32(&maxConcurrent)
					if cur > old {
						if atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
							break
						}
					} else {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				atomic.AddInt32(&running, -1)
				return TextResult("ok"), nil
			},
		}
	}

	// Two writes to same key should serialize
	reg := NewRegistry()
	w1 := makeTool("w1", "fs:/a", LockWrite)
	w2 := makeTool("w2", "fs:/a", LockWrite)
	_ = reg.Register(w1, w2)
	svc := NewService(reg)

	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{
			{ToolCallID: "tc1", Name: "w1"},
			{ToolCallID: "tc2", Name: "w2"},
		},
	})
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(resp.Results))
	}
	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Fatal("write/write on same key should serialize")
	}
}

func TestService_ScopedEmptyLocks(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "bad"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: Scoped, Locks: nil},
	}
	svc := newTestService(tool)
	resp := svc.ExecBatch(context.Background(), BatchRequest{
		Calls: []CallRequest{{ToolCallID: "tc1", Name: "bad"}},
	})
	if !resp.Results[0].IsError {
		t.Fatal("expected error for scoped policy with empty locks")
	}
}

func TestService_KillJob(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "slow"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: GlobalSerial},
		execFn: func(ctx context.Context, _ *PreparedCall) (*Result, error) {
			<-ctx.Done()
			return TextResult("canceled"), nil
		},
	}
	sess := &mockSession{}
	svc := newTestService(tool)
	svc.session = sess

	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "slow"}},
	})

	// Wait for job to start
	time.Sleep(50 * time.Millisecond)
	jobs := svc.ListJobs("")
	if len(jobs) == 0 {
		t.Fatal("expected active job")
	}

	result := svc.KillJob(jobs[0].JobID)
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)
	if len(svc.ListJobs("")) != 0 {
		t.Fatal("expected no active jobs after kill")
	}
}

func TestService_ListJobs_FilterBySession(t *testing.T) {
	blocker := make(chan struct{})
	tool := &syncTool{
		def:  model.ToolDef{Name: "block"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: ParallelSafe},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			<-blocker
			return TextResult("done"), nil
		},
	}
	sess := &mockSession{}
	svc := newTestService(tool)
	svc.session = sess

	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "block"}},
	})
	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s2",
		Calls:     []CallRequest{{ToolCallID: "tc2", Name: "block"}},
	})

	time.Sleep(50 * time.Millisecond)
	all := svc.ListJobs("")
	s1 := svc.ListJobs("s1")
	if len(all) != 2 {
		t.Fatalf("expected 2 total jobs, got %d", len(all))
	}
	if len(s1) != 1 {
		t.Fatalf("expected 1 job for s1, got %d", len(s1))
	}
	close(blocker)
	time.Sleep(200 * time.Millisecond)
}

func TestService_MixedBatch(t *testing.T) {
	syncT := &syncTool{
		def:  model.ToolDef{Name: "sync_tool"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: ParallelSafe},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			return TextResult("sync_result"), nil
		},
	}
	asyncT := &syncTool{
		def:  model.ToolDef{Name: "async_tool"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: GlobalSerial},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			return TextResult("async_result"), nil
		},
	}
	sess := &mockSession{}
	reg := NewRegistry()
	_ = reg.Register(syncT, asyncT)
	svc := NewService(reg, WithSession(sess))

	resp := svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls: []CallRequest{
			{ToolCallID: "tc1", Name: "sync_tool"},
			{ToolCallID: "tc2", Name: "async_tool"},
			{ToolCallID: "tc3", Name: "sync_tool"},
		},
	})
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}
	// sync results should have content
	if resp.Results[0].Content[0].Text != "sync_result" {
		t.Fatalf("expected sync_result, got %s", resp.Results[0].Content[0].Text)
	}
	// async result should be ack
	var ack map[string]string
	_ = json.Unmarshal([]byte(resp.Results[1].Content[0].Text), &ack)
	if ack["status"] != "dispatched" {
		t.Fatalf("expected dispatched ack, got %s", resp.Results[1].Content[0].Text)
	}
}

func TestService_WaitJobs(t *testing.T) {
	blocker := make(chan struct{})
	tool := &syncTool{
		def:  model.ToolDef{Name: "slow"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: ParallelSafe},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			<-blocker
			return TextResult("done"), nil
		},
	}
	sess := &mockSession{}
	svc := newTestService(tool)
	svc.session = sess

	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "slow"}},
	})

	// HasJobs should be true
	time.Sleep(50 * time.Millisecond)
	if !svc.HasJobs("s1") {
		t.Fatal("expected HasJobs to return true")
	}
	if svc.HasJobs("other") {
		t.Fatal("expected HasJobs to return false for other session")
	}

	// WaitJobs should block until done
	done := make(chan error, 1)
	go func() {
		done <- svc.WaitJobs(context.Background(), "s1")
	}()

	// Should not complete yet
	select {
	case <-done:
		t.Fatal("WaitJobs returned before job completed")
	case <-time.After(50 * time.Millisecond):
	}

	// Unblock the job
	close(blocker)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitJobs: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitJobs timed out")
	}
}

func TestService_WaitJobs_InboxFailureStillCleansPending(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "fail_inbox"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: ParallelSafe},
		execFn: func(_ context.Context, _ *PreparedCall) (*Result, error) {
			return TextResult("done"), nil
		},
	}
	sess := &mockSession{appendInboxErr: fmt.Errorf("disk full")}

	// Collect events with mutex protection (async goroutine emits)
	var mu sync.Mutex
	var events []model.Event
	svc := newTestService(tool)
	svc.session = sess
	svc.emitter = emitterFunc(func(e model.Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "fail_inbox"}},
	})

	// Use WaitJobs for synchronization instead of sleep
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = svc.WaitJobs(ctx, "s1")

	// Job should be cleaned up (unified defer always deletes)
	jobs := svc.ListJobs("s1")
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs after cleanup, got %d", len(jobs))
	}

	// FinishPendingJob should still have been called
	sess.mu.Lock()
	if len(sess.finished) == 0 {
		t.Fatal("expected FinishPendingJob to be called even on inbox failure")
	}
	sess.mu.Unlock()

	// HasJobs should return false
	if svc.HasJobs("s1") {
		t.Fatal("HasJobs should return false after cleanup")
	}

	// Verify EventToolComplete has IsError=true
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range events {
		if e.Type == model.EventToolComplete {
			data := e.Data.(model.ToolCompleteData)
			if !data.IsError {
				t.Error("expected IsError=true on EventToolComplete when inbox fails")
			}
			found = true
		}
	}
	if !found {
		t.Error("expected EventToolComplete event")
	}
}

func TestService_WaitJobs_CtxCancel(t *testing.T) {
	tool := &syncTool{
		def:  model.ToolDef{Name: "slow"},
		plan: ExecutionPlan{Mode: ExecModeAsync, Policy: ParallelSafe},
		execFn: func(ctx context.Context, _ *PreparedCall) (*Result, error) {
			<-ctx.Done()
			return TextResult("canceled"), nil
		},
	}
	sess := &mockSession{}
	svc := newTestService(tool)
	svc.session = sess

	svc.ExecBatch(context.Background(), BatchRequest{
		SessionID: "s1",
		Calls:     []CallRequest{{ToolCallID: "tc1", Name: "slow"}},
	})

	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.WaitJobs(ctx, "s1")
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
}

func TestService_BackfillContextAndEnv(t *testing.T) {
	// Tool that doesn't copy Context/Env in Prepare — Service should backfill
	var capturedCtx RuntimeContext
	var capturedEnv map[string]string

	fakeTool := &syncTool{
		def:  model.ToolDef{Name: "fake"},
		plan: ExecutionPlan{Mode: ExecModeSync, Policy: ParallelSafe},
		execFn: func(_ context.Context, call *PreparedCall) (*Result, error) {
			capturedCtx = call.Context
			capturedEnv = call.Env
			return TextResult("ok"), nil
		},
	}
	svc := newTestService(fakeTool)

	batchCtx := RuntimeContext{
		Set: true,
		Cwd: "/test/dir",
	}
	batchEnv := map[string]string{"KEY": "VALUE"}

	svc.ExecBatch(context.Background(), BatchRequest{
		Calls:   []CallRequest{{ToolCallID: "tc1", Name: "fake"}},
		Context: batchCtx,
		Env:     batchEnv,
	})

	if !capturedCtx.Set {
		t.Error("expected Context.Set=true from backfill")
	}
	if capturedCtx.Cwd != "/test/dir" {
		t.Errorf("expected Cwd=/test/dir, got %q", capturedCtx.Cwd)
	}
	if capturedEnv["KEY"] != "VALUE" {
		t.Errorf("expected KEY=VALUE in env, got %v", capturedEnv)
	}
}
