package db

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/model"
)

func mustOpenMemory(t *testing.T) *DB {
	t.Helper()
	db, err := OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigration(t *testing.T) {
	db := mustOpenMemory(t)
	// Verify tables exist by querying them
	_, err := db.conn.Exec("SELECT count(*) FROM processes")
	if err != nil {
		t.Fatalf("processes table not created: %v", err)
	}
	_, err = db.conn.Exec("SELECT count(*) FROM session_routes")
	if err != nil {
		t.Fatalf("session_routes table not created: %v", err)
	}
}

func TestProcessRepo_InsertAndFindByID(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.ProcessRow{
		ID:             "proc_testapp_a1b2",
		PID:            12345,
		Mode:           "cli",
		Status:         "STARTING",
		WorkflowSource: "./workflow.yml",
		CreatedAt:      now,
		UpdatedAt:      now,
		MetadataJSON:   "{}",
	}

	if err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.FindByID(ctx, row.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.ID != row.ID {
		t.Errorf("ID = %q, want %q", got.ID, row.ID)
	}
	if got.PID != row.PID {
		t.Errorf("PID = %d, want %d", got.PID, row.PID)
	}
	if got.Mode != row.Mode {
		t.Errorf("Mode = %q, want %q", got.Mode, row.Mode)
	}
	if got.Status != row.Status {
		t.Errorf("Status = %q, want %q", got.Status, row.Status)
	}
}

func TestProcessRepo_UpdateStatus(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.ProcessRow{
		ID: "proc_test_0001", PID: 100,
		Mode: "cli", Status: "STARTING",
		WorkflowSource: "test.yml",
		CreatedAt:      now, UpdatedAt: now, MetadataJSON: "{}",
	}
	if err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	meta := `{"last_run":{"status":"SUCCEEDED"}}`
	if err := repo.UpdateStatus(ctx, row.ID, "STOPPED", meta); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, err := repo.FindByID(ctx, row.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got.Status != "STOPPED" {
		t.Errorf("Status = %q, want STOPPED", got.Status)
	}
	if got.MetadataJSON != meta {
		t.Errorf("MetadataJSON = %q, want %q", got.MetadataJSON, meta)
	}
}

func TestProcessRepo_ListActive(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for _, s := range []struct {
		id     string
		status string
	}{
		{"proc_a_0001", "RUNNING"},
		{"proc_b_0002", "STOPPED"},
		{"proc_c_0003", "STARTING"},
	} {
		row := &model.ProcessRow{
			ID: s.id, PID: 1,
			Mode: "cli", Status: s.status,
			WorkflowSource: "w.yml",
			CreatedAt:      now, UpdatedAt: now, MetadataJSON: "{}",
		}
		if err := repo.Insert(ctx, row); err != nil {
			t.Fatalf("Insert %s: %v", s.id, err)
		}
	}

	active, err := repo.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("ListActive count = %d, want 2", len(active))
	}
}

func TestProcessRepo_FindByID_NotFound(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	_, err := repo.FindByID(context.Background(), "nonexistent")
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestProcessRepo_FindByPIDLatest(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Insert two rows with the same PID but different creation times.
	older := &model.ProcessRow{
		ID: "proc_pid_old", PID: 7777,
		Mode: "cli", Status: "STOPPED",
		WorkflowSource: "w.yml",
		CreatedAt:      now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), MetadataJSON: "{}",
	}
	newer := &model.ProcessRow{
		ID: "proc_pid_new", PID: 7777,
		Mode: "cli", Status: "RUNNING",
		WorkflowSource: "w.yml",
		CreatedAt:      now, UpdatedAt: now, MetadataJSON: "{}",
	}
	for _, r := range []*model.ProcessRow{older, newer} {
		if err := repo.Insert(ctx, r); err != nil {
			t.Fatalf("Insert %s: %v", r.ID, err)
		}
	}

	got, err := repo.FindByPIDLatest(ctx, 7777)
	if err != nil {
		t.Fatalf("FindByPIDLatest: %v", err)
	}
	if got.ID != newer.ID {
		t.Errorf("ID = %q, want %q (most recent)", got.ID, newer.ID)
	}
}

func TestProcessRepo_FindByPIDLatest_NotFound(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	_, err := repo.FindByPIDLatest(context.Background(), 0)
	if err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestProcessRepo_ListAll(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	statuses := []string{"RUNNING", "STOPPED", "FAILED"}
	for i, s := range statuses {
		row := &model.ProcessRow{
			ID: fmt.Sprintf("proc_all_%04d", i), PID: i + 1,
			Mode: "cli", Status: s,
			WorkflowSource: "w.yml",
			CreatedAt:      now, UpdatedAt: now, MetadataJSON: "{}",
		}
		if err := repo.Insert(ctx, row); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	all, err := repo.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != len(statuses) {
		t.Errorf("ListAll count = %d, want %d", len(all), len(statuses))
	}
}

func TestProcessRepo_ListAll_Empty(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	all, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("ListAll count = %d, want 0", len(all))
	}
}

func TestProcessRepo_Delete(t *testing.T) {
	db := mustOpenMemory(t)
	repo := NewProcessRepo(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	row := &model.ProcessRow{
		ID: "proc_del_0001", PID: 5555,
		Mode: "cli", Status: "STOPPED",
		WorkflowSource: "w.yml",
		CreatedAt:      now, UpdatedAt: now, MetadataJSON: "{}",
	}
	if err := repo.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := repo.Delete(ctx, row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := repo.FindByID(ctx, row.ID)
	if err != sql.ErrNoRows {
		t.Errorf("after Delete: expected sql.ErrNoRows, got %v", err)
	}
}

func TestDB_Open(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify schema is present in the file-based DB.
	if _, err := db.conn.Exec("SELECT count(*) FROM processes"); err != nil {
		t.Errorf("processes table missing: %v", err)
	}
	if _, err := db.conn.Exec("SELECT count(*) FROM session_routes"); err != nil {
		t.Errorf("session_routes table missing: %v", err)
	}
}

func TestDB_Conn(t *testing.T) {
	db := mustOpenMemory(t)
	if db.Conn() == nil {
		t.Error("Conn() returned nil")
	}
}
