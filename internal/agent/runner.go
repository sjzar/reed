package agent

import (
	"context"
	"time"

	"github.com/sjzar/reed/internal/memory"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
)

// DefaultMaxIterations is the default maximum number of loop iterations.
const DefaultMaxIterations = 64

// Runner is the unified agent execution engine.
// It orchestrates: resolve → prepare context → agent loop → persist.
type Runner struct {
	ai      AIService
	session SessionProvider
	tools   ToolExecutor
	skills  SkillProvider
	memory  memory.Provider
	profile ProfileProvider
	media   MediaService // nil = no media support
}

// Option configures the Runner.
type Option func(*Runner)

// WithMedia sets the media service for upload/deflation and URI validation.
func WithMedia(m MediaService) Option {
	return func(r *Runner) { r.media = m }
}

// WithProfile sets the profile provider for resolving named profiles.
func WithProfile(p ProfileProvider) Option {
	return func(r *Runner) { r.profile = p }
}

// New creates a Runner with the given dependencies.
func New(
	ai AIService,
	sess SessionProvider,
	tools ToolExecutor,
	skills SkillProvider,
	mem memory.Provider,
	opts ...Option,
) *Runner {
	r := &Runner{
		ai:      ai,
		session: sess,
		tools:   tools,
		skills:  skills,
		memory:  mem,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Run executes a complete agent run: resolve → loop → persist.
func (r *Runner) Run(ctx context.Context, req *model.AgentRunRequest) (*model.AgentRunResponse, error) {
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	cfg, err := r.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cfg.Release()

	// Store the security Guard in context so all downstream tool calls
	// can query it via security.FromContext(ctx).
	if cfg.Guard != nil {
		ctx = security.WithChecker(ctx, cfg.Guard)
	}

	state := newRunState()
	defer r.afterRun(ctx, cfg, state)

	return r.loop(ctx, cfg, state)
}

// buildAssistantMsgFromResponse converts a Response into a Message with Content blocks.
func buildAssistantMsgFromResponse(resp *model.Response, usage *model.Usage) model.Message {
	var content []model.Content
	if resp.Thinking != nil && resp.Thinking.Content != "" {
		content = append(content, model.Content{
			Type:      model.ContentTypeThinking,
			Text:      resp.Thinking.Content,
			Signature: resp.Thinking.Signature,
		})
	}
	if resp.Content != "" {
		content = append(content, model.Content{
			Type: model.ContentTypeText,
			Text: resp.Content,
		})
	}
	return model.Message{
		Role:      model.RoleAssistant,
		Content:   content,
		ToolCalls: resp.ToolCalls,
		Usage:     usage,
	}
}

// buildRunResponse constructs the final AgentRunResponse.
func buildRunResponse(messages []model.Message, usage model.Usage, iterations int, reason model.AgentStopReason) *model.AgentRunResponse {
	resp := &model.AgentRunResponse{
		Messages:   messages,
		TotalUsage: usage,
		Iterations: iterations,
		StopReason: reason,
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == model.RoleAssistant {
			resp.Output = messages[i].TextContent()
			break
		}
	}
	return resp
}
