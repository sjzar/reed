package agent

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"slices"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/memory"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/security"
	"github.com/sjzar/reed/internal/skill"
	"github.com/sjzar/reed/internal/tool"
	"github.com/sjzar/reed/pkg/mimetype"
)

// resolvedConfig holds every input the agent loop needs, fully resolved and
// ready to use. The loop must never read from the original AgentRunRequest
// after resolve() returns.
type resolvedConfig struct {
	// Session
	SessionID string
	Release   func()

	// Memory
	MemoryRC memory.RunContext

	// Model parameters (req > profile > default cascade)
	Model, SystemPrompt, ThinkingLevel string
	MaxTokens, MaxIterations           int
	Temperature, TopP                  *float64
	Schema, ExtraParams                map[string]any

	// Capabilities
	ToolDefs []model.ToolDef
	ToolCtx  tool.RuntimeContext

	// Prompt (assembled)
	Prompt     *Prompt
	PromptMode PromptMode

	// User input (extracted from req so loop doesn't depend on raw request)
	UserPrompt  string
	UserContent []model.Content // extra content blocks (images from req.Media)

	// Pass-through
	AgentID           string
	Env               map[string]string
	WaitForAsyncTasks bool

	// Model metadata
	ContextWindow int

	// Security
	Guard *security.Guard

	// Bus event streaming
	Bus       *bus.Bus
	StepRunID string
}

// resolve orchestrates all sub-resolve steps and returns a fully populated
// resolvedConfig. The caller is responsible for invoking Release when the run
// ends (even on error paths after a successful session acquire).
func (r *Runner) resolve(ctx context.Context, req *model.AgentRunRequest) (*resolvedConfig, error) {
	// Acquire session lock — must be released by the caller.
	sessionID, release, err := r.acquireSession(ctx, req)
	if err != nil {
		return nil, err
	}

	// Profile resolution.
	profile, err := r.resolveProfile(ctx, req)
	if err != nil {
		release()
		return nil, err
	}

	// Parameter cascade: req > profile > default.
	resolvedSystemPrompt := req.SystemPrompt
	if resolvedSystemPrompt == "" && profile != nil {
		resolvedSystemPrompt = profile.SystemPrompt
	}
	if resolvedSystemPrompt == "" {
		resolvedSystemPrompt = DefaultSystemPrompt
	}

	resolvedModel := req.Model
	if resolvedModel == "" && profile != nil && profile.Model != "" {
		resolvedModel = profile.Model
	}

	resolvedMaxIter := req.MaxIterations
	if resolvedMaxIter <= 0 && profile != nil && profile.MaxIterations > 0 {
		resolvedMaxIter = profile.MaxIterations
	}

	resolvedTemperature := req.Temperature
	if resolvedTemperature == nil && profile != nil && profile.Temperature != nil {
		resolvedTemperature = profile.Temperature
	}

	resolvedThinking := req.ThinkingLevel
	if resolvedThinking == "" && profile != nil && profile.ThinkingLevel != "" {
		resolvedThinking = profile.ThinkingLevel
	}

	// Tool definitions.
	toolDefs, reqSkills, err := r.resolveToolDefs(req.Tools, req.Skills, profile)
	if err != nil {
		release()
		return nil, err
	}

	// Subagents (depth > 0) must not have spawn_subagent regardless of how
	// tools were resolved (explicit list, profile, or core defaults).
	if req.Depth > 0 {
		toolDefs = slices.DeleteFunc(toolDefs, func(td model.ToolDef) bool {
			return td.Name == "spawn_subagent"
		})
	}

	// Skills.
	skillInfos := r.resolveSkillInfos(req.RunRoot, reqSkills)

	// Memory injection.
	memoryRC := memory.RunContext{
		Namespace:  req.Namespace,
		AgentID:    req.AgentID,
		SessionKey: req.SessionKey,
	}
	memoryContent := r.resolveMemory(ctx, memoryRC)

	// Model metadata (context window for proactive compaction and prompt budget).
	var contextWindow int
	if resolvedModel != "" {
		meta := r.ai.ModelMetadataFor(resolvedModel)
		contextWindow = meta.ContextWindow
	}

	// Prompt.
	mode := ParsePromptMode(req.PromptMode)
	prompt := buildPrompt(promptOpts{
		SystemPrompt:  resolvedSystemPrompt,
		ToolDefs:      toolDefs,
		SkillInfos:    skillInfos,
		MemoryContent: memoryContent,
		AgentID:       req.AgentID,
		Model:         resolvedModel,
		Cwd:           req.Cwd,
		Timezone:      time.Now().Location().String(),
		ContextWindow: contextWindow,
	})

	// RuntimeContext.
	toolCtx := newRuntimeContext(req)

	// Security Guard — centralized access control.
	// Resolve access profile: use request value, default to workdir.
	accessProfile := security.AccessProfile(req.ToolAccessProfile)
	if accessProfile == "" {
		accessProfile = security.ProfileWorkdir
	}

	var guard *security.Guard
	if parent, ok := security.FromContext(ctx).(security.Forkable); ok {
		// SubAgent: inherit parent grants via Fork, then supplement.
		guard = parent.Fork()
		// Grant SubAgent's own Cwd only if the parent already allows it.
		// This prevents a child from escaping the parent's security boundary.
		parentChecker := security.FromContext(ctx)
		if toolCtx.Cwd != "" {
			if parentChecker.CheckWrite(toolCtx.Cwd) == nil {
				_ = guard.GrantRead(toolCtx.Cwd, "cwd")
				_ = guard.GrantWrite(toolCtx.Cwd, "cwd")
			} else {
				log.Warn().Str("cwd", toolCtx.Cwd).Msg("subagent cwd outside parent boundary, skipping grant")
			}
		}
	} else {
		// Top-level agent: create fresh Guard.
		guard = security.New(accessProfile, toolCtx.Cwd)
	}

	// Grant RunRoot read+write access (temp dir for skill mount, artifacts).
	// For subagents, validate against parent boundary first.
	if toolCtx.RunRoot != "" {
		canonicalRunRoot, _ := security.Canonicalize(toolCtx.RunRoot)
		if canonicalRunRoot == "" {
			canonicalRunRoot = toolCtx.RunRoot
		}
		parentChecker := security.FromContext(ctx)
		if parentChecker == nil || parentChecker.CheckWrite(canonicalRunRoot) == nil {
			_ = guard.GrantRead(toolCtx.RunRoot, "run-root")
			_ = guard.GrantWrite(toolCtx.RunRoot, "run-root")
		} else {
			log.Warn().Str("runRoot", toolCtx.RunRoot).Msg("subagent RunRoot outside parent boundary, skipping grant")
		}
	}

	// Grant read access to skill directories (both mount and backing).
	for _, si := range skillInfos {
		if si.MountDir != "" {
			if err := guard.GrantRead(si.MountDir, "skill:"+si.ID); err != nil {
				log.Warn().Err(err).Str("skill", si.ID).Msg("failed to grant skill mount dir")
			}
		}
		if si.BackingDir != "" && si.BackingDir != si.MountDir {
			if err := guard.GrantRead(si.BackingDir, "skill-backing:"+si.ID); err != nil {
				log.Warn().Err(err).Str("skill", si.ID).Msg("failed to grant skill backing dir")
			}
		}
	}

	// Process media files from request — supports local paths, media:// URIs, and data: URIs
	var userContent []model.Content
	if len(req.Media) > 0 && r.media != nil {
		for _, m := range req.Media {
			c, err := resolveMediaInput(ctx, r.media, m)
			if err != nil {
				release()
				return nil, err
			}
			userContent = append(userContent, c)
		}
	}

	cfg := &resolvedConfig{
		SessionID:         sessionID,
		Release:           release,
		MemoryRC:          memoryRC,
		Model:             resolvedModel,
		SystemPrompt:      resolvedSystemPrompt,
		ThinkingLevel:     resolvedThinking,
		MaxTokens:         req.MaxTokens,
		MaxIterations:     resolvedMaxIter,
		Temperature:       resolvedTemperature,
		TopP:              req.TopP,
		Schema:            req.Schema,
		ExtraParams:       req.ExtraParams,
		ToolDefs:          toolDefs,
		ToolCtx:           toolCtx,
		Prompt:            prompt,
		PromptMode:        mode,
		UserPrompt:        req.Prompt,
		UserContent:       userContent,
		AgentID:           req.AgentID,
		Env:               req.Env,
		WaitForAsyncTasks: req.WaitForAsyncTasks,
		ContextWindow:     contextWindow,
		StepRunID:         req.StepRunID,
		Guard:             guard,
	}

	// Type-assert Bus: bus is optional streaming capability, not required.
	if req.Bus != nil {
		if b, ok := req.Bus.(*bus.Bus); ok {
			cfg.Bus = b
		} else {
			log.Warn().Str("type", fmt.Sprintf("%T", req.Bus)).Msg("AgentRunRequest.Bus has unexpected type, step output streaming disabled")
		}
	}

	return cfg, nil
}

// acquireSession performs dual-mode session resolution: SessionKey vs SessionID
// are mutually exclusive. The caller must invoke release when the run ends.
func (r *Runner) acquireSession(ctx context.Context, req *model.AgentRunRequest) (sessionID string, release func(), err error) {
	switch {
	case req.SessionKey != "" && req.SessionID != "":
		return "", nil, fmt.Errorf("only one of SessionKey and SessionID may be set")
	case req.SessionID != "":
		rel, err := r.session.AcquireByID(ctx, req.SessionID)
		if err != nil {
			return "", nil, err
		}
		return req.SessionID, rel, nil
	default:
		sid, rel, err := r.session.Acquire(ctx, req.Namespace, req.AgentID, req.SessionKey)
		if err != nil {
			return "", nil, err
		}
		return sid, rel, nil
	}
}

// resolveProfile resolves a named profile when req.Profile is non-empty.
// Returns nil when no profile is requested.
func (r *Runner) resolveProfile(ctx context.Context, req *model.AgentRunRequest) (*ResolvedProfile, error) {
	if req.Profile == "" {
		return nil, nil
	}
	if r.profile == nil {
		return nil, fmt.Errorf("profile %q requested but no profile provider configured", req.Profile)
	}
	p, err := r.profile.ResolveProfile(ctx, req.Profile)
	if err != nil {
		return nil, fmt.Errorf("resolve profile %q: %w", req.Profile, err)
	}
	return p, nil
}

// resolveToolDefs applies the nil=core / empty=none / [ids]=those contract,
// falling back to profile defaults when the request does not specify tools or
// skills explicitly.
//
// It returns the resolved tool definitions together with the effective skill ID
// list so the caller can use it for skill mounting without re-running profile
// logic.
func (r *Runner) resolveToolDefs(reqTools []string, reqSkills []string, profile *ResolvedProfile) ([]model.ToolDef, []string, error) {
	// Apply profile overrides where request leaves fields unset.
	if profile != nil {
		if reqTools == nil {
			reqTools = profile.ToolIDs
		}
		if len(reqSkills) == 0 {
			reqSkills = profile.SkillIDs
		}
	}

	// Collect tool defs:
	//   nil    → default core tool set
	//   empty  → no tools (explicit deny)
	//   [ids]  → exactly those tools (lenient: unknown IDs from skills are skipped)
	var toolDefs []model.ToolDef
	switch {
	case reqTools == nil:
		defs, err := r.tools.ListTools(r.tools.CoreToolIDs())
		if err != nil {
			return nil, nil, fmt.Errorf("resolve core tools: %w", err)
		}
		toolDefs = defs
	case len(reqTools) > 0:
		// Use lenient resolution: skill-derived tool IDs may reference
		// provider-specific names (e.g., Claude-sanitized) not in the registry.
		toolDefs = r.tools.ListToolsLenient(reqTools)
		// else: empty non-nil slice → no tools; toolDefs stays nil
	}

	return toolDefs, reqSkills, nil
}

// resolveSkillInfos loads and mounts skills for the run. Failures are logged
// as warnings and the run continues without the skills — skills are advisory,
// not required for execution.
func (r *Runner) resolveSkillInfos(runRoot string, reqSkills []string) []skill.SkillInfo {
	if r.skills == nil || len(reqSkills) == 0 {
		return nil
	}
	if runRoot == "" {
		log.Warn().Strs("skills", reqSkills).Msg("skills requested but RunRoot is empty, skipping skill injection")
		return nil
	}
	infos, err := r.skills.ListAndMount(runRoot, reqSkills)
	if err != nil {
		log.Warn().Err(err).Strs("skills", reqSkills).Msg("skill mount failed, continuing without skills")
		return nil
	}
	return infos
}

// resolveMemory fetches memory for the session. Failures are logged as
// warnings and the run continues without memory — memory is advisory.
func (r *Runner) resolveMemory(ctx context.Context, rc memory.RunContext) string {
	if r.memory == nil {
		return ""
	}
	result, err := r.memory.BeforeRun(ctx, rc)
	if err != nil {
		log.Warn().Err(err).Str("namespace", rc.Namespace).Str("agentID", rc.AgentID).Msg("memory injection failed, continuing without memory")
		return ""
	}
	return result.Content
}

// newRuntimeContext builds the tool.RuntimeContext from the request, normalizing
// paths. This is a pure function — it reads from req and the OS environment,
// but mutates nothing. Access control is handled by security.Guard, not here.
func newRuntimeContext(req *model.AgentRunRequest) tool.RuntimeContext {
	toolCtx := tool.RuntimeContext{
		Set:        true,
		Cwd:        req.Cwd,
		RunRoot:    req.RunRoot,
		OS:         runtime.GOOS,
		AgentDepth: req.Depth,
	}

	// Normalize Cwd early so all downstream path resolution uses a canonical
	// absolute path.
	if toolCtx.Cwd != "" {
		if normalized, err := security.Canonicalize(toolCtx.Cwd); err == nil {
			toolCtx.Cwd = normalized
		}
	}

	// Cwd fallback — applies to ALL profiles so tools always have a working
	// directory. Preference order: req.Cwd (already set above) → req.RunRoot →
	// os.Getwd().
	if toolCtx.Cwd == "" {
		cwd := req.RunRoot
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		if cwd != "" {
			if normalized, err := security.Canonicalize(cwd); err == nil {
				cwd = normalized
			}
			toolCtx.Cwd = cwd
		}
	}

	return toolCtx
}

// resolveMediaInput resolves a single media reference into a Content block.
// Supports: local file paths, media:// URIs, and data: URIs.
func resolveMediaInput(ctx context.Context, svc MediaService, ref string) (model.Content, error) {
	switch {
	case media.IsMediaURI(ref):
		id, _ := media.ParseURI(ref)
		entry, err := svc.Get(ctx, id)
		if err != nil {
			return model.Content{}, fmt.Errorf("validate media ref %q: %w", ref, err)
		}
		return mediaContentForMIME(ref, entry.MIMEType, ""), nil

	case media.IsDataURI(ref):
		mimeType, _, err := media.ParseDataURI(ref)
		if err != nil {
			return model.Content{}, fmt.Errorf("parse data URI: %w", err)
		}
		return mediaContentForMIME(ref, mimeType, ""), nil

	default:
		// Reject raw local file paths inside the agent engine.
		// Local path → media:// conversion must happen at the CLI/HTTP boundary
		// (processMediaInputs / parseMultipartInputs), not here, to prevent
		// filesystem boundary bypass via AgentRunRequest.Media.
		return model.Content{}, fmt.Errorf("unsupported media reference %q: expected media:// or data: URI", ref)
	}
}

// mediaContentForMIME creates the appropriate Content block (image or document) based on MIME type.
func mediaContentForMIME(uri, mimeType, filename string) model.Content {
	if mimetype.IsImage(mimeType) {
		return model.ImageContent(uri, mimeType)
	}
	return model.DocumentContent(uri, mimeType, filename)
}
