package engine

import "time"

// Option configures an Engine.
type Option func(*Engine)

// WithNowFunc overrides the clock function (default: time.Now().UTC()).
func WithNowFunc(fn func() time.Time) Option {
	return func(e *Engine) { e.now = fn }
}

// WithNewRunID overrides run ID generation.
func WithNewRunID(fn func() string) Option {
	return func(e *Engine) { e.newRunID = fn }
}

// WithNewStepRunID overrides step run ID generation.
func WithNewStepRunID(fn func() string) Option {
	return func(e *Engine) { e.newStepRunID = fn }
}

// WithRenderString overrides the expression renderer.
func WithRenderString(fn func(string, map[string]any) (any, error)) Option {
	return func(e *Engine) { e.renderString = fn }
}
