package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// ProcessRepo handles CRUD operations for the processes table.
type ProcessRepo struct {
	db *DB
}

// NewProcessRepo creates a new ProcessRepo.
func NewProcessRepo(db *DB) *ProcessRepo {
	return &ProcessRepo{db: db}
}

// Insert registers a new Process in the database.
func (r *ProcessRepo) Insert(ctx context.Context, row *model.ProcessRow) error {
	_, err := r.db.conn.ExecContext(ctx,
		`INSERT INTO processes (id, pid, mode, status, workflow_source, created_at, updated_at, metadata_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.PID,
		row.Mode, row.Status, row.WorkflowSource,
		row.CreatedAt.Format(time.RFC3339),
		row.UpdatedAt.Format(time.RFC3339),
		row.MetadataJSON,
	)
	return err
}

// UpdateStatus updates the status and metadata of a Process.
func (r *ProcessRepo) UpdateStatus(ctx context.Context, id string, status string, metadataJSON string) error {
	_, err := r.db.conn.ExecContext(ctx,
		`UPDATE processes SET status = ?, metadata_json = ?, updated_at = ? WHERE id = ?`,
		status, metadataJSON, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// FindByID retrieves a Process by its ProcessID.
func (r *ProcessRepo) FindByID(ctx context.Context, id string) (*model.ProcessRow, error) {
	return r.scanOne(r.db.conn.QueryRowContext(ctx,
		`SELECT id, pid, mode, status, workflow_source, created_at, updated_at, metadata_json
		 FROM processes WHERE id = ?`, id))
}

// ListActive returns all processes with STARTING or RUNNING status.
func (r *ProcessRepo) ListActive(ctx context.Context) ([]*model.ProcessRow, error) {
	rows, err := r.db.conn.QueryContext(ctx,
		`SELECT id, pid, mode, status, workflow_source, created_at, updated_at, metadata_json
		 FROM processes WHERE status IN ('STARTING', 'RUNNING')
		 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAll(rows)
}

// ListAll returns all processes ordered by creation time.
func (r *ProcessRepo) ListAll(ctx context.Context) ([]*model.ProcessRow, error) {
	rows, err := r.db.conn.QueryContext(ctx,
		`SELECT id, pid, mode, status, workflow_source, created_at, updated_at, metadata_json
		 FROM processes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanAll(rows)
}

// Delete removes a process row by ID.
func (r *ProcessRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.conn.ExecContext(ctx, `DELETE FROM processes WHERE id = ?`, id)
	return err
}

// FindByPIDLatest retrieves the most recent Process with the given PID.
func (r *ProcessRepo) FindByPIDLatest(ctx context.Context, pid int) (*model.ProcessRow, error) {
	return r.scanOne(r.db.conn.QueryRowContext(ctx,
		`SELECT id, pid, mode, status, workflow_source, created_at, updated_at, metadata_json
		 FROM processes WHERE pid = ? ORDER BY created_at DESC LIMIT 1`, pid))
}

func (r *ProcessRepo) scanOne(row *sql.Row) (*model.ProcessRow, error) {
	p := &model.ProcessRow{}
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.PID, &p.Mode, &p.Status,
		&p.WorkflowSource, &createdAt, &updatedAt, &p.MetadataJSON)
	if err != nil {
		return nil, err
	}
	p.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	p.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return p, nil
}

func (r *ProcessRepo) scanAll(rows *sql.Rows) ([]*model.ProcessRow, error) {
	var result []*model.ProcessRow
	for rows.Next() {
		p := &model.ProcessRow{}
		var createdAt, updatedAt string
		err := rows.Scan(&p.ID, &p.PID, &p.Mode, &p.Status,
			&p.WorkflowSource, &createdAt, &updatedAt, &p.MetadataJSON)
		if err != nil {
			return nil, err
		}
		p.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		p.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}
