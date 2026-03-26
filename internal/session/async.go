package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"github.com/sjzar/reed/internal/model"
)

// async manages pending job tracking and inbox event delivery.
type async struct {
	pendingMu     sync.Mutex
	pendingJobs   map[string]map[string]bool // sessionID -> jobID -> pending
	pendingNotify map[string]chan struct{}   // sessionID -> notification channel

	inboxDir string
	inboxMu  sync.Mutex

	closed    chan struct{} // closed on shutdown; unblocks all waiters
	closeOnce sync.Once
	logger    zerolog.Logger
}

func newAsync(inboxDir string, logger zerolog.Logger) *async {
	return &async{
		pendingJobs:   make(map[string]map[string]bool),
		pendingNotify: make(map[string]chan struct{}),
		inboxDir:      inboxDir,
		closed:        make(chan struct{}),
		logger:        logger.With().Str("component", "session.async").Logger(),
	}
}

// registerPendingJob marks a job as pending for the given session.
func (a *async) registerPendingJob(sessionID, jobID string) error {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()

	if _, ok := a.pendingJobs[sessionID]; !ok {
		a.pendingJobs[sessionID] = make(map[string]bool)
	}
	if a.pendingJobs[sessionID][jobID] {
		return fmt.Errorf("job already registered: %s", jobID)
	}
	a.pendingJobs[sessionID][jobID] = true
	return nil
}

// finishPendingJob marks a job as complete and notifies waiters.
func (a *async) finishPendingJob(sessionID, jobID string) error {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()

	jobs, ok := a.pendingJobs[sessionID]
	if !ok || !jobs[jobID] {
		return fmt.Errorf("unknown pending job: %s", jobID)
	}
	delete(jobs, jobID)

	if len(jobs) == 0 {
		delete(a.pendingJobs, sessionID)
		if ch, ok := a.pendingNotify[sessionID]; ok {
			close(ch)
			delete(a.pendingNotify, sessionID)
		}
	}
	return nil
}

// hasPendingJobs returns true if the session has pending jobs.
func (a *async) hasPendingJobs(sessionID string) bool {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	return len(a.pendingJobs[sessionID]) > 0
}

// waitPendingJobs blocks until all pending jobs for the session complete,
// the context is canceled, or the async subsystem is closed.
func (a *async) waitPendingJobs(ctx context.Context, sessionID string) error {
	a.pendingMu.Lock()
	if len(a.pendingJobs[sessionID]) == 0 {
		a.pendingMu.Unlock()
		return nil
	}
	ch, ok := a.pendingNotify[sessionID]
	if !ok {
		ch = make(chan struct{})
		a.pendingNotify[sessionID] = ch
	}
	a.pendingMu.Unlock()

	select {
	case <-ch:
		return nil
	case <-a.closed:
		return fmt.Errorf("session service closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// close signals all waiters and cleans up pending state.
func (a *async) close() {
	a.closeOnce.Do(func() {
		close(a.closed)

		a.pendingMu.Lock()
		defer a.pendingMu.Unlock()
		// Don't close pendingNotify channels — a.closed already unblocks all waiters.
		// Closing both would create ambiguous select results.
		a.pendingJobs = make(map[string]map[string]bool)
		a.pendingNotify = make(map[string]chan struct{})
	})
}

// appendInbox writes an event to the session's inbox file.
func (a *async) appendInbox(sessionID string, entry model.SessionEntry) error {
	if a.inboxDir == "" {
		return nil
	}

	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()

	if err := os.MkdirAll(a.inboxDir, 0755); err != nil {
		return fmt.Errorf("create inbox dir: %w", err)
	}

	path := filepath.Join(a.inboxDir, sessionID+".inbox.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open inbox file: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal inbox entry: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

// fetchAndClearInbox reads all inbox events and clears the inbox file.
func (a *async) fetchAndClearInbox(sessionID string) ([]model.SessionEntry, error) {
	if a.inboxDir == "" {
		return nil, nil
	}

	a.inboxMu.Lock()
	defer a.inboxMu.Unlock()

	path := filepath.Join(a.inboxDir, sessionID+".inbox.jsonl")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}

	var entries []model.SessionEntry
	skipped := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 4<<20), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry model.SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			skipped++
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan inbox: %w", err)
	}

	if skipped > 0 {
		a.logger.Warn().Str("sessionID", sessionID).Int("skipped", skipped).Msg("skipped unparseable inbox lines")
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return entries, fmt.Errorf("clear inbox: %w", err)
	}
	return entries, nil
}
