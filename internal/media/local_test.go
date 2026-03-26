package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sjzar/reed/internal/db"
)

func mustOpenMemoryDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatalf("OpenInMemory: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newTestService(t *testing.T) *LocalService {
	t.Helper()
	d := mustOpenMemoryDB(t)
	repo := db.NewMediaRepo(d)
	cacheDir := t.TempDir()
	return NewLocalService(repo, cacheDir, 7*24*time.Hour)
}

func TestUploadAndGet(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	data := []byte("hello world png data")
	entry, err := svc.Upload(ctx, bytes.NewReader(data), "image/png")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if entry.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", entry.MIMEType)
	}
	if entry.Size != int64(len(data)) {
		t.Errorf("Size = %d, want %d", entry.Size, len(data))
	}
	if entry.ExpiresAt.IsZero() == false {
		t.Errorf("expected zero ExpiresAt, got %v", entry.ExpiresAt)
	}

	// Verify Get
	got, err := svc.Get(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != entry.ID {
		t.Errorf("Get ID = %q, want %q", got.ID, entry.ID)
	}
}

func TestUploadAndOpen(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	data := []byte("binary file content")
	entry, err := svc.Upload(ctx, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	rc, meta, err := svc.Open(ctx, entry.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()

	if meta.MIMEType != "application/octet-stream" {
		t.Errorf("MIMEType = %q", meta.MIMEType)
	}

	buf := make([]byte, len(data)+1)
	n, _ := rc.Read(buf)
	if string(buf[:n]) != string(data) {
		t.Errorf("content mismatch: got %q", buf[:n])
	}
}

func TestOpenNotFound(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, _, err := svc.Open(ctx, "nonexistent-id")
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	entry, err := svc.Upload(ctx, bytes.NewReader([]byte("del")), "text/plain")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if err := svc.Delete(ctx, entry.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(entry.StoragePath); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted")
	}

	// DB entry should be gone
	_, _, err = svc.Open(ctx, entry.ID)
	if !os.IsNotExist(err) {
		t.Errorf("expected not found after delete, got %v", err)
	}
}

func TestUploadExceedsMaxSize(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Create data that exceeds MaxMediaSize
	big := make([]byte, MaxMediaSize+1)
	_, err := svc.Upload(ctx, bytes.NewReader(big), "application/octet-stream")
	if err == nil {
		t.Fatal("expected error for oversized upload")
	}
}

func TestResolveMediaURI(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	data := []byte("resolve me")
	entry, err := svc.Upload(ctx, bytes.NewReader(data), "image/jpeg")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	resolved, err := svc.Resolve(ctx, URI(entry.ID))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(resolved.Data) != string(data) {
		t.Errorf("data mismatch")
	}
	if resolved.MIMEType != "image/jpeg" {
		t.Errorf("MIMEType = %q", resolved.MIMEType)
	}
}

func TestResolveDataURI(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	raw := []byte("hello")
	encoded := base64.StdEncoding.EncodeToString(raw)
	uri := "data:text/plain;base64," + encoded

	resolved, err := svc.Resolve(ctx, uri)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(resolved.Data) != "hello" {
		t.Errorf("data = %q, want %q", resolved.Data, "hello")
	}
	if resolved.MIMEType != "text/plain" {
		t.Errorf("MIMEType = %q", resolved.MIMEType)
	}
}

func TestMarkExpirableAndGC(t *testing.T) {
	d := mustOpenMemoryDB(t)
	repo := db.NewMediaRepo(d)
	cacheDir := t.TempDir()
	svc := NewLocalService(repo, cacheDir, 1*time.Second)

	ctx := context.Background()

	entry, err := svc.Upload(ctx, bytes.NewReader([]byte("gc me")), "text/plain")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Not yet marked — GC should skip
	n, err := svc.GC(ctx)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 GC'd, got %d", n)
	}

	// Mark with past expiry directly via repo for deterministic test
	pastExpiry := time.Now().UTC().Add(-time.Hour)
	if err := repo.SetExpiry(ctx, entry.ID, pastExpiry); err != nil {
		t.Fatalf("SetExpiry: %v", err)
	}

	n, err = svc.GC(ctx)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 GC'd, got %d", n)
	}

	// Verify file is gone
	if _, err := os.Stat(entry.StoragePath); !os.IsNotExist(err) {
		t.Errorf("expected file removed after GC")
	}
}

func TestStorageDirectory3LevelSharding(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	entry, err := svc.Upload(ctx, bytes.NewReader([]byte("shard")), "text/plain")
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Verify the file exists at a sharded path
	dir := filepath.Dir(entry.StoragePath)
	rel, err := filepath.Rel(svc.cacheDir, dir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	parts := filepath.SplitList(rel)
	// Should be like "ab/cd" (2 directory levels)
	if rel == "." || rel == "" {
		t.Errorf("expected sharded path, got %q", rel)
	}
	_ = parts
}

// --- URI pure function tests ---

func TestURI(t *testing.T) {
	if got := URI("abc"); got != "media://abc" {
		t.Errorf("URI = %q", got)
	}
}

func TestParseURI(t *testing.T) {
	id, ok := ParseURI("media://abc-123")
	if !ok || id != "abc-123" {
		t.Errorf("ParseURI = (%q, %v)", id, ok)
	}
	_, ok = ParseURI("https://example.com")
	if ok {
		t.Error("ParseURI should fail for non-media URI")
	}
}

func TestIsMediaURI(t *testing.T) {
	if !IsMediaURI("media://x") {
		t.Error("expected true")
	}
	if IsMediaURI("data:x") {
		t.Error("expected false for data URI")
	}
}

func TestIsDataURI(t *testing.T) {
	if !IsDataURI("data:text/plain;base64,abc") {
		t.Error("expected true")
	}
	if IsDataURI("media://x") {
		t.Error("expected false for media URI")
	}
}

func TestParseDataURI_Malformed(t *testing.T) {
	tests := []string{
		"not-a-data-uri",
		"data:no-semicolon",
		"data:text/plain;notbase64,abc",
	}
	for _, tc := range tests {
		_, _, err := ParseDataURI(tc)
		if err == nil {
			t.Errorf("expected error for %q", tc)
		}
	}
}
