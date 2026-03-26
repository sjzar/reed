package db

import (
	"context"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
)

func TestSessionRouteRepo_UpsertAndFind(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewSessionRouteRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.SessionRouteRow{
		Namespace:        "default",
		AgentID:          "agent_coder",
		SessionKey:       "user_123",
		CurrentSessionID: "sess_abc",
		UpdatedAt:        now,
	}

	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := repo.Find(ctx, "default", "agent_coder", "user_123")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.CurrentSessionID != "sess_abc" {
		t.Errorf("CurrentSessionID = %q, want %q", got.CurrentSessionID, "sess_abc")
	}
}

func TestSessionRouteRepo_UpsertOverwrite(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewSessionRouteRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.SessionRouteRow{
		Namespace: "ns", AgentID: "a1", SessionKey: "k1",
		CurrentSessionID: "sess_old", UpdatedAt: now,
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 1: %v", err)
	}

	row.CurrentSessionID = "sess_new"
	row.UpdatedAt = now.Add(time.Hour)
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert 2: %v", err)
	}

	got, err := repo.Find(ctx, "ns", "a1", "k1")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.CurrentSessionID != "sess_new" {
		t.Errorf("CurrentSessionID = %q, want %q", got.CurrentSessionID, "sess_new")
	}
}

func TestSessionRouteRepo_FindNotFound(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewSessionRouteRepo(db)
	row, err := repo.Find(context.Background(), "x", "y", "z")
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if row != nil {
		t.Errorf("expected nil row, got %v", row)
	}
}

func TestSessionRouteRepo_Delete(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewSessionRouteRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.SessionRouteRow{
		Namespace: "ns", AgentID: "a1", SessionKey: "k1",
		CurrentSessionID: "sess_1", UpdatedAt: now,
	}
	if err := repo.Upsert(ctx, row); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := repo.Delete(ctx, "ns", "a1", "k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	row2, err := repo.Find(ctx, "ns", "a1", "k1")
	if err != nil {
		t.Errorf("expected nil error after delete, got %v", err)
	}
	if row2 != nil {
		t.Errorf("expected nil row after delete, got %v", row2)
	}
}
