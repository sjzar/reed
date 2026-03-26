package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sjzar/reed/internal/bus"
	"github.com/sjzar/reed/internal/media"
	"github.com/sjzar/reed/internal/model"
	"github.com/sjzar/reed/internal/session"
	"github.com/sjzar/reed/internal/tool"
)

// loop is the core agent iteration loop. All inputs come from cfg and state;
// it never reads from the raw AgentRunRequest.
func (r *Runner) loop(ctx context.Context, cfg *resolvedConfig, state *runState) (*model.AgentRunResponse, error) {
	// Load session context (post-last-compaction view)
	history, err := r.session.LoadContext(ctx, cfg.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	// Build initial messages — always rebuild system prompt fresh (never persist it)
	promptText := cfg.Prompt.BuildForMode(cfg.PromptMode)
	if promptText != "" {
		sysMsg := model.NewTextMessage(model.RoleSystem, promptText)
		state.appendMessages(sysMsg)
		// Persist system prompt for audit trail. LoadContext filters out system
		// messages when rebuilding context, so this won't duplicate on reload.
		if err := r.session.AppendMessages(ctx, cfg.SessionID, []model.Message{sysMsg}); err != nil {
			log.Warn().Err(err).Str("sessionID", cfg.SessionID).Msg("failed to persist system prompt")
		}
	}
	state.appendMessages(history...)

	// Build user message — supports text + image content blocks
	var userContent []model.Content
	userContent = append(userContent, model.Content{Type: model.ContentTypeText, Text: cfg.UserPrompt})
	userContent = append(userContent, cfg.UserContent...)
	userMsg := model.Message{Role: model.RoleUser, Content: userContent}
	state.appendMessages(userMsg)

	// Persist user message
	if err := r.session.AppendMessages(ctx, cfg.SessionID, []model.Message{userMsg}); err != nil {
		log.Warn().Err(err).Str("sessionID", cfg.SessionID).Msg("failed to persist user message")
	}

	// Resolve max iterations
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}

	// Resolve bus for event streaming
	var eventBus *bus.Bus
	var outputTopic string
	if cfg.Bus != nil && cfg.StepRunID != "" {
		eventBus = cfg.Bus
		outputTopic = bus.StepOutputTopic(cfg.StepRunID)
	}

	stopBusSub := startBusSubscriber(ctx, eventBus, cfg.StepRunID, cfg.SessionID, r.session)
	defer stopBusSub()

	for state.iteration < maxIter {
		if ctx.Err() != nil {
			return state.buildResponse(model.AgentStopCanceled), nil
		}

		// Micro-compact old tool results only under context pressure.
		// When context usage is low, preserving full tool output gives the model
		// better information; truncation is a net loss when there's room to spare.
		if state.iteration > 0 && state.lastToolBoundary > 0 && cfg.ContextWindow > 0 {
			est := estimateRequestTokens(state.messages)
			if est > int(float64(cfg.ContextWindow)*0.7) {
				compactOldToolResults(state.messages, state.lastToolBoundary, defaultToolCompactMaxChars)
			}
		}

		// Proactive compaction: if estimated tokens exceed 90% of context window,
		// compact before wasting an LLM call that would fail with ErrContextOverflow.
		// Cooldown: skip if compaction ran recently to avoid compacting every iteration
		// on small context windows. The reactive ErrContextOverflow path has no cooldown.
		// lastCompactIter < 0 means "never compacted" — always eligible.
		if cfg.ContextWindow > 0 && (state.lastCompactIter < 0 || state.iteration-state.lastCompactIter >= compactCooldownIters) {
			est := estimateRequestTokens(state.messages)
			if est > int(float64(cfg.ContextWindow)*0.9) {
				if !r.compactAndRebuild(ctx, cfg, state) {
					log.Warn().Int("estimated", est).Int("contextWindow", cfg.ContextWindow).
						Msg("proactive compaction could not reduce context, proceeding anyway")
				}
			}
		}

		resp, stopResult, err := r.callLLM(ctx, cfg, state, eventBus, outputTopic)
		if err != nil {
			return nil, err
		}
		if stopResult != nil {
			return stopResult, nil
		}

		// Build assistant message and persist
		usage := resp.Usage
		currentIter := state.iteration
		assistantMsg := buildAssistantMsgFromResponse(resp, &usage)
		assistantMsg.Meta = map[string]any{
			"iteration":       currentIter,
			"model":           cfg.Model,
			"estimatedTokens": estimateRequestTokens(state.messages),
		}
		state.appendMessages(assistantMsg)
		if err := r.session.AppendMessages(ctx, cfg.SessionID, []model.Message{assistantMsg}); err != nil {
			log.Warn().Err(err).Str("sessionID", cfg.SessionID).Msg("failed to persist assistant message")
		}
		state.iteration++

		// Token budget check
		if cfg.MaxTokens > 0 && state.totalUsage.Total >= cfg.MaxTokens {
			return state.buildResponse(model.AgentStopMaxTokens), nil
		}

		// No tool calls → drain inbox or complete
		if len(resp.ToolCalls) == 0 {
			stop, result, err := r.drainInboxOrComplete(ctx, cfg, state)
			if err != nil {
				return nil, err
			}
			if stop {
				return result, nil
			}
			continue
		}

		// Execute tools
		stop, result, err := r.executeTools(ctx, cfg, state, resp, currentIter)
		if err != nil {
			return nil, err
		}
		if stop {
			return result, nil
		}

		// Drain inbox after tool execution so steer messages enter
		// the conversation before the next LLM call.
		if _, fetchErr := r.drainInbox(ctx, cfg, state); fetchErr != nil {
			return nil, fmt.Errorf("fetch inbox after tools: %w", fetchErr)
		}

		// Mark boundary after inbox drain so async tool results from the inbox
		// are also eligible for micro-compaction in subsequent iterations.
		state.lastToolBoundary = len(state.messages)
	}

	return state.buildResponse(model.AgentStopMaxIter), nil
}

// maxLLMRetries caps the number of consecutive retries (rate limit + overflow)
// within a single callLLM invocation to prevent unbounded spinning.
const maxLLMRetries = 8

// compactCooldownIters is the minimum number of iterations between proactive
// compaction attempts. Prevents compacting every iteration on small context windows.
// The reactive ErrContextOverflow path bypasses this cooldown.
const compactCooldownIters = 3

// tokensToCharsRatio is the inverse of the pkg/token Latin weight (0.25).
// Used to convert a remaining-token budget into a character budget.
const tokensToCharsRatio = 4

// callLLM calls the AI provider, handling rate limits and context overflow
// internally. Neither consumes an iteration count. Retries are capped at
// maxLLMRetries to prevent infinite loops on persistent errors.
// Returns (response, stopResult, error):
//   - response!=nil: successful LLM call, continue processing
//   - stopResult!=nil: graceful stop (e.g., compaction failed), return to caller
//   - error!=nil: fatal error
func (r *Runner) callLLM(ctx context.Context, cfg *resolvedConfig, state *runState, eventBus *bus.Bus, outputTopic string) (*model.Response, *model.AgentRunResponse, error) {
	for retries := 0; ; retries++ {
		if retries >= maxLLMRetries {
			return nil, state.buildResponse(model.AgentStopRetryExhausted), nil
		}
		if eventBus != nil {
			eventBus.Publish(outputTopic, bus.Message{
				Type:    "status",
				Payload: bus.StatusPayload{Message: fmt.Sprintf("llm_start iteration=%d model=%s", state.iteration, cfg.Model)},
			})
		}

		// Build request messages: state.messages + optional ephemeral progress hint.
		// The progress hint is NOT appended to state.messages — it's transient and
		// only visible to this LLM call. This prevents it from polluting compaction,
		// token estimates, retry behavior, and the final API response.
		requestMessages := state.messages
		maxIter := cfg.MaxIterations
		if maxIter <= 0 {
			maxIter = DefaultMaxIterations
		}
		if hint := buildProgressHint(state.iteration, maxIter, estimateRequestTokens(state.messages), cfg.ContextWindow); hint != "" {
			requestMessages = make([]model.Message, len(state.messages)+1)
			copy(requestMessages, state.messages)
			requestMessages[len(state.messages)] = model.NewTextMessage(model.RoleSystem, hint)
		}

		stream, err := r.ai.Responses(ctx, &model.Request{
			Model: cfg.Model, AgentID: cfg.AgentID,
			Messages: requestMessages, Tools: cfg.ToolDefs,
			MaxTokens: cfg.MaxTokens, Temperature: cfg.Temperature, TopP: cfg.TopP,
			Schema: cfg.Schema, Thinking: cfg.ThinkingLevel,
			ExtraParams:     cfg.ExtraParams,
			EstimatedTokens: estimateRequestTokens(requestMessages),
		})
		if err != nil {
			retry, stopResult, fatalErr := r.handleLLMError(ctx, cfg, state, err)
			if fatalErr != nil {
				return nil, nil, fatalErr
			}
			if stopResult != nil {
				return nil, stopResult, nil
			}
			if retry {
				continue
			}
			return nil, nil, fmt.Errorf("llm chat: %w", err)
		}

		resp, drainErr := drainStreamWithEvents(ctx, stream, eventBus, outputTopic)
		if drainErr != nil {
			return nil, nil, fmt.Errorf("drain response stream: %w", drainErr)
		}
		state.addUsage(resp.Usage)

		if eventBus != nil {
			eventBus.Publish(outputTopic, bus.Message{
				Type:    "status",
				Payload: bus.StatusPayload{Message: fmt.Sprintf("llm_end stop=%s tools=%d", resp.StopReason, len(resp.ToolCalls))},
			})
		}

		return resp, nil, nil
	}
}

// handleLLMError classifies an LLM error and returns (retry, stopResult, fatalErr).
// retry=true means the caller should retry the LLM call.
// stopResult!=nil means the caller should return this response (graceful stop).
// fatalErr!=nil means the caller should return the error.
func (r *Runner) handleLLMError(ctx context.Context, cfg *resolvedConfig, state *runState, err error) (retry bool, stopResult *model.AgentRunResponse, fatalErr error) {
	var aiErr *model.AIError
	if !errors.As(err, &aiErr) {
		return false, nil, fmt.Errorf("llm chat: %w", err)
	}

	switch aiErr.Kind {
	case model.ErrContextOverflow:
		if !r.compactAndRebuild(ctx, cfg, state) {
			return false, state.buildResponse(model.AgentStopMaxTokens), nil
		}
		return true, nil, nil

	case model.ErrRateLimit:
		if aiErr.RetryAfter > 0 {
			select {
			case <-time.After(aiErr.RetryAfter):
			case <-ctx.Done():
				return false, state.buildResponse(model.AgentStopCanceled), nil
			}
		}
		return true, nil, nil
	}

	return false, nil, fmt.Errorf("llm chat: %w", err)
}

// executeTools runs tool calls and checks for repetition.
// Returns (stop, result, err) — stop=true means the loop should return result.
// iteration is the 0-based iteration that triggered these tool calls.
func (r *Runner) executeTools(ctx context.Context, cfg *resolvedConfig, state *runState, resp *model.Response, iteration int) (bool, *model.AgentRunResponse, error) {
	var calls []tool.CallRequest
	for _, tc := range resp.ToolCalls {
		rawArgs, _ := tool.MarshalArgs(tc)
		calls = append(calls, tool.CallRequest{
			ToolCallID: tc.ID,
			Name:       tc.Name,
			RawArgs:    rawArgs,
			Env:        cfg.Env,
		})
	}
	batchResp := r.tools.ExecBatch(ctx, tool.BatchRequest{
		SessionID: cfg.SessionID,
		Calls:     calls,
		Context:   cfg.ToolCtx,
		Env:       cfg.Env,
	})
	toolMsgs := callResultsToMessages(batchResp.Results, iteration)

	// Deflate data URIs → media:// references before persisting to JSONL
	r.deflateMessages(ctx, toolMsgs)

	// Collect round records for repetition detection
	var roundCalls []roundCall
	for idx, msg := range toolMsgs {
		state.appendMessages(msg)
		tc := resp.ToolCalls[idx]
		roundCalls = append(roundCalls, roundCall{
			Name:   tc.Name,
			Input:  canonicalJSON(tc.Arguments),
			Output: hashPrefix(msg.ContentSummary(), 16),
		})
	}
	if err := r.session.AppendMessages(ctx, cfg.SessionID, toolMsgs); err != nil {
		log.Warn().Err(err).Str("sessionID", cfg.SessionID).Msg("failed to persist tool messages")
	}

	// Repetition detection
	state.repDetector.recordRound(roundCalls)
	if state.repDetector.detected() {
		return true, state.buildResponse(model.AgentStopLoop), nil
	}

	return false, nil, nil
}

// drainInbox fetches and processes inbox events into the agent state.
// Returns the number of messages drained and any fetch error.
// Phase-specific error wrapping remains at call sites.
func (r *Runner) drainInbox(ctx context.Context, cfg *resolvedConfig, state *runState) (int, error) {
	events, err := r.session.FetchAndClearInbox(ctx, cfg.SessionID)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	inboxMsgs := inboxEventsToMessages(events)
	// Stamp trace metadata on inbox messages. DurationMs is not available
	// here (tracked by the async job runner), but iteration is useful for tracing.
	for i := range inboxMsgs {
		if inboxMsgs[i].Meta == nil {
			inboxMsgs[i].Meta = make(map[string]any, 2)
		}
		inboxMsgs[i].Meta["iteration"] = state.iteration
		inboxMsgs[i].Meta["source"] = "inbox"
	}
	r.deflateMessages(ctx, inboxMsgs)
	state.appendMessages(inboxMsgs...)
	if err := r.session.AppendMessages(ctx, cfg.SessionID, inboxMsgs); err != nil {
		log.Warn().Err(err).Str("sessionID", cfg.SessionID).Msg("failed to persist inbox messages")
	}
	return len(events), nil
}

// drainInboxOrComplete handles the no-tool-calls case: drain inbox, wait for
// async jobs, or complete.
// Returns (stop, result, err) — stop=true means the loop should return result.
func (r *Runner) drainInboxOrComplete(ctx context.Context, cfg *resolvedConfig, state *runState) (bool, *model.AgentRunResponse, error) {
	// Always drain inbox — catches results from jobs that completed
	// between iterations (inbox written before pending cleared).
	n, fetchErr := r.drainInbox(ctx, cfg, state)
	if fetchErr != nil {
		return true, nil, fmt.Errorf("fetch inbox: %w", fetchErr)
	}
	if n > 0 {
		return false, nil, nil // continue loop
	}

	if r.session.HasPendingJobs(cfg.SessionID) {
		if cfg.WaitForAsyncTasks {
			if err := r.session.WaitPendingJobs(ctx, cfg.SessionID); err != nil {
				return true, state.buildResponse(model.AgentStopCanceled), nil
			}
			n, fetchErr := r.drainInbox(ctx, cfg, state)
			if fetchErr != nil {
				return true, nil, fmt.Errorf("fetch inbox after wait: %w", fetchErr)
			}
			if n > 0 {
				return false, nil, nil // continue loop
			}
		} else {
			return true, state.buildResponse(model.AgentStopAsync), nil
		}
	}

	return true, state.buildResponse(model.AgentStopComplete), nil
}

// afterRunTimeout is the maximum time afterRun will spend on cleanup.
const afterRunTimeout = 60 * time.Second

// afterRunShutdownTimeout is the cleanup timeout when the parent context is
// already canceled (i.e., during shutdown). Must be shorter than the 3s grace
// period defined in lifecycle.go.
const afterRunShutdownTimeout = 2 * time.Second

// afterRun persists memory after the run completes. It uses
// context.WithoutCancel to ensure best-effort persistence even when the
// parent context is canceled, with a timeout to prevent indefinite blocking.
// During shutdown (ctx already canceled), uses a shorter timeout to avoid
// blocking beyond the grace period.
func (r *Runner) afterRun(ctx context.Context, cfg *resolvedConfig, state *runState) {
	if r.memory != nil {
		timeout := afterRunTimeout
		if ctx.Err() != nil {
			timeout = afterRunShutdownTimeout
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if err := r.memory.AfterRun(cleanupCtx, cfg.MemoryRC, state.messages); err != nil {
			log.Warn().Err(err).Str("namespace", cfg.MemoryRC.Namespace).Msg("memory extraction failed")
		}
	}
}

// drainStreamWithEvents consumes a ResponseStream, publishing text deltas
// to the bus so subscribers (e.g. terminal) can display streaming output.
func drainStreamWithEvents(ctx context.Context, stream model.ResponseStream, eventBus *bus.Bus, topic string) (*model.Response, error) {
	defer stream.Close()
	for {
		ev, err := stream.Next(ctx)
		if err != nil {
			if err == io.EOF {
				return stream.Response(), nil
			}
			return nil, err
		}
		if ev.Type == model.StreamEventTextDelta && ev.Delta != "" && eventBus != nil && topic != "" {
			eventBus.Publish(topic, bus.Message{
				Type:    "text",
				Payload: bus.TextPayload{Delta: ev.Delta},
			})
		}
	}
}

// callResultsToMessages converts tool.CallResult slice to model.Message slice.
// iteration is written into each message's Meta for trace purposes.
func callResultsToMessages(results []tool.CallResult, iteration int) []model.Message {
	msgs := make([]model.Message, len(results))
	for i, cr := range results {
		msgs[i] = model.Message{
			Role:       model.RoleTool,
			Content:    cr.Content,
			ToolCallID: cr.ToolCallID,
			ToolName:   cr.ToolName,
			IsError:    cr.IsError,
			Meta: map[string]any{
				"iteration":  iteration,
				"durationMs": cr.DurationMs,
			},
		}
	}
	return msgs
}

// deflateMessages deflates data URIs in a slice of messages using the media service.
func (r *Runner) deflateMessages(ctx context.Context, msgs []model.Message) {
	if r.media == nil {
		return
	}
	for i := range msgs {
		deflateMessage(ctx, r.media, &msgs[i])
	}
}

// deflateMessage replaces inline data URIs in image/document content with media:// references.
// Failures are logged and the original data URI is preserved (graceful degradation).
func deflateMessage(ctx context.Context, uploader MediaService, msg *model.Message) {
	for i := range msg.Content {
		c := &msg.Content[i]
		if (c.Type != model.ContentTypeImage && c.Type != model.ContentTypeDocument) || !media.IsDataURI(c.MediaURI) {
			continue
		}
		mime, data, err := media.ParseDataURI(c.MediaURI)
		if err != nil || len(data) == 0 {
			continue
		}
		entry, err := uploader.Upload(ctx, bytes.NewReader(data), mime)
		if err != nil {
			log.Warn().Err(err).Msg("media deflation failed, keeping inline data")
			continue
		}
		c.MediaURI = media.URI(entry.ID)
	}
}

// compactAndRebuild runs session compaction and rebuilds state.messages with the
// compacted history plus a fresh system prompt. Returns true if compaction achieved
// a meaningful size reduction, false if compaction failed or produced no shrink.
// Used by both proactive threshold check and reactive ErrContextOverflow handler.
func (r *Runner) compactAndRebuild(ctx context.Context, cfg *resolvedConfig, state *runState) bool {
	beforeCount := len(state.messages)
	compacted, err := r.session.Compact(ctx, cfg.SessionID, session.CompactOptions{
		KeepRecentN: 4,
	})
	if err != nil {
		log.Warn().Err(err).Msg("compaction failed")
		return false
	}

	// No-shrink guard: if compaction didn't reduce messages, don't replace.
	// This prevents spinning when there's nothing to compress (e.g., first turn
	// with a huge user prompt).
	if len(compacted) >= beforeCount {
		return false
	}

	state.messages = compacted
	// After compaction, use budget-aware prompt building if context window is known.
	// This dynamically trims lower-priority prompt sections (skills, memory) when
	// context is under pressure, while respecting prompt mode filtering.
	var promptText string
	if cfg.ContextWindow > 0 {
		remainingTokens := cfg.ContextWindow - estimateRequestTokens(compacted)
		charBudget := remainingTokens * tokensToCharsRatio
		promptText = cfg.Prompt.BuildForModeWithBudget(cfg.PromptMode, charBudget)
	} else {
		promptText = cfg.Prompt.BuildForMode(cfg.PromptMode)
	}
	if promptText != "" {
		state.messages = append([]model.Message{model.NewTextMessage(model.RoleSystem, promptText)}, state.messages...)
	}

	// Reset tool boundary since message indices changed.
	state.lastToolBoundary = 0

	// Record compaction iteration for cooldown.
	state.lastCompactIter = state.iteration

	return true
}
