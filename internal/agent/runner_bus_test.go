package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
)

// mockInboxAppender records AppendInbox calls for assertion.
type mockInboxAppender struct {
	mu      sync.Mutex
	entries []model.SessionEntry
}

func (m *mockInboxAppender) AppendInbox(_ context.Context, _ string, entry model.SessionEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockInboxAppender) getEntries() []model.SessionEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]model.SessionEntry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

// minimalSessionProvider embeds mockInboxAppender and stubs the rest of
// SessionProvider so we can test bus subscriber in isolation.
type minimalSessionProvider struct {
	*mockInboxAppender
}

func (m *minimalSessionProvider) Acquire(_ context.Context, _, _, _ string) (string, func(), error) {
	return "", func() {}, nil
}
func (m *minimalSessionProvider) AcquireByID(_ context.Context, _ string) (func(), error) {
	return func() {}, nil
}
func (m *minimalSessionProvider) LoadContext(_ context.Context, _ string) ([]model.Message, error) {
	return nil, nil
}
func (m *minimalSessionProvider) Compact(_ context.Context, _ string, _ session.CompactOptions) ([]model.Message, error) {
	return nil, nil
}
func (m *minimalSessionProvider) AppendMessages(_ context.Context, _ string, _ []model.Message) error {
	return nil
}
func (m *minimalSessionProvider) FetchAndClearInbox(_ context.Context, _ string) ([]model.SessionEntry, error) {
	return nil, nil
}
func (m *minimalSessionProvider) HasPendingJobs(_ string) bool                      { return false }
func (m *minimalSessionProvider) WaitPendingJobs(_ context.Context, _ string) error { return nil }

func TestBusSubscriber_NilBus(t *testing.T) {
	stop := startBusSubscriber(context.Background(), nil, "sr_001", "sess_001", nil)
	stop() // should not panic
}

func TestBusSubscriber_EmptyStepRunID(t *testing.T) {
	b := bus.New()
	defer b.Close()
	stop := startBusSubscriber(context.Background(), b, "", "sess_001", nil)
	stop() // should not panic
}

func TestBusSubscriber_SteerMessage(t *testing.T) {
	b := bus.New()
	defer b.Close()

	inbox := &mockInboxAppender{}
	sess := &minimalSessionProvider{mockInboxAppender: inbox}

	stepRunID := "sr_steer_001"
	sessionID := "sess_steer_001"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := startBusSubscriber(ctx, b, stepRunID, sessionID, sess)
	defer stop()

	// Publish a steer message
	b.Publish(bus.StepInputTopic(stepRunID), bus.Message{
		Type:    "steer",
		Payload: bus.SteerPayload{Message: "change direction"},
	})

	// Wait for the handler to process
	deadline := time.After(2 * time.Second)
	for {
		entries := inbox.getEntries()
		if len(entries) > 0 {
			entry := entries[0]
			if entry.Type != model.SessionEntryMessage {
				t.Errorf("entry type: got %q, want %q", entry.Type, model.SessionEntryMessage)
			}
			if entry.Message == nil {
				t.Fatal("entry.Message is nil")
			}
			if entry.Message.Role != model.RoleUser {
				t.Errorf("message role: got %q, want %q", entry.Message.Role, model.RoleUser)
			}
			if entry.Message.TextContent() != "change direction" {
				t.Errorf("message text: got %q, want %q", entry.Message.TextContent(), "change direction")
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for AppendInbox call")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestBusSubscriber_UnknownType(t *testing.T) {
	b := bus.New()
	defer b.Close()

	inbox := &mockInboxAppender{}
	sess := &minimalSessionProvider{mockInboxAppender: inbox}

	stepRunID := "sr_unknown_001"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := startBusSubscriber(ctx, b, stepRunID, "sess", sess)
	defer stop()

	// Publish an unknown message type
	b.Publish(bus.StepInputTopic(stepRunID), bus.Message{
		Type:    "bogus",
		Payload: "whatever",
	})

	// Give the goroutine time to process
	time.Sleep(50 * time.Millisecond)

	if entries := inbox.getEntries(); len(entries) != 0 {
		t.Errorf("expected no inbox entries for unknown type, got %d", len(entries))
	}
}

func TestBusSubscriber_ContextCancel(t *testing.T) {
	b := bus.New()
	defer b.Close()

	inbox := &mockInboxAppender{}
	sess := &minimalSessionProvider{mockInboxAppender: inbox}

	ctx, cancel := context.WithCancel(context.Background())
	stop := startBusSubscriber(ctx, b, "sr_cancel_001", "sess", sess)

	cancel()

	// stop should return promptly after ctx cancel
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return after context cancel")
	}
}
