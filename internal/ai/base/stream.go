package base

import (
	"context"
	"io"

	"github.com/sjzar/reed/internal/model"
)

// StreamBase provides common stream lifecycle fields.
type StreamBase struct {
	Cancel context.CancelFunc
	Resp   model.Response
	Done   bool
}

// CheckDone returns (done event, EOF, true) if the stream is already finished
// or the context is cancelled. Handlers call this at the top of Next().
func (b *StreamBase) CheckDone(ctx context.Context) (model.StreamEvent, error, bool) {
	if b.Done {
		return model.StreamEvent{Type: model.StreamEventDone}, io.EOF, true
	}
	if ctx.Err() != nil {
		return model.StreamEvent{}, ctx.Err(), true
	}
	return model.StreamEvent{}, nil, false
}

// CloseStream cancels the context and calls the provider-specific close function.
func (b *StreamBase) CloseStream(closeFn func() error) error {
	if b.Cancel != nil {
		b.Cancel()
	}
	return closeFn()
}

// Response returns a pointer to the accumulated response.
func (b *StreamBase) Response() *model.Response {
	return &b.Resp
}
