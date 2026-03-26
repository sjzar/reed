package memory

import (
	"context"

	"github.com/sjzar/reed/internal/model"
)

// NoopProvider is a no-op memory provider that never reads or writes memory.
type NoopProvider struct{}

// NewNoopProvider creates a NoopProvider.
func NewNoopProvider() *NoopProvider { return &NoopProvider{} }

// BeforeRun returns empty memory.
func (n *NoopProvider) BeforeRun(_ context.Context, _ RunContext) (MemoryResult, error) {
	return MemoryResult{}, nil
}

// AfterRun does nothing.
func (n *NoopProvider) AfterRun(_ context.Context, _ RunContext, _ []model.Message) error {
	return nil
}
