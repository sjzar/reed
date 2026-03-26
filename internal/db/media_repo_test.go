package db

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
)

func TestMediaRepo_InsertAndFindByID(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewMediaRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	entry := &model.MediaEntry{
		ID:          "test-media-001",
		MIMEType:    "image/png",
		Size:        1024,
		StoragePath: "/tmp/test/ab/cd/test-media-001",
		CreatedAt:   now,
	}

	if err := repo.Insert(ctx, entry); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.FindByID(ctx, "test-media-001")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != entry.ID {
		t.Errorf("ID = %q, want %q", got.ID, entry.ID)
	}
	if got.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want %q", got.MIMEType, "image/png")
	}
	if got.Size != 1024 {
		t.Errorf("Size = %d, want %d", got.Size, 1024)
	}
	if got.StoragePath != entry.StoragePath {
		t.Errorf("StoragePath = %q, want %q", got.StoragePath, entry.StoragePath)
	}
	if !got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt = %v, want zero (no expiry)", got.ExpiresAt)
	}
}

func TestMediaRepo_FindByID_NotFound(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewMediaRepo(db)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, "nonexistent")
	if err != sql.ErrNoRows {
		t.Fatalf("FindByID(nonexistent) err = %v, want sql.ErrNoRows", err)
	}
}

func TestMediaRepo_Delete(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewMediaRepo(db)
	ctx := context.Background()

	entry := &model.MediaEntry{
		ID:          "delete-me",
		MIMEType:    "image/jpeg",
		Size:        512,
		StoragePath: "/tmp/test/de/le/delete-me",
		CreatedAt:   time.Now().UTC(),
	}
	if err := repo.Insert(ctx, entry); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.Delete(ctx, "delete-me"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := repo.FindByID(ctx, "delete-me")
	if err != sql.ErrNoRows {
		t.Fatalf("after delete, FindByID err = %v, want sql.ErrNoRows", err)
	}
}

func TestMediaRepo_SetExpiryAndFindExpired(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewMediaRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Insert with no expiry
	entry := &model.MediaEntry{
		ID:          "expire-test",
		MIMEType:    "image/png",
		Size:        256,
		StoragePath: "/tmp/expire-test",
		CreatedAt:   now,
	}
	if err := repo.Insert(ctx, entry); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Should not appear in expired list
	expired, err := repo.FindExpired(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expected 0 expired, got %d", len(expired))
	}

	// Mark expirable in the past
	pastExpiry := now.Add(-time.Hour)
	if err := repo.SetExpiry(ctx, "expire-test", pastExpiry); err != nil {
		t.Fatalf("SetExpiry: %v", err)
	}

	// Now should appear
	expired, err = repo.FindExpired(ctx, now)
	if err != nil {
		t.Fatalf("FindExpired: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0].ID != "expire-test" {
		t.Errorf("expired ID = %q, want %q", expired[0].ID, "expire-test")
	}
}

func TestMediaRepo_MigrationCreatesTable(t *testing.T) {
	db := mustOpenMemory(t)
	_, err := db.conn.Exec("SELECT count(*) FROM media")
	if err != nil {
		t.Fatalf("media table not created: %v", err)
	}
}
