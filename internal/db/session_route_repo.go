package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sjzar/reed/internal/model"
)

// SessionRouteRepo handles CRUD operations for the session_routes table.
type SessionRouteRepo struct {
	db *DB
}

// NewSessionRouteRepo creates a new SessionRouteRepo.
func NewSessionRouteRepo(db *DB) *SessionRouteRepo {
	return &SessionRouteRepo{db: db}
}

// Upsert inserts or updates a session route.
func (r *SessionRouteRepo) Upsert(ctx context.Context, row *model.SessionRouteRow) error {
	_, err := r.db.conn.ExecContext(ctx,
		`INSERT INTO session_routes (namespace, agent_id, session_key, current_session_id, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (namespace, agent_id, session_key)
		 DO UPDATE SET current_session_id = excluded.current_session_id, updated_at = excluded.updated_at`,
		row.Namespace, row.AgentID, row.SessionKey,
		row.CurrentSessionID, row.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// Find looks up a session route by its composite key.
// Returns (nil, nil) when no matching route exists.
func (r *SessionRouteRepo) Find(ctx context.Context, namespace, agentID, sessionKey string) (*model.SessionRouteRow, error) {
	row := &model.SessionRouteRow{}
	var updatedAt string
	err := r.db.conn.QueryRowContext(ctx,
		`SELECT namespace, agent_id, session_key, current_session_id, updated_at
		 FROM session_routes WHERE namespace = ? AND agent_id = ? AND session_key = ?`,
		namespace, agentID, sessionKey,
	).Scan(&row.Namespace, &row.AgentID, &row.SessionKey, &row.CurrentSessionID, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return row, nil
}

// Delete removes a session route by its composite key.
func (r *SessionRouteRepo) Delete(ctx context.Context, namespace, agentID, sessionKey string) error {
	_, err := r.db.conn.ExecContext(ctx,
		`DELETE FROM session_routes WHERE namespace = ? AND agent_id = ? AND session_key = ?`,
		namespace, agentID, sessionKey,
	)
	return err
}

// FindBySessionID reverse-looks up a session route by its current_session_id.
func (r *SessionRouteRepo) FindBySessionID(ctx context.Context, sessionID string) (*model.SessionRouteRow, error) {
	row := &model.SessionRouteRow{}
	var updatedAt string
	err := r.db.conn.QueryRowContext(ctx,
		`SELECT namespace, agent_id, session_key, current_session_id, updated_at
		 FROM session_routes WHERE current_session_id = ?`, sessionID,
	).Scan(&row.Namespace, &row.AgentID, &row.SessionKey, &row.CurrentSessionID, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	row.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return row, nil
}
