// Package migrations embeds versioned SQL files and applies any that have not
// yet been recorded in schema_migrations. Migrations run inside individual
// transactions so a failure leaves the database in its previous state.
package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

// Run applies all pending migrations in lexicographic order.
func Run(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT     PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf("scan migration version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || applied[name] {
			continue
		}

		content, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin transaction for %s: %w", name, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}

		if _, err := tx.Exec(
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
