package middleware

import (
	"context"
	"time"

	"github.com/sjzar/reed/internal/ai/base"
	"github.com/sjzar/reed/internal/model"
)

// Timeout wraps a RoundTripper, applying a per-request timeout from req.TimeoutMs.
type Timeout struct {
	next base.RoundTripper
}

// NewTimeout creates a Timeout middleware.
func NewTimeout(next base.RoundTripper) *Timeout {
	return &Timeout{next: next}
}

func (t *Timeout) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	ctx, cancel := t.applyTimeout(ctx, req)
	if cancel != nil {
		// For streaming, we don't defer cancel — the stream owns the context.
		// We wrap the stream to cancel on Close.
		stream, err := t.next.Responses(ctx, req)
		if err != nil {
			cancel()
			return nil, err
		}
		return &timeoutStream{ResponseStream: stream, cancel: cancel}, nil
	}
	return t.next.Responses(ctx, req)
}

// applyTimeout returns a context with timeout if req.TimeoutMs > 0.
func (t *Timeout) applyTimeout(ctx context.Context, req *model.Request) (context.Context, context.CancelFunc) {
	if req.TimeoutMs > 0 {
		return context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
	}
	return ctx, nil
}

// timeoutStream wraps a ResponseStream with a cancel function.
type timeoutStream struct {
	model.ResponseStream
	cancel context.CancelFunc
}

func (s *timeoutStream) Close() error {
	defer s.cancel()
	return s.ResponseStream.Close()
}
