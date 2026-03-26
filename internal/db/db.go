package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB with reed-specific helpers.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the SQLite database at the given directory.
// It runs migrations to ensure the schema is up to date.
func Open(dbDir string) (*DB, error) {
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	dsn := filepath.Join(dbDir, "reed.db")
	conn, err := sql.Open("sqlite", dsn+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite single-writer
	conn.SetConnMaxIdleTime(5 * time.Minute)

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// OpenInMemory opens an in-memory SQLite database (for testing).
func OpenInMemory() (*DB, error) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // Each :memory: conn gets a separate DB; limit to 1
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying *sql.DB for advanced usage.
func (db *DB) Conn() *sql.DB {
	return db.conn
}
