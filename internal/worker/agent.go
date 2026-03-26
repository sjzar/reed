package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/agent"
	"github.com/sjzar/reed/internal/engine"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/skill"
)

// workerSkillProvider is the local interface for skill lookups in the worker.
type workerSkillProvider interface {
	Get(id string) (*skill.ResolvedSkill, bool)
}

// AgentWorker is a thin adapter between engine.Worker and agent.Runner.
// It extracts data from the workflow + payload and delegates to agent.Runner.
type AgentWorker struct {
	workflow    *model.Workflow
	runner      *agent.Runner
	coreToolIDs []string
	skillSvc    workerSkillProvider
}

// Ensure AgentWorker implements engine.Worker.
var _ engine.Worker = (*AgentWorker)(nil)

// Execute runs an agent loop for the given step payload.
func (w *AgentWorker) Execute(ctx context.Context, p engine.StepPayload) engine.StepRunResult {
	result := newResult(p)

	// 1. Parse optional agent ID from payload
	agentID, _ := p.With["agent"].(string)
	var spec model.AgentSpec
	if agentID != "" {
		var ok bool
		spec, ok = w.workflow.Agents[agentID]
		if !ok {
			result.Status = model.StepFailed
			result.ErrorMessage = fmt.Sprintf("agent %q not found in workflow", agentID)
			return result
		}
	}

	// 2. Extract user prompt, session_key, and session_id
	userPrompt, _ := p.With["prompt"].(string)
	sessionKey, _ := p.With["session_key"].(string)
	sessionID, _ := p.With["session_id"].(string)

	// Mutual exclusion check
	if sessionKey != "" && sessionID != "" {
		result.Status = model.StepFailed
		result.ErrorMessage = "agent worker: both session_key and session_id provided; only one allowed"
		return result
	}

	namespace := w.resolveNamespace(p)

	// 3. Resolve tools
	tools, err := w.resolveToolIDs(spec, p.With)
	if err != nil {
		result.Status = model.StepFailed
		result.ErrorMessage = err.Error()
		return result
	}

	// 4. Build AgentRunRequest
	req := w.buildRunRequest(p, spec, agentID, namespace, tools, userPrompt, sessionKey, sessionID)

	// 5. Delegate to agent.Engine
	agentResp, err := w.runner.Run(ctx, req)
	if err != nil {
		result.Status = model.StepFailed
		result.ErrorMessage = err.Error()
		return result
	}

	// 6. Convert result
	result.Status = model.StepSucceeded
	result.Outputs["output"] = agentResp.Output
	result.Outputs["iterations"] = agentResp.Iterations
	result.Outputs["total_tokens"] = agentResp.TotalUsage.Total
	result.Outputs["stop_reason"] = string(agentResp.StopReason)
	return result
}

// resolveNamespace determines the session namespace from payload and workflow fields.
// Priority: with.namespace > App+Clean(WorkDir) > Clean(WorkDir) > App > Source > Name
func (w *AgentWorker) resolveNamespace(p engine.StepPayload) string {
	namespace, _ := p.With["namespace"].(string)
	if namespace == "" {
		app := w.workflow.App
		workDir := cleanWorkDir(p.WorkDir)
		switch {
		case app != "" && workDir != "":
			namespace = app + ":" + workDir
		case workDir != "":
			namespace = workDir
		case app != "":
			namespace = app
		}
	}
	if namespace == "" {
		namespace = w.workflow.Source
	}
	if namespace == "" {
		namespace = w.workflow.Name
	}
	return namespace
}

// resolveToolIDs determines the tool set for the agent request.
// If with.tools is set, it overrides everything. Otherwise, core tools
// are merged with skill tool dependencies.
func (w *AgentWorker) resolveToolIDs(spec model.AgentSpec, with map[string]any) ([]string, error) {
	if rawTools, ok := with["tools"]; ok {
		tools, valid := toStringSlice(rawTools)
		if !valid {
			return nil, fmt.Errorf("agent worker: 'tools' must be a list of strings")
		}
		return tools, nil
	}

	// Collect skill tool dependencies.
	// If a tool name looks Claude-sanitized (contains "__") and the raw name
	// is not found, also try the normalized form (e.g., "mcp__srv__tool" →
	// "mcp/srv/tool"). Only add the normalized form as a fallback, never both.
	seen := make(map[string]bool)
	if w.skillSvc != nil {
		for _, skillName := range spec.Skills {
			if rs, ok := w.skillSvc.Get(skillName); ok {
				for _, t := range rs.Meta.AllowedTools {
					if norm := normalizeClaudeToolName(t); norm != t {
						seen[norm] = true
					} else {
						seen[t] = true
					}
				}
			}
		}
	}
	if len(seen) > 0 {
		// core ∪ skill tools (additive, not replacing)
		for _, id := range w.coreToolIDs {
			if !seen[id] {
				seen[id] = true
			}
		}
		tools := make([]string, 0, len(seen))
		for t := range seen {
			tools = append(tools, t)
		}
		sort.Strings(tools)
		return tools, nil
	}
	// tools stays nil → engine uses core defaults
	return nil, nil
}

// buildRunRequest constructs the AgentRunRequest from payload, spec, and resolved values.
func (w *AgentWorker) buildRunRequest(p engine.StepPayload, spec model.AgentSpec, agentID, namespace string, tools []string, userPrompt, sessionKey, sessionID string) *model.AgentRunRequest {
	envCopy := make(map[string]string, len(p.Env))
	for k, v := range p.Env {
		envCopy[k] = v
	}
	req := &model.AgentRunRequest{
		Namespace:         namespace,
		AgentID:           agentID,
		SessionKey:        sessionKey,
		SessionID:         sessionID,
		Model:             spec.Model,
		SystemPrompt:      spec.SystemPrompt,
		Skills:            spec.Skills,
		Tools:             tools,
		Prompt:            userPrompt,
		RunRoot:           p.Env["REED_RUN_TEMP_DIR"],
		Cwd:               p.WorkDir,
		ToolAccessProfile: string(p.ToolAccess),
		Env:               envCopy,
	}
	// Prefer rendered values (expression-evaluated by engine dispatch)
	if p.RenderedAgentModel != "" {
		req.Model = p.RenderedAgentModel
	}
	if p.RenderedAgentSystemPrompt != "" {
		req.SystemPrompt = p.RenderedAgentSystemPrompt
	}

	// Extract optional model parameters from with.
	// Invalid types are logged as warnings to help diagnose workflow misconfigurations.
	if v, ok := toMapStringAny(p.With["schema"]); ok {
		req.Schema = v
	} else if p.With["schema"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "schema").Msg("agent with: expected map, ignoring")
	}
	if v, ok := toMapStringAny(p.With["extra_params"]); ok {
		req.ExtraParams = v
	} else if p.With["extra_params"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "extra_params").Msg("agent with: expected map, ignoring")
	}
	if v, ok := toInt(p.With["max_tokens"]); ok {
		req.MaxTokens = v
	} else if p.With["max_tokens"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "max_tokens").Msg("agent with: expected integer, ignoring")
	}
	if v, ok := toFloat64(p.With["temperature"]); ok {
		req.Temperature = &v
	} else if p.With["temperature"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "temperature").Msg("agent with: expected number, ignoring")
	}
	if v, ok := toFloat64(p.With["top_p"]); ok {
		req.TopP = &v
	} else if p.With["top_p"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "top_p").Msg("agent with: expected number, ignoring")
	}
	if v, ok := p.With["thinking_level"].(string); ok && v != "" {
		req.ThinkingLevel = v
	}
	if v, ok := toInt(p.With["max_iterations"]); ok {
		req.MaxIterations = v
	} else if p.With["max_iterations"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "max_iterations").Msg("agent with: expected integer, ignoring")
	}
	if v, ok := toInt(p.With["timeout_ms"]); ok {
		req.TimeoutMs = v
	} else if p.With["timeout_ms"] != nil {
		log.Warn().Str("step", p.StepID).Str("key", "timeout_ms").Msg("agent with: expected integer, ignoring")
	}
	if v, ok := p.With["profile"].(string); ok && v != "" {
		req.Profile = v
	}
	if v, ok := p.With["prompt_mode"].(string); ok && v != "" {
		req.PromptMode = v
	}

	// Apply AgentSpec defaults (only when request doesn't already specify)
	if spec.MaxIterations > 0 && req.MaxIterations == 0 {
		req.MaxIterations = spec.MaxIterations
	}
	if spec.Temperature != nil && req.Temperature == nil {
		req.Temperature = spec.Temperature
	}
	if spec.ThinkingLevel != "" && req.ThinkingLevel == "" {
		req.ThinkingLevel = spec.ThinkingLevel
	}

	// Pass through Bus and StepRunID for streaming agent events.
	req.Bus = p.Bus
	req.StepRunID = p.StepRunID

	// Collect media:// URIs from step inputs (with parameters) for agent media attachment.
	req.Media = media.CollectURIsFromMap(p.With)

	return req
}

// cleanWorkDir returns an absolute, cleaned version of dir.
func cleanWorkDir(dir string) string {
	if dir == "" {
		return ""
	}
	cleaned := filepath.Clean(dir)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}
	return abs
}

// toStringSlice converts an any value to []string if possible.
func toStringSlice(v any) ([]string, bool) {
	switch val := v.(type) {
	case []string:
		return val, true
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, s)
		}
		return result, true
	default:
		return nil, false
	}
}

// normalizeClaudeToolName converts a Claude-sanitized tool name back to the
// original format. Claude's API sanitizes tool names by replacing "/" with "__"
// and "." with "_". This function only reverses the "__" → "/" transformation;
// the "." → "_" transformation is lossy and cannot be reversed. This means
// tool names containing literal dots will not round-trip correctly.
// A more complete solution would handle this at the skill loading stage in
// internal/skill/, normalizing allowed_tools at parse time.
func normalizeClaudeToolName(name string) string {
	if !strings.Contains(name, "__") {
		return name
	}
	return strings.ReplaceAll(name, "__", "/")
}

// toMapStringAny extracts a map[string]any from an any value.
func toMapStringAny(v any) (map[string]any, bool) {
	if v == nil {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// toInt extracts an int from an any value, handling common numeric types
// that YAML/JSON parsers produce. For float64, only whole numbers are accepted;
// fractional values like 1.9 return false to avoid silent truncation.
func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		if val != float64(int(val)) {
			return 0, false // reject fractional values
		}
		return int(val), true
	default:
		return 0, false
	}
}

// toFloat64 extracts a float64 from an any value.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}
