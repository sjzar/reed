package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// MediaRepo handles CRUD operations for the media table.
type MediaRepo struct {
	db *DB
}

// NewMediaRepo creates a new MediaRepo.
func NewMediaRepo(db *DB) *MediaRepo {
	return &MediaRepo{db: db}
}

// Insert stores a new media entry.
func (r *MediaRepo) Insert(ctx context.Context, e *model.MediaEntry) error {
	expiresAt := ""
	if !e.ExpiresAt.IsZero() {
		expiresAt = e.ExpiresAt.Format(time.RFC3339)
	}
	_, err := r.db.conn.ExecContext(ctx,
		`INSERT INTO media (id, mime_type, size, storage_path, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.MIMEType, e.Size, e.StoragePath,
		expiresAt, e.CreatedAt.Format(time.RFC3339),
	)
	return err
}

// FindByID retrieves a media entry by its ID.
func (r *MediaRepo) FindByID(ctx context.Context, id string) (*model.MediaEntry, error) {
	row := r.db.conn.QueryRowContext(ctx,
		`SELECT id, mime_type, size, storage_path, expires_at, created_at
		 FROM media WHERE id = ?`, id)
	return r.scanOne(row)
}

// Delete removes a media entry by ID.
func (r *MediaRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM media WHERE id = ?`, id)
	return err
}

// FindExpired returns all media entries whose expires_at is non-empty and before now.
func (r *MediaRepo) FindExpired(ctx context.Context, now time.Time) ([]*model.MediaEntry, error) {
	rows, err := r.db.conn.QueryContext(ctx,
		`SELECT id, mime_type, size, storage_path, expires_at, created_at
		 FROM media WHERE expires_at != '' AND expires_at < ?`,
		now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAll(rows)
}

// SetExpiry sets the expires_at timestamp for a media entry.
func (r *MediaRepo) SetExpiry(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE media SET expires_at = ? WHERE id = ?`,
		expiresAt.Format(time.RFC3339), id)
	return err
}

func (r *MediaRepo) scanOne(row *sql.Row) (*model.MediaEntry, error) {
	e := &model.MediaEntry{}
	var expiresAt, createdAt string
	err := row.Scan(&e.ID, &e.MIMEType, &e.Size, &e.StoragePath, &expiresAt, &createdAt)
	if err != nil {
		return nil, err
	}
	if expiresAt != "" {
		e.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("parse expires_at: %w", err)
		}
	}
	e.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return e, nil
}

func (r *MediaRepo) scanAll(rows *sql.Rows) ([]*model.MediaEntry, error) {
	var result []*model.MediaEntry
	for rows.Next() {
		e := &model.MediaEntry{}
		var expiresAt, createdAt string
		err := rows.Scan(&e.ID, &e.MIMEType, &e.Size, &e.StoragePath, &expiresAt, &createdAt)
		if err != nil {
			return nil, err
		}
		if expiresAt != "" {
			e.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
			if err != nil {
				return nil, fmt.Errorf("parse expires_at: %w", err)
			}
		}
		e.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}
