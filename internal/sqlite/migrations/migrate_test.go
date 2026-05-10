package migrations_test

import (
	"database/sql"
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRun_CreatesSchemaTable(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, migrations.Run(t.Context(), db))

	var count int
	err := db.QueryRow(
		`SELECT count() FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "schema_migrations table should exist after Run")
}

func TestRun_AppliesInitialMigration(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, migrations.Run(t.Context(), db))

	for _, table := range []string{"hosts", "ports", "scans"} {
		var count int
		err := db.QueryRow(
			`SELECT count() FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "table %q should exist after migration", table)
	}
}

func TestRun_RecordsMigrationVersion(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, migrations.Run(t.Context(), db))

	var version string
	err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, "001_initial.sql", version)
}

func TestRun_Idempotent(t *testing.T) {
	db := openMemDB(t)
	require.NoError(t, migrations.Run(t.Context(), db), "first run should succeed")
	require.NoError(t, migrations.Run(t.Context(), db), "second run should be a no-op, not an error")

	var count int
	err := db.QueryRow(`SELECT count() FROM schema_migrations`).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "migration should be recorded exactly once")
}
