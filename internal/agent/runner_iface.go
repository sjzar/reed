package agent

import (
	"context"
	"io"

	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/internal/tool"
)

// AIService abstracts the AI provider for the Runner.
type AIService interface {
	Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error)
	ModelMetadataFor(modelRef string) model.ModelMetadata
}

// ProfileProvider resolves a named profile into runtime defaults.
type ProfileProvider interface {
	ResolveProfile(ctx context.Context, name string) (*ResolvedProfile, error)
}

// SkillProvider abstracts skill listing and mounting for the Runner.
type SkillProvider interface {
	ListAndMount(runRoot string, ids []string) ([]skill.SkillInfo, error)
}

// MediaService is the interface for media operations used by the agent runner.
// It supports upload (deflation), get (validation), and resolve (URI resolution).
type MediaService interface {
	Upload(ctx context.Context, r io.Reader, mimeType string) (*model.MediaEntry, error)
	Get(ctx context.Context, id string) (*model.MediaEntry, error)
}

// SessionProvider abstracts the session methods the runner uses when
// driving an agent execution loop. It is a subset of session.Session scoped
// to the operations that the runner requires.
type SessionProvider interface {
	// Acquire resolves or creates a session route and acquires a per-key serial
	// lock. The caller must invoke the returned release func when the run ends.
	Acquire(ctx context.Context, namespace, agentID, sessionKey string) (string, func(), error)

	// AcquireByID reverse-looks up a session by its ID, validates it exists,
	// and acquires a serial lock on that session.
	// The caller must invoke the returned release func when the run ends.
	AcquireByID(ctx context.Context, sessionID string) (func(), error)

	// LoadContext returns the compaction-aware durable context for the session:
	// [summary_system_msg] + messages after the last compaction cursor, or the
	// full history when no compaction has occurred.
	LoadContext(ctx context.Context, sessionID string) ([]model.Message, error)

	// Compact performs LLM-driven compaction on the session's durable history.
	// Messages older than opts.KeepRecentN are compressed into a summary.
	// Returns summary + recent messages; returns an empty (non-nil) slice when
	// no messages exist.
	Compact(ctx context.Context, sessionID string, opts session.CompactOptions) ([]model.Message, error)

	// AppendMessages persists a batch of messages to the session JSONL store.
	AppendMessages(ctx context.Context, sessionID string, msgs []model.Message) error

	// FetchAndClearInbox reads all queued inbox events for the session and
	// atomically clears the inbox file.
	FetchAndClearInbox(ctx context.Context, sessionID string) ([]model.SessionEntry, error)

	// HasPendingJobs reports whether the session has any outstanding async jobs.
	HasPendingJobs(sessionID string) bool

	// WaitPendingJobs blocks until all pending async jobs for the session finish
	// or ctx is canceled.
	WaitPendingJobs(ctx context.Context, sessionID string) error

	// AppendInbox writes a single session entry to the session's inbox file so it
	// can be picked up by FetchAndClearInbox on the next drain cycle.
	AppendInbox(ctx context.Context, sessionID string, entry model.SessionEntry) error
}

// ToolExecutor is a flat interface that the runner uses to execute tool
// calls and introspect the available tool set. It deliberately hides the
// tool.Service / Registry two-layer structure so that callers depend only on
// the capabilities they need.
type ToolExecutor interface {
	// ExecBatch executes a batch of tool calls and returns their results.
	// Results preserve the ordering of req.Calls.
	ExecBatch(ctx context.Context, req tool.BatchRequest) tool.BatchResponse

	// ListTools returns ToolDef descriptors for the given tool IDs.
	// An empty or nil ids slice returns descriptors for all registered tools.
	// An unknown ID causes an error.
	ListTools(ids []string) ([]model.ToolDef, error)

	// ListToolsLenient is like ListTools but silently skips unknown IDs.
	// Used for skill-derived tool lists where IDs may use provider-specific names.
	ListToolsLenient(ids []string) []model.ToolDef

	// CoreToolIDs returns the names of all tools registered in GroupCore,
	// sorted alphabetically.
	CoreToolIDs() []string
}

// toolExecutorAdapter adapts *tool.Service to the ToolExecutor interface.
type toolExecutorAdapter struct {
	svc *tool.Service
}

// ExecBatch delegates to the underlying tool.Service.
func (a *toolExecutorAdapter) ExecBatch(ctx context.Context, req tool.BatchRequest) tool.BatchResponse {
	return a.svc.ExecBatch(ctx, req)
}

// ListTools delegates to the underlying tool.Registry.
func (a *toolExecutorAdapter) ListTools(ids []string) ([]model.ToolDef, error) {
	return a.svc.Registry().ListTools(ids)
}

// ListToolsLenient delegates to the underlying tool.Registry.
func (a *toolExecutorAdapter) ListToolsLenient(ids []string) []model.ToolDef {
	return a.svc.Registry().ListToolsLenient(ids)
}

// CoreToolIDs delegates to the underlying tool.Registry.
func (a *toolExecutorAdapter) CoreToolIDs() []string {
	return a.svc.Registry().CoreToolIDs()
}

// WrapToolService wraps a *tool.Service as a ToolExecutor. The returned
// value delegates ExecBatch to the service and ListTools / CoreToolIDs to its
// registry, flattening the two-layer API into a single interface.
func WrapToolService(svc *tool.Service) ToolExecutor {
	return &toolExecutorAdapter{svc: svc}
}
