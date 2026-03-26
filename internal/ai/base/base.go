// Package base provides shared types for ai handler subpackages.
package base

import (
	"context"

	"github.com/sjzar/reed/internal/model"
)

// RoundTripper is the single-method interface for LLM request dispatch.
type RoundTripper interface {
	Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error)
}

// RoundTripperFunc is an adapter for using ordinary functions as RoundTrippers.
type RoundTripperFunc func(ctx context.Context, req *model.Request) (model.ResponseStream, error)

func (f RoundTripperFunc) Responses(ctx context.Context, req *model.Request) (model.ResponseStream, error) {
	return f(ctx, req)
}
