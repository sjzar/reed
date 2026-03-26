package memory

import (
	"context"
	"testing"

	"github.com/sjzar/reed/internal/model"
)

func TestNoopProviderBeforeRun(t *testing.T) {
	p := NewNoopProvider()
	result, err := p.BeforeRun(context.Background(), RunContext{
		Namespace: "ns", AgentID: "agent", SessionKey: "key1",
	})
	if err != nil {
		t.Fatalf("BeforeRun: %v", err)
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
}

func TestNoopProviderAfterRun(t *testing.T) {
	p := NewNoopProvider()
	err := p.AfterRun(context.Background(), RunContext{
		Namespace: "ns", AgentID: "agent", SessionKey: "key1",
	}, []model.Message{
		model.NewTextMessage(model.RoleUser, "hello"),
	})
	if err != nil {
		t.Fatalf("AfterRun: %v", err)
	}
}
