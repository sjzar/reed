package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// --- Test helpers ---

type mockClock struct {
	now time.Time
}

func (c *mockClock) Now() time.Time { return c.now }

type mockIDGen struct {
	mu    sync.Mutex
	count int
}

func (g *mockIDGen) NewSessionID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.count++
	return fmt.Sprintf("sess_%04d", g.count)
}

type memRouteStore struct {
	mu   sync.Mutex
	data map[string]*model.SessionRouteRow
}

func newMemRouteStore() *memRouteStore {
	return &memRouteStore{data: make(map[string]*model.SessionRouteRow)}
}

func (s *memRouteStore) Upsert(_ context.Context, row *model.SessionRouteRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := row.Namespace + "|" + row.AgentID + "|" + row.SessionKey
	s.data[key] = row
	return nil
}

func (s *memRouteStore) Find(_ context.Context, ns, agent, sk string) (*model.SessionRouteRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data[ns+"|"+agent+"|"+sk]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (s *memRouteStore) Delete(_ context.Context, ns, agent, sk string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, ns+"|"+agent+"|"+sk)
	return nil
}

func (s *memRouteStore) FindBySessionID(_ context.Context, sessionID string) (*model.SessionRouteRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data {
		if r.CurrentSessionID == sessionID {
			return r, nil
		}
	}
	return nil, nil
}

type mockLLMCompressor struct {
	summary   string
	err       error
	called    bool
	lastInput []model.Message
}

func (m *mockLLMCompressor) Compress(_ context.Context, msgs []model.Message) (string, error) {
	m.called = true
	m.lastInput = make([]model.Message, len(msgs))
	copy(m.lastInput, msgs)
	return m.summary, m.err
}

func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	dir := t.TempDir()
	clk := &mockClock{now: time.Now()}
	return New(dir, newMemRouteStore(), clk, &mockIDGen{}, opts...)
}

// --- Tests ---

func TestFindSessionID_Empty(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.FindSessionID(context.Background(), "ns", "agent", "")
	if err == nil {
		t.Error("expected error for empty sessionKey")
	}
}

func TestFindSessionID_NotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.FindSessionID(context.Background(), "ns", "agent", "key")
	if err == nil {
		t.Error("expected error for non-existent route")
	}
}

func TestAcquire_EmptySessionKey(t *testing.T) {
	svc := newTestService(t)
	id, release, err := svc.Acquire(context.Background(), "ns", "agent", "")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()
	if id == "" {
		t.Error("expected non-empty session ID")
	}
}

func TestAcquire_WithSessionKey(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id1, release1, err := svc.Acquire(ctx, "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release1()

	// Same key should return same session ID (same logical day)
	id2, release2, err := svc.Acquire(ctx, "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release2()

	if id1 != id2 {
		t.Errorf("expected same session ID, got %q and %q", id1, id2)
	}
}

func TestAcquire_FindAfterAcquire(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, release, err := svc.Acquire(ctx, "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()

	found, err := svc.FindSessionID(ctx, "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("FindSessionID: %v", err)
	}
	if found != id {
		t.Errorf("FindSessionID: got %q, want %q", found, id)
	}
}

func TestAppendMessages_And_Load(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
		model.NewTextMessage(model.RoleAssistant, "hi there"),
	}
	if err := svc.AppendMessages(ctx, "sess1", msgs); err != nil {
		t.Fatalf("AppendMessages: %v", err)
	}

	loaded, err := svc.Load(ctx, "sess1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].TextContent() != "hello" {
		t.Errorf("msg[0]: got %q", loaded[0].TextContent())
	}
	if loaded[1].TextContent() != "hi there" {
		t.Errorf("msg[1]: got %q", loaded[1].TextContent())
	}
}

func TestLoadContext_NoCompaction(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	msgs := []model.Message{model.NewTextMessage(model.RoleUser, "test")}
	_ = svc.AppendMessages(ctx, "sess1", msgs)

	loaded, err := svc.LoadContext(ctx, "sess1")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded))
	}
}

func TestCompact(t *testing.T) {
	comp := &mockLLMCompressor{summary: "Summary of conversation"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write enough messages to have something to compact
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "old question"),
		model.NewTextMessage(model.RoleAssistant, "old answer"),
		model.NewTextMessage(model.RoleUser, "new question"),
		model.NewTextMessage(model.RoleAssistant, "new answer"),
	}
	_ = svc.AppendMessages(ctx, "sess1", msgs)

	result, err := svc.Compact(ctx, "sess1", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !comp.called {
		t.Error("compressor was not called")
	}
	// result should be: [summary system msg] + [new question] + [new answer]
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != model.RoleSystem {
		t.Errorf("first message should be system, got %s", result[0].Role)
	}
	if result[0].TextContent() != "Summary of conversation" {
		t.Errorf("summary: got %q", result[0].TextContent())
	}

	// Load should return raw full history (all 4 messages, no compaction awareness)
	loaded, err := svc.Load(ctx, "sess1")
	if err != nil {
		t.Fatalf("Load after compact: %v", err)
	}
	if len(loaded) != 4 {
		t.Errorf("expected 4 raw messages after compaction, got %d", len(loaded))
	}

	// LoadContext should return compacted view
	contextMsgs, err := svc.LoadContext(ctx, "sess1")
	if err != nil {
		t.Fatalf("LoadContext after compact: %v", err)
	}
	if len(contextMsgs) != 3 {
		t.Errorf("expected 3 context messages after compaction, got %d", len(contextMsgs))
	}
}

func TestCompact_NoCompressor(t *testing.T) {
	svc := newTestService(t) // no compressor
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess1", []model.Message{model.NewTextMessage(model.RoleUser, "hi")})
	_, err := svc.Compact(ctx, "sess1", CompactOptions{})
	if err == nil {
		t.Error("expected error without compressor")
	}
}

func TestPendingJobs(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.RegisterPendingJob(ctx, "sess1", "job1"); err != nil {
		t.Fatalf("RegisterPendingJob: %v", err)
	}
	if !svc.HasPendingJobs("sess1") {
		t.Error("expected pending after register")
	}
	if err := svc.FinishPendingJob(ctx, "sess1", "job1"); err != nil {
		t.Fatalf("FinishPendingJob: %v", err)
	}
	if svc.HasPendingJobs("sess1") {
		t.Error("expected no pending after finish")
	}
}

func TestWaitPendingJobs(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_ = svc.RegisterPendingJob(ctx, "sess1", "job1")

	done := make(chan struct{})
	go func() {
		_ = svc.WaitPendingJobs(ctx, "sess1")
		close(done)
	}()

	_ = svc.FinishPendingJob(ctx, "sess1", "job1")

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitPendingJobs timed out")
	}
}

func TestInbox(t *testing.T) {
	dir := t.TempDir()
	clk := &mockClock{now: time.Now()}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{}, WithInbox(dir))
	ctx := context.Background()

	entry := model.NewCustomSessionEntry("inbox", map[string]any{
		"jobID":   "job1",
		"payload": "result",
	})
	if err := svc.AppendInbox(ctx, "sess1", entry); err != nil {
		t.Fatalf("AppendInbox: %v", err)
	}

	entries, err := svc.FetchAndClearInbox(ctx, "sess1")
	if err != nil {
		t.Fatalf("FetchAndClearInbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// Second fetch should be empty
	entries, err = svc.FetchAndClearInbox(ctx, "sess1")
	if err != nil {
		t.Fatalf("FetchAndClearInbox: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestInbox_NotConfigured(t *testing.T) {
	svc := newTestService(t) // no inbox
	ctx := context.Background()

	// Should not error when inbox is not configured
	if err := svc.AppendInbox(ctx, "sess1", model.SessionEntry{}); err != nil {
		t.Fatalf("AppendInbox with no inbox: %v", err)
	}
	entries, err := svc.FetchAndClearInbox(ctx, "sess1")
	if err != nil {
		t.Fatalf("FetchAndClearInbox with no inbox: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %v", entries)
	}
}

func TestResolveRoute_TTL(t *testing.T) {
	dir := t.TempDir()
	clk := &mockClock{now: time.Date(2026, 3, 13, 10, 0, 0, 0, time.Local)}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{})
	ctx := context.Background()

	// First acquire
	id1, release1, _ := svc.Acquire(ctx, "ns", "a", "k")
	release1()

	// Same logical day — same session
	clk.now = time.Date(2026, 3, 13, 23, 59, 0, 0, time.Local)
	id2, release2, _ := svc.Acquire(ctx, "ns", "a", "k")
	release2()
	if id2 != id1 {
		t.Errorf("expected same session on same day, got %q and %q", id1, id2)
	}

	// 03:59 next day — still same logical day (before 4am)
	clk.now = time.Date(2026, 3, 14, 3, 59, 0, 0, time.Local)
	id3, release3, _ := svc.Acquire(ctx, "ns", "a", "k")
	release3()
	if id3 != id1 {
		t.Errorf("expected same session before 4am, got %q and %q", id1, id3)
	}

	// 04:01 — crosses logical day boundary → new session
	clk.now = time.Date(2026, 3, 14, 4, 1, 0, 0, time.Local)
	id4, release4, _ := svc.Acquire(ctx, "ns", "a", "k")
	release4()
	if id4 == id1 {
		t.Error("expected new session after 4am boundary")
	}
}

func TestSerialGuard(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	id, release, err := svc.Acquire(ctx, "ns", "agent", "key")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Second acquire with short timeout should fail (lock held)
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()

	_, _, err = svc.Acquire(timeoutCtx, "ns", "agent", "key")
	if err == nil {
		t.Error("expected timeout error for second acquire")
	}

	release()

	// Now it should succeed
	id2, release2, err := svc.Acquire(ctx, "ns", "agent", "key")
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	release2()

	if id2 != id {
		t.Errorf("expected same session ID, got %q and %q", id, id2)
	}
}

func TestDoubleCompaction(t *testing.T) {
	callCount := 0
	svc := newTestService(t, WithCompressor(&countingCompressor{count: &callCount}))
	ctx := context.Background()

	// Write 6 messages
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
		model.NewTextMessage(model.RoleUser, "q3"),
		model.NewTextMessage(model.RoleAssistant, "a3"),
	}
	_ = svc.AppendMessages(ctx, "sess-dc", msgs)

	// First compaction: keep 2, compress 4
	result1, err := svc.Compact(ctx, "sess-dc", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}
	if len(result1) != 3 { // summary + q3 + a3
		t.Fatalf("first compact: expected 3 messages, got %d", len(result1))
	}

	// Add more messages
	moreMsgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q4"),
		model.NewTextMessage(model.RoleAssistant, "a4"),
	}
	_ = svc.AppendMessages(ctx, "sess-dc", moreMsgs)

	// Second compaction: should only compress post-first-cursor messages (q3, a3),
	// keeping q4, a4
	result2, err := svc.Compact(ctx, "sess-dc", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if len(result2) != 3 { // summary + q4 + a4
		t.Fatalf("second compact: expected 3 messages, got %d", len(result2))
	}

	// Verify the kept messages are q4 and a4 (not old ones)
	if result2[1].TextContent() != "q4" {
		t.Errorf("second compact kept[0]: got %q, want q4", result2[1].TextContent())
	}
	if result2[2].TextContent() != "a4" {
		t.Errorf("second compact kept[1]: got %q, want a4", result2[2].TextContent())
	}

	// LoadContext should show the second compaction view
	loaded, err := svc.LoadContext(ctx, "sess-dc")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(loaded) != 3 { // summary + q4 + a4
		t.Fatalf("LoadContext: expected 3, got %d", len(loaded))
	}

	// Load (raw) should return all 8 original messages
	raw, err := svc.Load(ctx, "sess-dc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(raw) != 8 {
		t.Errorf("Load raw: expected 8, got %d", len(raw))
	}
}

// countingCompressor tracks call count and produces distinct summaries.
type countingCompressor struct {
	count *int
}

func (c *countingCompressor) Compress(_ context.Context, msgs []model.Message) (string, error) {
	*c.count++
	return fmt.Sprintf("summary-%d (from %d messages)", *c.count, len(msgs)), nil
}

func TestCacheMutationSafety(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess-cache", []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
		model.NewTextMessage(model.RoleAssistant, "world"),
	})

	// First load populates cache
	msgs1, err := svc.LoadContext(ctx, "sess-cache")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	// Mutate the returned slice
	msgs1[0] = model.NewTextMessage(model.RoleUser, "MUTATED")

	// Second load should return original data from cache, not mutated
	msgs2, err := svc.LoadContext(ctx, "sess-cache")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if msgs2[0].TextContent() == "MUTATED" {
		t.Error("cache was corrupted by caller mutation")
	}
	if msgs2[0].TextContent() != "hello" {
		t.Errorf("expected 'hello', got %q", msgs2[0].TextContent())
	}
}

func TestLoadVsLoadContext(t *testing.T) {
	comp := &mockLLMCompressor{summary: "compressed history"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "old"),
		model.NewTextMessage(model.RoleAssistant, "old reply"),
		model.NewTextMessage(model.RoleUser, "new"),
	}
	_ = svc.AppendMessages(ctx, "sess-lv", msgs)

	// Compact: keep 1, compress 2
	_, err := svc.Compact(ctx, "sess-lv", CompactOptions{KeepRecentN: 1})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Load returns raw: all 3 original messages
	raw, err := svc.Load(ctx, "sess-lv")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(raw) != 3 {
		t.Errorf("Load: expected 3 raw messages, got %d", len(raw))
	}

	// LoadContext returns compacted view: summary + kept
	ctx2, err := svc.LoadContext(ctx, "sess-lv")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(ctx2) != 2 { // summary + "new"
		t.Errorf("LoadContext: expected 2 messages, got %d", len(ctx2))
	}
	if ctx2[0].Role != model.RoleSystem {
		t.Errorf("LoadContext[0]: expected system role, got %s", ctx2[0].Role)
	}
	if ctx2[1].TextContent() != "new" {
		t.Errorf("LoadContext[1]: expected 'new', got %q", ctx2[1].TextContent())
	}
}

func TestCompact_ZeroKeepRecentN_DefaultsToSafe(t *testing.T) {
	comp := &mockLLMCompressor{summary: "safe summary"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write 4 messages
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
	}
	_ = svc.AppendMessages(ctx, "sess-zero", msgs)

	// Compact with zero KeepRecentN — should default to 2
	result, err := svc.Compact(ctx, "sess-zero", CompactOptions{KeepRecentN: 0})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Should keep at least 2 recent messages: summary + q2 + a2
	if len(result) != 3 {
		t.Fatalf("expected 3 messages (summary + 2 kept), got %d", len(result))
	}
	if result[1].TextContent() != "q2" {
		t.Errorf("kept[0]: got %q, want q2", result[1].TextContent())
	}
	if result[2].TextContent() != "a2" {
		t.Errorf("kept[1]: got %q, want a2", result[2].TextContent())
	}
}

func TestCompact_NothingToCompress_NoEmptySystemMsg(t *testing.T) {
	comp := &mockLLMCompressor{summary: "should not be called"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write only 2 messages — with default keepN=2, nothing to compress
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "only question"),
		model.NewTextMessage(model.RoleAssistant, "only answer"),
	}
	_ = svc.AppendMessages(ctx, "sess-noop", msgs)

	result, err := svc.Compact(ctx, "sess-noop", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Nothing to compress, no prior summary — should return messages directly, no system prefix
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role == model.RoleSystem {
		t.Error("should not inject empty system message when nothing to compress")
	}
	if comp.called {
		t.Error("compressor should not have been called when nothing to compress")
	}
}

func TestLoadContext_MissingCursorFallback(t *testing.T) {
	comp := &mockLLMCompressor{summary: "test summary"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write messages and compact
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
	}
	_ = svc.AppendMessages(ctx, "sess-corrupt", msgs)
	_, err := svc.Compact(ctx, "sess-corrupt", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Corrupt the JSONL: rewrite with same messages but the compaction entry will
	// reference a cursor ID that no longer exists in the new entries.
	// Simulate by closing, rewriting the file with fresh entries + a bogus compaction.
	_ = svc.Close()

	// Rebuild: write fresh messages with new IDs, then a compaction pointing to a non-existent cursor
	dir := svc.testSessionDir()
	svc2 := New(dir, newMemRouteStore(), &mockClock{now: time.Now()}, &mockIDGen{},
		WithCompressor(comp))

	freshMsgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "fresh q1"),
		model.NewTextMessage(model.RoleAssistant, "fresh a1"),
	}
	_ = svc2.AppendMessages(ctx, "sess-corrupt2", freshMsgs)

	// Manually append a compaction entry with a bogus cursor
	bogusCompaction := model.NewCompactionSessionEntry("nonexistent-cursor-id", "old summary", 100, 0)
	_ = svc2.appendEntry(ctx, "sess-corrupt2", bogusCompaction)

	// LoadContext should fall back to full history instead of returning empty
	loaded, err := svc2.LoadContext(ctx, "sess-corrupt2")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages (fallback to full history), got %d", len(loaded))
	}
	if loaded[0].TextContent() != "fresh q1" {
		t.Errorf("msg[0]: got %q, want 'fresh q1'", loaded[0].TextContent())
	}
}

func TestCompact_MissingCursorFallback(t *testing.T) {
	comp := &mockLLMCompressor{summary: "recompressed"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write messages
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
	}
	_ = svc.AppendMessages(ctx, "sess-cc", msgs)

	// Manually append a compaction entry with a bogus cursor
	bogusCompaction := model.NewCompactionSessionEntry("bogus-cursor", "old summary", 100, 0)
	_ = svc.appendEntry(ctx, "sess-cc", bogusCompaction)

	// Compact should fall back to treating all messages as post-cursor
	result, err := svc.Compact(ctx, "sess-cc", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if !comp.called {
		t.Error("compressor should have been called")
	}
	// Should compress 2 and keep 2: summary + q2 + a2
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
	if result[0].Role != model.RoleSystem {
		t.Errorf("result[0]: expected system role, got %s", result[0].Role)
	}
}

func TestCompact_FullyCompactedReturnsExistingSummary(t *testing.T) {
	comp := &mockLLMCompressor{summary: "should not be called"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write 2 messages
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
	}
	_ = svc.AppendMessages(ctx, "sess-full", msgs)

	// Load entries to find the last message entry ID
	entries, err := svc.loadEntries("sess-full")
	if err != nil {
		t.Fatalf("loadEntries: %v", err)
	}
	var lastMsgEntryID string
	for _, e := range entries {
		if e.Type == model.SessionEntryMessage {
			lastMsgEntryID = e.ID
		}
	}
	if lastMsgEntryID == "" {
		t.Fatal("no message entries found")
	}

	// Manually append a compaction entry whose cursor is the very last message,
	// simulating a session where everything has been compacted with no remaining messages.
	compEntry := model.NewCompactionSessionEntry(lastMsgEntryID, "everything summarized", 100, 0)
	_ = svc.appendEntry(ctx, "sess-full", compEntry)

	// Now Compact: postCursorMsgs is empty, but priorSummary exists.
	// Before fix: returns nil, nil — breaking callers.
	// After fix: returns [summary system msg].
	result, err := svc.Compact(ctx, "sess-full", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if comp.called {
		t.Error("compressor should not be called when there are no new messages")
	}
	if result == nil {
		t.Fatal("Compact returned nil; expected [summary] when prior summary exists")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message (summary), got %d", len(result))
	}
	if result[0].Role != model.RoleSystem {
		t.Errorf("expected system role, got %s", result[0].Role)
	}
	if result[0].TextContent() != "everything summarized" {
		t.Errorf("expected prior summary text, got %q", result[0].TextContent())
	}
}

func TestCompact_EmptySession_ReturnsEmptySlice(t *testing.T) {
	comp := &mockLLMCompressor{summary: "unused"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	result, err := svc.Compact(ctx, "nonexistent-session", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 messages, got %d", len(result))
	}
	if comp.called {
		t.Error("compressor should not be called on empty session")
	}
}

func TestCompact_CompressorError(t *testing.T) {
	comp := &mockLLMCompressor{err: fmt.Errorf("LLM unavailable")}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
	}
	_ = svc.AppendMessages(ctx, "sess-err", msgs)

	_, err := svc.Compact(ctx, "sess-err", CompactOptions{KeepRecentN: 2})
	if err == nil {
		t.Fatal("expected error from compressor")
	}
	if !comp.called {
		t.Error("compressor should have been called")
	}
}

func TestCompact_PriorSummaryInjectedIntoCompressor(t *testing.T) {
	comp := &mockLLMCompressor{summary: "new summary"}
	svc := newTestService(t, WithCompressor(comp))
	ctx := context.Background()

	// Write messages, compact once
	msgs := []model.Message{
		model.NewTextMessage(model.RoleUser, "q1"),
		model.NewTextMessage(model.RoleAssistant, "a1"),
		model.NewTextMessage(model.RoleUser, "q2"),
		model.NewTextMessage(model.RoleAssistant, "a2"),
	}
	_ = svc.AppendMessages(ctx, "sess-prior", msgs)
	comp.summary = "first summary"
	_, err := svc.Compact(ctx, "sess-prior", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("first Compact: %v", err)
	}

	// Add more messages
	more := []model.Message{
		model.NewTextMessage(model.RoleUser, "q3"),
		model.NewTextMessage(model.RoleAssistant, "a3"),
		model.NewTextMessage(model.RoleUser, "q4"),
		model.NewTextMessage(model.RoleAssistant, "a4"),
	}
	_ = svc.AppendMessages(ctx, "sess-prior", more)

	// Second compact — prior summary should be injected as first message to compressor
	comp.summary = "second summary"
	comp.called = false
	comp.lastInput = nil
	_, err = svc.Compact(ctx, "sess-prior", CompactOptions{KeepRecentN: 2})
	if err != nil {
		t.Fatalf("second Compact: %v", err)
	}
	if !comp.called {
		t.Fatal("compressor should have been called")
	}
	if len(comp.lastInput) == 0 {
		t.Fatal("compressor received no input")
	}
	// First message in compressor input should be the prior summary
	if comp.lastInput[0].Role != model.RoleSystem {
		t.Errorf("expected system role for prior summary, got %s", comp.lastInput[0].Role)
	}
	if comp.lastInput[0].TextContent() != "first summary" {
		t.Errorf("expected prior summary 'first summary', got %q", comp.lastInput[0].TextContent())
	}
}

func TestRegisterPendingJob_Duplicate(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if err := svc.RegisterPendingJob(ctx, "sess1", "job1"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := svc.RegisterPendingJob(ctx, "sess1", "job1")
	if err == nil {
		t.Error("expected error for duplicate job registration")
	}
}

func TestFinishPendingJob_Unknown(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	err := svc.FinishPendingJob(ctx, "sess1", "nonexistent")
	if err == nil {
		t.Error("expected error for unknown job")
	}
}

func TestWaitPendingJobs_Canceled(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_ = svc.RegisterPendingJob(ctx, "sess1", "job1")

	cancelCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- svc.WaitPendingJobs(cancelCtx, "sess1")
	}()

	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitPendingJobs did not return after cancel")
	}
}

func TestLoadContext_CacheTTLExpiry(t *testing.T) {
	dir := t.TempDir()
	clk := &mockClock{now: time.Now()}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{}, WithCacheTTL(1*time.Second))
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess-ttl", []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
	})

	// First load — populates cache
	msgs1, err := svc.LoadContext(ctx, "sess-ttl")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(msgs1) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs1))
	}

	// Append another message directly (bypasses cache update since we use appendEntry)
	entry := model.NewMessageSessionEntry(model.NewTextMessage(model.RoleAssistant, "world"), "")
	_ = svc.appendEntry(ctx, "sess-ttl", entry)

	// Advance mock clock past cache TTL
	clk.now = clk.now.Add(2 * time.Second)

	// Second load should re-read from disk and see both messages
	msgs2, err := svc.LoadContext(ctx, "sess-ttl")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(msgs2) != 2 {
		t.Errorf("expected 2 messages after TTL expiry, got %d", len(msgs2))
	}
}

func TestClose_FlushAndReopen(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess-close", []model.Message{
		model.NewTextMessage(model.RoleUser, "before close"),
	})

	if err := svc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After close, appending again should reopen the file
	_ = svc.AppendMessages(ctx, "sess-close", []model.Message{
		model.NewTextMessage(model.RoleAssistant, "after reopen"),
	})

	loaded, err := svc.Load(ctx, "sess-close")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].TextContent() != "before close" {
		t.Errorf("msg[0]: got %q", loaded[0].TextContent())
	}
	if loaded[1].TextContent() != "after reopen" {
		t.Errorf("msg[1]: got %q", loaded[1].TextContent())
	}
}

func TestNew_NilClockAndIDGen(t *testing.T) {
	dir := t.TempDir()
	// nil clock and idGen should not panic — safe defaults kick in
	svc := New(dir, newMemRouteStore(), nil, nil)

	// Acquire with empty key should return a valid ID using the default IDGenerator
	id, release, err := svc.Acquire(context.Background(), "ns", "agent", "")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()
	if id == "" {
		t.Error("expected non-empty session ID from default IDGenerator")
	}
}

func TestAcquire_EmptyKey_UsesIDGenerator(t *testing.T) {
	dir := t.TempDir()
	gen := &mockIDGen{}
	svc := New(dir, newMemRouteStore(), &mockClock{now: time.Now()}, gen)

	id, release, err := svc.Acquire(context.Background(), "ns", "agent", "")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	// mockIDGen produces "sess_0001" on first call
	if id != "sess_0001" {
		t.Errorf("expected injected ID 'sess_0001', got %q", id)
	}
	if gen.count != 1 {
		t.Errorf("expected IDGenerator called once, got %d", gen.count)
	}
}

// --- Phase 0 regression tests ---

func TestAcquire_AcquireByID_MutualExclusion(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Acquire via route key
	sessionID, release1, err := svc.Acquire(ctx, "ns", "agent", "key1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// AcquireByID for the same session should block (same lock key)
	timeoutCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()

	_, err = svc.AcquireByID(timeoutCtx, sessionID)
	if err == nil {
		t.Error("expected timeout error: AcquireByID should be blocked by Acquire holding the same lock")
	}

	release1()

	// Now AcquireByID should succeed
	release2, err := svc.AcquireByID(ctx, sessionID)
	if err != nil {
		t.Fatalf("AcquireByID after release: %v", err)
	}
	release2()
}

func TestFindSessionID_RouteNotFound(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.FindSessionID(context.Background(), "ns", "agent", "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent route, got nil")
	}
}

func TestCacheMutationSafety_DeepContent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess-deep", []model.Message{
		{Role: model.RoleUser, Content: []model.Content{
			{Type: model.ContentTypeText, Text: "original"},
		}},
		model.NewTextMessage(model.RoleAssistant, "reply"),
	})

	// First load populates cache
	msgs1, err := svc.LoadContext(ctx, "sess-deep")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	// Mutate the Content slice of the returned message
	msgs1[0].Content[0].Text = "MUTATED"

	// Second load should return original data from cache
	msgs2, err := svc.LoadContext(ctx, "sess-deep")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if msgs2[0].Content[0].Text == "MUTATED" {
		t.Error("cache was corrupted by deep Content mutation")
	}
	if msgs2[0].Content[0].Text != "original" {
		t.Errorf("expected 'original', got %q", msgs2[0].Content[0].Text)
	}
}

func TestCacheMutationSafety_Usage(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	usage := &model.Usage{Input: 10, Output: 20, Total: 30}
	_ = svc.AppendMessages(ctx, "sess-usage", []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
		{Role: model.RoleAssistant, Content: model.TextContent("reply"), Usage: usage},
	})

	// First load populates cache
	msgs1, err := svc.LoadContext(ctx, "sess-usage")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if msgs1[1].Usage == nil {
		t.Fatal("expected non-nil Usage")
	}

	// Mutate Usage pointer
	msgs1[1].Usage.Input = 9999

	// Second load should return original Usage
	msgs2, err := svc.LoadContext(ctx, "sess-usage")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if msgs2[1].Usage == nil {
		t.Fatal("expected non-nil Usage on second load")
	}
	if msgs2[1].Usage.Input == 9999 {
		t.Error("cache was corrupted by Usage pointer mutation")
	}
	if msgs2[1].Usage.Input != 10 {
		t.Errorf("expected Usage.Input=10, got %d", msgs2[1].Usage.Input)
	}
}

func TestCacheExpiry_WithMockClock(t *testing.T) {
	dir := t.TempDir()
	clk := &mockClock{now: time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{}, WithCacheTTL(30*time.Second))
	ctx := context.Background()

	_ = svc.AppendMessages(ctx, "sess-clk", []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
	})

	// First load — cache populated at clk.now
	_, err := svc.LoadContext(ctx, "sess-clk")
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}

	// Append via appendEntry (bypasses cache update)
	entry := model.NewMessageSessionEntry(model.NewTextMessage(model.RoleAssistant, "world"), "")
	_ = svc.appendEntry(ctx, "sess-clk", entry)

	// Still within TTL — cache should be used (1 message)
	clk.now = clk.now.Add(10 * time.Second)
	msgs, _ := svc.LoadContext(ctx, "sess-clk")
	if len(msgs) != 1 {
		t.Errorf("expected 1 message from cache within TTL, got %d", len(msgs))
	}

	// Advance past TTL — cache should be expired
	clk.now = clk.now.Add(25 * time.Second)
	msgs, _ = svc.LoadContext(ctx, "sess-clk")
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages after TTL expiry, got %d", len(msgs))
	}
}

func TestInitSessionState_DiskError(t *testing.T) {
	// Point sessionDir at a file (not a directory) to cause loadEntries to fail
	dir := t.TempDir()
	clk := &mockClock{now: time.Now()}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{})

	// Create a file where the session JSONL would try to read from a directory
	// that is actually a file
	badDir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(badDir, []byte("not a directory"), 0644); err != nil {
		t.Fatal(err)
	}
	svc.testSetSessionDir(badDir)

	err := svc.AppendMessages(context.Background(), "sess1", []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
	})
	if err == nil {
		t.Error("expected error from AppendMessages when session dir is invalid")
	}
}

func TestInbox_LargeEntry(t *testing.T) {
	dir := t.TempDir()
	clk := &mockClock{now: time.Now()}
	svc := New(dir, newMemRouteStore(), clk, &mockIDGen{}, WithInbox(dir))
	ctx := context.Background()

	// Create a large payload (~500KB)
	largeText := make([]byte, 500*1024)
	for i := range largeText {
		largeText[i] = 'A' + byte(i%26)
	}

	entry := model.NewCustomSessionEntry("large", map[string]any{
		"payload": string(largeText),
	})
	if err := svc.AppendInbox(ctx, "sess-large", entry); err != nil {
		t.Fatalf("AppendInbox: %v", err)
	}

	entries, err := svc.FetchAndClearInbox(ctx, "sess-large")
	if err != nil {
		t.Fatalf("FetchAndClearInbox: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}
