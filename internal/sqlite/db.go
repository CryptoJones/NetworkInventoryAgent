// Package sqlite provides SQLite-backed implementations of the store interfaces.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"

	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite/migrations"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"

	_ "modernc.org/sqlite"
)

// DB holds two pools backed by the same on-disk SQLite file:
//
//   - writer is pinned at one connection because SQLite supports a single
//     writer at a time; serialising here keeps callers from ever observing
//     SQLITE_BUSY from a concurrent goroutine.
//   - reader allows multiple concurrent connections so dashboard queries and
//     the watchdog don't queue behind an ongoing scan upsert. WAL mode lets
//     them proceed against a consistent snapshot while the writer commits.
//
// Both pools point at the same path; modernc.org/sqlite is a pure-Go driver
// so there's no CGO state to worry about.
type DB struct {
	writer *sql.DB
	reader *sql.DB
}

// Open opens (or creates) the SQLite database at path, applies any pending
// migrations, and returns a ready-to-use DB.
func Open(ctx context.Context, path string) (*DB, error) {
	writer, err := openPool(ctx, path, 1)
	if err != nil {
		return nil, fmt.Errorf("open writer pool: %w", err)
	}

	if err := migrations.Run(ctx, writer); err != nil {
		writer.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// An anonymous `:memory:` database is private to each connection — a
	// second pool would see an empty schema. Tests rely on this path, so
	// collapse to a single pool whenever the storage is per-connection.
	if path == ":memory:" {
		return &DB{writer: writer, reader: writer}, nil
	}

	// Reader pool: a small multiple of GOMAXPROCS is plenty for dashboard /
	// watchdog read concurrency without exhausting SQLite's per-connection
	// file descriptors.
	readers := runtime.GOMAXPROCS(0) * 2
	if readers < 2 {
		readers = 2
	}
	reader, err := openPool(ctx, path, readers)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader pool: %w", err)
	}

	return &DB{writer: writer, reader: reader}, nil
}

// openPool opens a single SQLite pool with the standard pragmas and the
// requested max-open-conns cap.
func openPool(ctx context.Context, path string, maxOpen int) (*sql.DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	pragmas := []string{
		"PRAGMA foreign_keys = ON",   // enforce referential integrity
		"PRAGMA journal_mode = WAL",  // allow concurrent reads during writes
		"PRAGMA busy_timeout = 5000", // wait up to 5 s instead of failing immediately
	}
	for _, p := range pragmas {
		if _, err := conn.ExecContext(ctx, p); err != nil {
			conn.Close()
			return nil, fmt.Errorf("set %q: %w", p, err)
		}
	}
	conn.SetMaxOpenConns(maxOpen)
	return conn, nil
}

// Close releases both underlying database pools.
func (db *DB) Close() error {
	rErr := db.reader.Close()
	wErr := db.writer.Close()
	if wErr != nil {
		return wErr
	}
	return rErr
}

// Hosts returns a HostStore backed by this connection.
func (db *DB) Hosts() store.HostStore { return &HostRepo{writer: db.writer, reader: db.reader} }

// Ports returns a PortStore backed by this connection.
func (db *DB) Ports() store.PortStore { return &PortRepo{writer: db.writer, reader: db.reader} }

// Scans returns a ScanStore backed by this connection.
func (db *DB) Scans() store.ScanStore { return &ScanRepo{writer: db.writer, reader: db.reader} }
