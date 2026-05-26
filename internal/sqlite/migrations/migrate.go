// Package migrations embeds versioned SQL files and applies any that have not
// yet been recorded in schema_migrations. Migrations run inside individual
// transactions so a failure leaves the database in its previous state.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Run applies all pending migrations in lexicographic order.
func Run(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT     PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]struct{})
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan migration version: %w", err)
		}
		applied[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		if _, ok := applied[name]; ok {
			continue
		}

		content, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin transaction for %s: %w", name, err)
		}

		// Upgrade to a write transaction up front so two agents booting
		// against a shared SQLite file serialise here instead of racing the
		// applied-versions check above. The deferred default transaction
		// would only acquire the write lock on first INSERT, by which point
		// both processes might have read an empty applied set.
		if _, err := tx.ExecContext(ctx, `ROLLBACK; BEGIN IMMEDIATE`); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("begin immediate for %s: %w", name, err)
		}

		// Re-check after acquiring the write lock — a peer agent may have
		// applied this migration while we were waiting.
		var already int
		if err := tx.QueryRowContext(ctx,
			`SELECT count() FROM schema_migrations WHERE version = ?`, name,
		).Scan(&already); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("recheck applied %s: %w", name, err)
		}
		if already > 0 {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit no-op for %s: %w", name, err)
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}
