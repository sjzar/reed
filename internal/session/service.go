package session

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	reedErrors "github.com/sjzar/reed/internal/errors"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/pkg/token"
)

// Service implements the Session interface, delegating to internal components:
//   - router: route resolution + serial locking
//   - history: JSONL persistence + caching + append pipeline
//   - async: pending job tracking + inbox delivery
type Service struct {
	router     *router
	history    *history
	async      *async
	compressor LLMCompressor
	logger     zerolog.Logger
}

// Option configures a Service.
type Option func(*serviceConfig)

type serviceConfig struct {
	inboxDir   string
	compressor LLMCompressor
	cacheTTL   time.Duration
	logger     zerolog.Logger
}

// WithInbox enables the inbox for async event delivery.
func WithInbox(dir string) Option {
	return func(c *serviceConfig) { c.inboxDir = dir }
}

// WithCompressor sets the LLM compressor for context compaction.
func WithCompressor(comp LLMCompressor) Option {
	return func(c *serviceConfig) { c.compressor = comp }
}

// WithCacheTTL sets the cache TTL. Defaults to 45 seconds if not set.
func WithCacheTTL(ttl time.Duration) Option {
	return func(c *serviceConfig) { c.cacheTTL = ttl }
}

// WithLogger sets the logger for the service.
func WithLogger(l zerolog.Logger) Option {
	return func(c *serviceConfig) { c.logger = l }
}

// New creates a Service. If clock or idGen is nil, safe defaults (real system clock
// and UUID generator) are used. The routeStore may be nil if session routing is not
// needed (e.g., all callers use empty sessionKey).
func New(sessionDir string, routeStore RouteStore, clock Clock, idGen IDGenerator, opts ...Option) *Service {
	cfg := &serviceConfig{
		cacheTTL: 45 * time.Second,
		logger:   zerolog.Nop(),
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if clock == nil {
		clock = defaultClock{}
	}
	if idGen == nil {
		idGen = defaultIDGen{}
	}

	return &Service{
		router:     newRouter(routeStore, clock, idGen, cfg.logger),
		history:    newHistory(sessionDir, clock, cfg.cacheTTL, cfg.logger),
		async:      newAsync(cfg.inboxDir, cfg.logger),
		compressor: cfg.compressor,
		logger:     cfg.logger,
	}
}

// --- Session interface implementation ---

// FindSessionID performs a read-only lookup for an existing session route.
func (s *Service) FindSessionID(ctx context.Context, namespace, agentID, sessionKey string) (string, error) {
	return s.router.findSessionID(ctx, namespace, agentID, sessionKey)
}

// Acquire resolves or creates a session route and acquires a per-key serial lock.
func (s *Service) Acquire(ctx context.Context, namespace, agentID, sessionKey string) (string, func(), error) {
	return s.router.acquire(ctx, namespace, agentID, sessionKey)
}

// AcquireByID reverse-looks up a session_id via the route store,
// validates it exists, and acquires a serial lock on that session.
func (s *Service) AcquireByID(ctx context.Context, sessionID string) (func(), error) {
	return s.router.acquireByID(ctx, sessionID)
}

// Load returns the full raw message history for a session (no compaction awareness).
func (s *Service) Load(_ context.Context, sessionID string) ([]model.Message, error) {
	return s.history.load(sessionID)
}

// LoadContext returns the post-last-compaction view (compaction-aware, cached).
func (s *Service) LoadContext(_ context.Context, sessionID string) ([]model.Message, error) {
	return s.history.loadContext(sessionID)
}

// Compact performs LLM-driven context compaction.
func (s *Service) Compact(ctx context.Context, sessionID string, opts CompactOptions) ([]model.Message, error) {
	if s.compressor == nil {
		return nil, reedErrors.New(reedErrors.CodeValidation, "compressor is nil but compaction needed")
	}

	entries, err := s.history.loadEntries(sessionID)
	if err != nil {
		return nil, fmt.Errorf("load raw entries: %w", err)
	}

	postCursorMsgs, postCursorEntryIDs, priorSummary := s.history.walkPostCompaction(sessionID, entries, true)

	if len(postCursorMsgs) == 0 {
		if priorSummary != "" {
			return []model.Message{model.NewTextMessage(model.RoleSystem, priorSummary)}, nil
		}
		return []model.Message{}, nil
	}

	// Split: messages to compress vs messages to keep
	const defaultKeepRecentN = 2
	keepN := opts.KeepRecentN
	if keepN <= 0 {
		keepN = defaultKeepRecentN
	}
	if keepN > len(postCursorMsgs) {
		keepN = len(postCursorMsgs)
	}

	toCompress := postCursorMsgs[:len(postCursorMsgs)-keepN]
	compressedEntryIDs := postCursorEntryIDs[:len(postCursorEntryIDs)-keepN]
	kept := postCursorMsgs[len(postCursorMsgs)-keepN:]

	if len(toCompress) == 0 {
		if priorSummary != "" {
			result := []model.Message{model.NewTextMessage(model.RoleSystem, priorSummary)}
			result = append(result, postCursorMsgs...)
			return result, nil
		}
		return postCursorMsgs, nil
	}

	// If there's a prior summary, prepend it as context for the compressor
	compressInput := toCompress
	if priorSummary != "" {
		compressInput = make([]model.Message, 0, len(toCompress)+1)
		compressInput = append(compressInput, model.NewTextMessage(model.RoleSystem, priorSummary))
		compressInput = append(compressInput, toCompress...)
	}

	summary, err := s.compressor.Compress(ctx, compressInput)
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}

	cursor := compressedEntryIDs[len(compressedEntryIDs)-1]

	var estimatedTokens int
	for _, msg := range postCursorMsgs {
		estimatedTokens += token.Estimate(msg.TextContent())
	}
	var tokensAfter int
	tokensAfter += token.Estimate(summary)
	for _, msg := range kept {
		tokensAfter += token.Estimate(msg.TextContent())
	}
	record := model.NewCompactionSessionEntry(cursor, summary, estimatedTokens, tokensAfter)
	if err := s.history.appendEntry(ctx, sessionID, record); err != nil {
		return nil, fmt.Errorf("persist compaction: %w", err)
	}

	s.history.invalidateCache(sessionID)

	result := []model.Message{model.NewTextMessage(model.RoleSystem, summary)}
	result = append(result, kept...)
	return result, nil
}

// AppendMessages persists a batch of messages to the session JSONL.
func (s *Service) AppendMessages(ctx context.Context, sessionID string, msgs []model.Message) error {
	return s.history.appendMessages(ctx, sessionID, msgs)
}

// RegisterPendingJob marks a job as pending for the given session.
func (s *Service) RegisterPendingJob(_ context.Context, sessionID, jobID string) error {
	return s.async.registerPendingJob(sessionID, jobID)
}

// FinishPendingJob marks a job as complete and notifies waiters.
func (s *Service) FinishPendingJob(_ context.Context, sessionID, jobID string) error {
	return s.async.finishPendingJob(sessionID, jobID)
}

// HasPendingJobs returns true if the session has pending jobs.
func (s *Service) HasPendingJobs(sessionID string) bool {
	return s.async.hasPendingJobs(sessionID)
}

// WaitPendingJobs blocks until all pending jobs for the session complete or ctx is canceled.
func (s *Service) WaitPendingJobs(ctx context.Context, sessionID string) error {
	return s.async.waitPendingJobs(ctx, sessionID)
}

// AppendInbox writes an event to the session's inbox file.
func (s *Service) AppendInbox(_ context.Context, sessionID string, entry model.SessionEntry) error {
	return s.async.appendInbox(sessionID, entry)
}

// FetchAndClearInbox reads all inbox events and clears the inbox file.
func (s *Service) FetchAndClearInbox(_ context.Context, sessionID string) ([]model.SessionEntry, error) {
	return s.async.fetchAndClearInbox(sessionID)
}

// Close closes all open file handles and signals async waiters.
func (s *Service) Close() error {
	s.async.close()
	return s.history.close()
}

// --- Test accessors (same-package only) ---

// appendEntry is a convenience accessor for tests that need to inject raw entries.
func (s *Service) appendEntry(ctx context.Context, sessionID string, entry model.SessionEntry) error {
	return s.history.appendEntry(ctx, sessionID, entry)
}

// loadEntries is a convenience accessor for tests that need to inspect raw entries.
func (s *Service) loadEntries(sessionID string) ([]model.SessionEntry, error) {
	return s.history.loadEntries(sessionID)
}

// testSessionDir returns the history directory path for tests.
func (s *Service) testSessionDir() string {
	return s.history.dir
}

// testSetSessionDir overrides the history directory for tests.
func (s *Service) testSetSessionDir(dir string) {
	s.history.dir = dir
}

// defaultClock uses the real system clock.
type defaultClock struct{}

func (defaultClock) Now() time.Time { return time.Now() }

// defaultIDGen generates UUIDs as session IDs.
type defaultIDGen struct{}

func (defaultIDGen) NewSessionID() string { return uuid.New().String() }
