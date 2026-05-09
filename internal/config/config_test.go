package config_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefault(t *testing.T) {
	cfg := config.Default()

	assert.Equal(t, "inventory.db", cfg.Database.Path)
	assert.Equal(t, 5*time.Minute, cfg.Scanner.ScanInterval.Duration)
	assert.Equal(t, 30*time.Second, cfg.Scanner.Timeout.Duration)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
	assert.Empty(t, cfg.Scanner.Subnets)
}

func TestLoad_FileNotExist(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.json")
	require.NoError(t, err, "missing config file should return defaults, not an error")
	assert.Equal(t, "inventory.db", cfg.Database.Path)
	assert.Equal(t, 5*time.Minute, cfg.Scanner.ScanInterval.Duration)
	assert.Equal(t, 30*time.Second, cfg.Scanner.Timeout.Duration)
}

func TestLoad_ValidFile(t *testing.T) {
	data := map[string]any{
		"database": map[string]any{"path": "/tmp/test.db"},
		"scanner": map[string]any{
			"subnets":       []string{"10.0.0.0/8"},
			"scan_interval": "10m",
			"timeout":       "60s",
		},
		"log": map[string]any{"level": "debug", "format": "json"},
	}

	f := writeTempConfig(t, data)

	cfg, err := config.Load(f)
	require.NoError(t, err)

	assert.Equal(t, "/tmp/test.db", cfg.Database.Path)
	assert.Equal(t, []string{"10.0.0.0/8"}, cfg.Scanner.Subnets)
	assert.Equal(t, 10*time.Minute, cfg.Scanner.ScanInterval.Duration)
	assert.Equal(t, 60*time.Second, cfg.Scanner.Timeout.Duration)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("INVENTORY_DB_PATH", "/env/override.db")
	t.Setenv("INVENTORY_LOG_LEVEL", "warn")
	t.Setenv("INVENTORY_LOG_FORMAT", "json")

	cfg, err := config.Load("/nonexistent/config.json")
	require.NoError(t, err)

	assert.Equal(t, "/env/override.db", cfg.Database.Path)
	assert.Equal(t, "warn", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestLoad_EnvOverridesFile(t *testing.T) {
	data := map[string]any{
		"database": map[string]any{"path": "/from/file.db"},
		"log":      map[string]any{"level": "debug"},
	}
	f := writeTempConfig(t, data)

	t.Setenv("INVENTORY_DB_PATH", "/from/env.db")

	cfg, err := config.Load(f)
	require.NoError(t, err)

	assert.Equal(t, "/from/env.db", cfg.Database.Path, "env var must win over file value")
	assert.Equal(t, "debug", cfg.Log.Level, "file value should be kept when no env override")
}

func TestLoad_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.json")
	require.NoError(t, err)
	_, err = f.WriteString("{not valid json")
	require.NoError(t, err)
	f.Close()

	_, err = config.Load(f.Name())
	require.Error(t, err)
}

// writeTempConfig marshals data to a temp JSON file and returns its path.
func writeTempConfig(t *testing.T, data any) string {
	t.Helper()
	b, err := json.Marshal(data)
	require.NoError(t, err)

	f, err := os.CreateTemp(t.TempDir(), "config-*.json")
	require.NoError(t, err)
	_, err = f.Write(b)
	require.NoError(t, err)
	f.Close()
	return f.Name()
}
