package ai

import (
	"context"
	"errors"
	"fmt"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/ai/middleware"
	"github.com/sjzar/reed/internal/model"
)

// failoverTripper iterates through a queue of candidates, dispatching the request
// to each until one succeeds. It applies middleware per candidate and handles
// context cancellation, penalty tracking, and context-window filtering.
type failoverTripper struct {
	service *Service
	queue   []candidate
	agentID string
}

// dispatch sends a streaming request with failover on initial connection failure.
// Once a stream is established, mid-stream errors are NOT retried.
func (ft *failoverTripper) dispatch(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	var lastErr error
	allSkippedWindow := true

	for _, entry := range ft.queue {
		if ft.service.penalty.isPenalized(entry.providerID, entry.modelID) {
			continue
		}
		// Skip candidates whose context window cannot fit the estimated prompt tokens.
		// Reserve 20% headroom for completion tokens.
		if req.EstimatedTokens > 0 && entry.metadata.ContextWindow > 0 {
			if req.EstimatedTokens > int(float64(entry.metadata.ContextWindow)*0.8) {
				continue
			}
		}
		allSkippedWindow = false

		fwdReq := cloneRequest(req, entry.modelID)
		handler := ft.wrapHandler(entry)

		stream, err := handler.Responses(ctx, fwdReq)
		if err == nil {
			return stream, nil // committed — no mid-stream failover
		}

		// Context cancel / deadline — return immediately, no penalty, no failover
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		action, ttl := classifyForFailover(err)
		if ttl > 0 {
			ft.service.penalty.penalize(entry.providerID, entry.modelID, ttl)
		}
		if action == actionHardStop {
			return nil, err
		}
		lastErr = err
	}

	if allSkippedWindow && lastErr == nil {
		return nil, &model.AIError{
			Kind:    model.ErrContextOverflow,
			Message: fmt.Sprintf("ai: all candidates skipped — estimated tokens exceed context window for agent %q", ft.agentID),
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("ai: all providers penalized for agent %q", ft.agentID)
}

// wrapHandler wraps the raw provider handler with the middleware chain:
// clone → timeout → handler
//
// All providers use streaming-only SDKs (NewStreaming). Non-streaming models
// simply produce a single-chunk stream, which the stream protocol handles
// transparently. No polyfill or adapter is needed.
func (ft *failoverTripper) wrapHandler(entry candidate) base.RoundTripper {
	raw := ft.service.providers[entry.providerIdx].handler
	var h base.RoundTripper = raw

	// Apply per-request timeout
	h = middleware.NewTimeout(h)

	// Deep-copy request to protect against mutation during failover
	h = middleware.NewClone(h)

	return h
}
