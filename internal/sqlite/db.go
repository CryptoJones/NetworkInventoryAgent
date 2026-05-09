// Package sqlite provides SQLite-backed implementations of the store interfaces.
package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite/migrations"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection and exposes typed repository accessors.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the SQLite database at path, applies any pending
// migrations, and returns a ready-to-use DB.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",  // enforce referential integrity
		"PRAGMA journal_mode = WAL", // allow concurrent reads during writes
		"PRAGMA busy_timeout = 5000", // wait up to 5 s instead of failing immediately
	}
	for _, p := range pragmas {
		if _, err := conn.Exec(p); err != nil {
			return nil, fmt.Errorf("set %q: %w", p, err)
		}
	}

	// SQLite supports only one writer at a time; serialise at the driver level
	// so callers never see SQLITE_BUSY from a concurrent goroutine.
	conn.SetMaxOpenConns(1)

	if err := migrations.Run(conn); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

// Hosts returns a HostStore backed by this connection.
func (db *DB) Hosts() *HostRepo { return &HostRepo{conn: db.conn} }

// Ports returns a PortStore backed by this connection.
func (db *DB) Ports() *PortRepo { return &PortRepo{conn: db.conn} }

// Scans returns a ScanStore backed by this connection.
func (db *DB) Scans() *ScanRepo { return &ScanRepo{conn: db.conn} }
