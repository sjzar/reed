package media

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sjzar/reed/internal/db"
	"github.com/sjzar/reed/internal/model"
)

// LocalService implements media storage backed by local files and SQLite metadata.
type LocalService struct {
	repo     *db.MediaRepo
	cacheDir string
	ttl      time.Duration
}

// NewLocalService creates a LocalService.
func NewLocalService(repo *db.MediaRepo, cacheDir string, ttl time.Duration) *LocalService {
	return &LocalService{repo: repo, cacheDir: cacheDir, ttl: ttl}
}

// Upload reads from r, stores the file locally, and inserts metadata into the DB.
// Returns the created MediaEntry. The entry has no expiry by default (use MarkExpirable).
func (s *LocalService) Upload(ctx context.Context, r io.Reader, mimeType string) (*model.MediaEntry, error) {
	id := uuid.New().String()
	dir := s.storageDirForID(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create media dir: %w", err)
	}

	// Write to temp file first, then rename for atomicity.
	tmpFile, err := os.CreateTemp(dir, ".media-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup on failure.
	success := false
	defer func() {
		if !success {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	// Limit read to MaxMediaSize+1 to detect oversized input.
	limited := io.LimitReader(r, MaxMediaSize+1)
	n, err := io.Copy(tmpFile, limited)
	if err != nil {
		return nil, fmt.Errorf("write media file: %w", err)
	}
	if n > MaxMediaSize {
		return nil, fmt.Errorf("media file exceeds maximum size of %d bytes", MaxMediaSize)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp file: %w", err)
	}

	finalPath := filepath.Join(dir, id+ExtForMIME(mimeType))
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, fmt.Errorf("rename media file: %w", err)
	}
	success = true

	entry := &model.MediaEntry{
		ID:          id,
		MIMEType:    mimeType,
		Size:        n,
		StoragePath: finalPath,
		CreatedAt:   time.Now().UTC(),
		// ExpiresAt zero = no expiry
	}
	if err := s.repo.Insert(ctx, entry); err != nil {
		// DB failed — remove the file to avoid orphans.
		os.Remove(finalPath)
		return nil, fmt.Errorf("insert media entry: %w", err)
	}
	return entry, nil
}

// Get retrieves media metadata by ID.
func (s *LocalService) Get(ctx context.Context, id string) (*model.MediaEntry, error) {
	return s.repo.FindByID(ctx, id)
}

// Open returns a reader for the media file and its metadata.
func (s *LocalService) Open(ctx context.Context, id string) (io.ReadCloser, *model.MediaEntry, error) {
	entry, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, os.ErrNotExist
		}
		return nil, nil, err
	}
	f, err := os.Open(entry.StoragePath)
	if err != nil {
		return nil, nil, err
	}
	return f, entry, nil
}

// Delete removes a media entry and its file.
func (s *LocalService) Delete(ctx context.Context, id string) error {
	entry, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	// Remove file first, then DB entry.
	if err := os.Remove(entry.StoragePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove media file: %w", err)
	}
	return s.repo.Delete(ctx, id)
}

// Resolve resolves a media URI to raw bytes.
// Supports media://<id>, data:<mime>;base64,..., and passthrough for http(s):// URLs.
func (s *LocalService) Resolve(ctx context.Context, uri string) (*Resolved, error) {
	switch {
	case IsMediaURI(uri):
		id, _ := ParseURI(uri)
		rc, entry, err := s.Open(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("resolve media %s: %w", id, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return nil, fmt.Errorf("read media %s: %w", id, err)
		}
		return &Resolved{Data: data, MIMEType: entry.MIMEType}, nil

	case IsDataURI(uri):
		mimeType, data, err := ParseDataURI(uri)
		if err != nil {
			return nil, err
		}
		return &Resolved{Data: data, MIMEType: mimeType}, nil

	case strings.HasPrefix(uri, "http://") || strings.HasPrefix(uri, "https://"):
		// URL passthrough — return nil data, caller uses URL directly.
		return &Resolved{MIMEType: "application/octet-stream"}, nil

	default:
		return nil, fmt.Errorf("unsupported media URI scheme: %s", uri)
	}
}

// MarkExpirable sets the expiry timestamp for a media entry.
// After expiry, the entry becomes eligible for GC.
func (s *LocalService) MarkExpirable(ctx context.Context, id string) error {
	return s.repo.SetExpiry(ctx, id, time.Now().Add(s.ttl))
}

// GC removes expired media entries and their files. Returns the number of entries removed.
func (s *LocalService) GC(ctx context.Context) (int, error) {
	expired, err := s.repo.FindExpired(ctx, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("find expired media: %w", err)
	}
	removed := 0
	for _, e := range expired {
		if err := os.Remove(e.StoragePath); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("id", e.ID).Msg("failed to remove expired media file")
			continue
		}
		if err := s.repo.Delete(ctx, e.ID); err != nil {
			log.Warn().Err(err).Str("id", e.ID).Msg("failed to delete expired media entry")
			continue
		}
		removed++
	}
	return removed, nil
}

// CleanOrphans walks the cache directory and removes files that have no
// corresponding DB entry (orphaned uploads) or stale temp files from
// interrupted uploads. Must be called at startup only, before the media
// service handles requests, to avoid races with in-flight uploads.
func (s *LocalService) CleanOrphans(ctx context.Context) (int, error) {
	// Skip if cache directory doesn't exist yet (fresh installation).
	if _, err := os.Stat(s.cacheDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	err := filepath.Walk(s.cacheDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible entries
		}
		if info.IsDir() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		base := filepath.Base(path)

		// Remove stale temp files from interrupted uploads.
		if strings.HasPrefix(base, ".media-upload-") {
			if err := os.Remove(path); err == nil {
				removed++
				log.Debug().Str("path", path).Msg("removed stale media temp file")
			}
			return nil
		}

		// Check UUID-named files against the DB.
		name := strings.TrimSuffix(base, filepath.Ext(base))
		if _, parseErr := uuid.Parse(name); parseErr != nil {
			return nil // not a media file
		}
		_, dbErr := s.repo.FindByID(ctx, name)
		if dbErr == sql.ErrNoRows {
			if err := os.Remove(path); err == nil {
				removed++
				log.Debug().Str("id", name).Msg("removed orphaned media file")
			}
		}
		return nil
	})
	return removed, err
}

// storageDirForID returns the 3-level sharded directory for a media ID.
// e.g., id "abcdef..." → cacheDir/ab/cd/
func (s *LocalService) storageDirForID(id string) string {
	// UUID format: 8-4-4-4-12, strip hyphens for sharding
	clean := strings.ReplaceAll(id, "-", "")
	if len(clean) < 4 {
		return s.cacheDir
	}
	return filepath.Join(s.cacheDir, clean[:2], clean[2:4])
}
