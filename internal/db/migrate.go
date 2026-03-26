package db

import (
	"database/sql"
	"fmt"
)

// migration represents a numbered schema change.
type migration struct {
	version int
	sql     string
}

// migrations is the ordered list of schema migrations.
// Existing migrations use IF NOT EXISTS / IF NOT EXISTS so they are
// safe to replay on databases that pre-date the _schema_version table.
var migrations = []migration{
	{1, `CREATE TABLE IF NOT EXISTS processes (
		id TEXT PRIMARY KEY,
		pid INTEGER NOT NULL,
		mode TEXT NOT NULL,
		status TEXT NOT NULL,
		workflow_source TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		metadata_json TEXT NOT NULL DEFAULT '{}'
	)`},
	{2, `CREATE INDEX IF NOT EXISTS idx_processes_status ON processes(status)`},
	{3, `CREATE INDEX IF NOT EXISTS idx_processes_pid ON processes(pid)`},
	{4, `CREATE TABLE IF NOT EXISTS session_routes (
		namespace TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		session_key TEXT NOT NULL,
		current_session_id TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (namespace, agent_id, session_key)
	)`},
	{5, `CREATE TABLE IF NOT EXISTS media (
		id TEXT PRIMARY KEY,
		mime_type TEXT NOT NULL,
		size INTEGER NOT NULL,
		storage_path TEXT NOT NULL,
		expires_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`},
	{6, `CREATE INDEX IF NOT EXISTS idx_media_expires_at ON media(expires_at)`},
	{7, `CREATE INDEX IF NOT EXISTS idx_session_routes_session_id ON session_routes(current_session_id)`},
}

func (db *DB) migrate() error {
	// Bootstrap the schema version table.
	if _, err := db.conn.Exec(`CREATE TABLE IF NOT EXISTS _schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create _schema_version: %w", err)
	}

	// Load already-applied versions.
	applied, err := loadAppliedVersions(db.conn)
	if err != nil {
		return fmt.Errorf("load schema versions: %w", err)
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}

		tx, err := db.conn.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(
			`INSERT INTO _schema_version (version, applied_at) VALUES (?, datetime('now'))`,
			m.version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

// loadAppliedVersions returns a set of already-applied migration versions.
func loadAppliedVersions(conn *sql.DB) (map[int]bool, error) {
	rows, err := conn.Query(`SELECT version FROM _schema_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
