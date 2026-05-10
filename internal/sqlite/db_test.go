package sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestDB opens an in-memory SQLite database and registers cleanup.
// All repo tests should use this helper for isolation and speed.
func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), ":memory:")
	require.NoError(t, err, "failed to open in-memory test database")
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpen_InMemory(t *testing.T) {
	db, err := sqlite.Open(t.Context(), ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestOpen_FileDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sqlite.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, db.Close())
}

func TestOpen_RunsMigrations(t *testing.T) {
	db := openTestDB(t)

	// If migrations ran, we can write a host without error.
	hosts := db.Hosts()
	_, err := hosts.Upsert(t.Context(), newTestHost("192.168.1.1"))
	assert.NoError(t, err, "upsert should succeed after migrations have run")
}

func TestOpen_InvalidPath(t *testing.T) {
	_, err := sqlite.Open(t.Context(), "/this/directory/does/not/exist/db.sqlite")
	require.Error(t, err)
}
