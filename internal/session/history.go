package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/sjzar/reed/internal/model"
)

// history manages JSONL persistence, message caching, and the append pipeline.
type history struct {
	dir   string
	clock Clock

	writersMu  sync.Mutex
	writers    map[string]*bufio.Writer
	files      map[string]*os.File
	writerUsed map[string]time.Time // last-used time for idle eviction
	maxOpenFDs int                  // max open file descriptors; 0 = unlimited

	cacheMu  sync.RWMutex
	cache    map[string]*cacheEntry
	cacheTTL time.Duration

	leafMu      sync.Mutex
	leafIDs     map[string]string            // sessionID → last message entry ID
	toolCallMap map[string]map[string]string // sessionID → { toolCallID → assistantEntryID }

	logger zerolog.Logger
}

// cacheEntry holds a cached copy of a session's compaction-aware messages.
type cacheEntry struct {
	messages []model.Message
	loadedAt time.Time
}

func newHistory(dir string, clock Clock, cacheTTL time.Duration, logger zerolog.Logger) *history {
	return &history{
		dir:         dir,
		clock:       clock,
		writers:     make(map[string]*bufio.Writer),
		files:       make(map[string]*os.File),
		writerUsed:  make(map[string]time.Time),
		maxOpenFDs:  64,
		cache:       make(map[string]*cacheEntry),
		cacheTTL:    cacheTTL,
		leafIDs:     make(map[string]string),
		toolCallMap: make(map[string]map[string]string),
		logger:      logger.With().Str("component", "session.history").Logger(),
	}
}

// load returns the full raw message history for a session (no compaction awareness).
func (h *history) load(sessionID string) ([]model.Message, error) {
	entries, err := h.loadEntries(sessionID)
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	for _, e := range entries {
		if e.Type == model.SessionEntryMessage && e.Message != nil {
			msgs = append(msgs, *e.Message)
		}
	}
	return msgs, nil
}

// loadContext returns the post-last-compaction view (compaction-aware, cached).
func (h *history) loadContext(sessionID string) ([]model.Message, error) {
	// Check cache
	h.cacheMu.RLock()
	if e, ok := h.cache[sessionID]; ok && h.clock.Now().Sub(e.loadedAt) < h.cacheTTL {
		msgs := make([]model.Message, len(e.messages))
		for i, m := range e.messages {
			msgs[i] = m.Clone()
		}
		h.cacheMu.RUnlock()
		return msgs, nil
	}
	h.cacheMu.RUnlock()

	entries, err := h.loadEntries(sessionID)
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	msgs, _, priorSummary := h.walkPostCompaction(sessionID, entries, false)

	// Filter out system messages from history.
	filtered := msgs[:0]
	for _, m := range msgs {
		if m.Role != model.RoleSystem {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if priorSummary != "" {
		result := make([]model.Message, 0, 1+len(msgs))
		result = append(result, model.NewTextMessage(model.RoleSystem, priorSummary))
		result = append(result, msgs...)
		msgs = result
	}

	// Update cache (deep copy)
	h.cacheMu.Lock()
	cached := make([]model.Message, len(msgs))
	for i, m := range msgs {
		cached[i] = m.Clone()
	}
	h.cache[sessionID] = &cacheEntry{messages: cached, loadedAt: h.clock.Now()}
	h.cacheMu.Unlock()

	return msgs, nil
}

// appendMessages persists a batch of messages to the session JSONL.
// Manages parentID, toolCallMap, leafID, and cache updates as a single pipeline.
func (h *history) appendMessages(ctx context.Context, sessionID string, msgs []model.Message) error {
	h.leafMu.Lock()
	defer h.leafMu.Unlock()

	// Lazy-init leafID and toolCallMap from persisted entries if not yet loaded.
	if _, ok := h.leafIDs[sessionID]; !ok {
		if err := h.initSessionState(sessionID); err != nil {
			return err
		}
	}

	currentLeaf := h.leafIDs[sessionID]
	for _, msg := range msgs {
		parentID := currentLeaf

		// Tool result → look up toolCallMap for the assistant entry ID.
		var matchedToolCallID string
		if msg.Role == model.RoleTool && msg.ToolCallID != "" {
			if tcMap := h.toolCallMap[sessionID]; tcMap != nil {
				if aID, ok := tcMap[msg.ToolCallID]; ok {
					parentID = aID
					matchedToolCallID = msg.ToolCallID
				}
			}
		}

		entry := model.NewMessageSessionEntry(msg, parentID)
		if len(msg.Meta) > 0 {
			entry.Meta = make(map[string]any, len(msg.Meta))
			for k, v := range msg.Meta {
				entry.Meta[k] = v
			}
		}
		if err := h.appendEntry(ctx, sessionID, entry); err != nil {
			return err
		}

		// Clean up toolCallMap only after successful write.
		if matchedToolCallID != "" {
			tcMap := h.toolCallMap[sessionID]
			delete(tcMap, matchedToolCallID)
			if len(tcMap) == 0 {
				delete(h.toolCallMap, sessionID)
			}
		}

		currentLeaf = entry.ID
		h.leafIDs[sessionID] = currentLeaf

		// Assistant message with tool calls → cache toolCallID → entryID.
		if msg.Role == model.RoleAssistant && len(msg.ToolCalls) > 0 {
			if h.toolCallMap[sessionID] == nil {
				h.toolCallMap[sessionID] = make(map[string]string)
			}
			for _, tc := range msg.ToolCalls {
				h.toolCallMap[sessionID][tc.ID] = entry.ID
			}
		}

		// Update cache — skip system messages to match the filter in loadContext.
		h.cacheMu.Lock()
		if e, ok := h.cache[sessionID]; ok && msg.Role != model.RoleSystem {
			e.messages = append(e.messages, msg.Clone())
		}
		h.cacheMu.Unlock()
	}
	return nil
}

// invalidateCache removes the cache entry for a session.
func (h *history) invalidateCache(sessionID string) {
	h.cacheMu.Lock()
	delete(h.cache, sessionID)
	h.cacheMu.Unlock()
}

// initSessionState scans the session JSONL to initialize leafID and toolCallMap.
// Must be called with leafMu held.
func (h *history) initSessionState(sessionID string) error {
	entries, err := h.loadEntries(sessionID)
	if err != nil {
		return fmt.Errorf("init session state: %w", err)
	}
	if len(entries) == 0 {
		h.leafIDs[sessionID] = ""
		return nil
	}

	// Find last message entry for leafID.
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == model.SessionEntryMessage {
			h.leafIDs[sessionID] = entries[i].ID
			break
		}
	}
	if _, ok := h.leafIDs[sessionID]; !ok {
		h.leafIDs[sessionID] = ""
	}

	// Rebuild toolCallMap
	pending := make(map[string]string)
	matched := make(map[string]bool)
	for _, e := range entries {
		if e.Type != model.SessionEntryMessage || e.Message == nil {
			continue
		}
		if e.Message.Role == model.RoleAssistant {
			for _, tc := range e.Message.ToolCalls {
				pending[tc.ID] = e.ID
			}
		}
		if e.Message.Role == model.RoleTool && e.Message.ToolCallID != "" {
			matched[e.Message.ToolCallID] = true
		}
	}
	for id := range matched {
		delete(pending, id)
	}
	if len(pending) > 0 {
		h.toolCallMap[sessionID] = pending
	}
	return nil
}

// walkPostCompaction scans entries for the last compaction event and collects
// post-cursor messages.
func (h *history) walkPostCompaction(
	sessionID string, entries []model.SessionEntry, trackIDs bool,
) (msgs []model.Message, entryIDs []string, priorSummary string) {
	var lastCompaction *model.SessionEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == model.SessionEntryCompaction {
			lastCompaction = &entries[i]
			break
		}
	}

	collectAll := func() {
		for _, e := range entries {
			if e.Type == model.SessionEntryMessage && e.Message != nil {
				msgs = append(msgs, *e.Message)
				if trackIDs {
					entryIDs = append(entryIDs, e.ID)
				}
			}
		}
	}

	if lastCompaction == nil {
		collectAll()
		return
	}

	priorSummary = lastCompaction.Summary
	pastCursor := false
	for _, e := range entries {
		if e.ID == lastCompaction.Cursor {
			pastCursor = true
			continue
		}
		if pastCursor && e.Type == model.SessionEntryMessage && e.Message != nil {
			msgs = append(msgs, *e.Message)
			if trackIDs {
				entryIDs = append(entryIDs, e.ID)
			}
		}
	}

	if !pastCursor {
		h.logger.Warn().
			Str("sessionID", sessionID).
			Str("cursor", lastCompaction.Cursor).
			Msg("compaction cursor not found in entries, falling back to all messages")
		msgs = nil
		entryIDs = nil
		priorSummary = ""
		collectAll()
	}
	return
}

// loadEntries reads and parses all lines from the session JSONL file.
// It flushes any buffered writer for the session first to avoid reading
// a partially written last line (torn read).
func (h *history) loadEntries(sessionID string) ([]model.SessionEntry, error) {
	h.writersMu.Lock()
	if bw, ok := h.writers[sessionID]; ok {
		if err := bw.Flush(); err != nil {
			h.writersMu.Unlock()
			return nil, fmt.Errorf("flush before read: %w", err)
		}
	}
	h.writersMu.Unlock()

	path := filepath.Join(h.dir, sessionID+".jsonl")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	var entries []model.SessionEntry
	skipped := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry model.SessionEntry
		if err := json.Unmarshal(line, &entry); err == nil && entry.Type != "" {
			entries = append(entries, entry)
			continue
		}
		skipped++
	}

	if skipped > 0 {
		h.logger.Warn().Str("sessionID", sessionID).Int("skipped", skipped).Msg("skipped unparseable JSONL lines")
	}

	return entries, scanner.Err()
}

// appendEntry writes a single SessionEntry to the session's JSONL file.
func (h *history) appendEntry(_ context.Context, sessionID string, entry model.SessionEntry) error {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	if entry.Timestamp == 0 {
		entry.Timestamp = h.clock.Now().UnixMilli()
	}

	h.writersMu.Lock()
	defer h.writersMu.Unlock()

	bw, err := h.getWriter(sessionID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal session entry: %w", err)
	}
	// Guard: reject entries that exceed the scanner buffer used in loadEntries.
	// Without this, a write succeeds but subsequent reads fail with "token too long".
	const maxEntryBytes = 1024 * 1024 // must match scanner buffer in loadEntries
	if len(data) > maxEntryBytes {
		return fmt.Errorf("session entry exceeds %d byte limit (%d bytes)", maxEntryBytes, len(data))
	}
	if _, err := bw.Write(data); err != nil {
		return err
	}
	if err := bw.WriteByte('\n'); err != nil {
		return err
	}
	return bw.Flush()
}

// getWriter returns a cached bufio.Writer for the session. Must be called with writersMu held.
func (h *history) getWriter(sessionID string) (*bufio.Writer, error) {
	if bw, ok := h.writers[sessionID]; ok {
		h.writerUsed[sessionID] = h.clock.Now()
		return bw, nil
	}

	// Evict idle writers if at capacity
	if h.maxOpenFDs > 0 && len(h.writers) >= h.maxOpenFDs {
		h.evictIdleWriter()
	}

	if err := os.MkdirAll(h.dir, 0755); err != nil {
		return nil, fmt.Errorf("create session dir: %w", err)
	}

	path := filepath.Join(h.dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open session file: %w", err)
	}

	bw := bufio.NewWriter(f)
	h.files[sessionID] = f
	h.writers[sessionID] = bw
	h.writerUsed[sessionID] = h.clock.Now()
	return bw, nil
}

// evictIdleWriter closes the least-recently-used writer. Must be called with writersMu held.
func (h *history) evictIdleWriter() {
	var oldestID string
	var oldestTime time.Time
	for id, t := range h.writerUsed {
		if oldestID == "" || t.Before(oldestTime) {
			oldestID = id
			oldestTime = t
		}
	}
	if oldestID == "" {
		return
	}
	if bw, ok := h.writers[oldestID]; ok {
		if err := bw.Flush(); err != nil {
			h.logger.Warn().Err(err).Str("sessionID", oldestID).Msg("flush evicted writer")
		}
	}
	if f, ok := h.files[oldestID]; ok {
		if err := f.Close(); err != nil {
			h.logger.Warn().Err(err).Str("sessionID", oldestID).Msg("close evicted file")
		}
	}
	delete(h.writers, oldestID)
	delete(h.files, oldestID)
	delete(h.writerUsed, oldestID)
}

// close flushes all writers and closes all file handles.
func (h *history) close() error {
	h.writersMu.Lock()
	defer h.writersMu.Unlock()

	var firstErr error
	for id, bw := range h.writers {
		if err := bw.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		if f, ok := h.files[id]; ok {
			if err := f.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	h.writers = make(map[string]*bufio.Writer)
	h.files = make(map[string]*os.File)
	h.writerUsed = make(map[string]time.Time)
	return firstErr
}
