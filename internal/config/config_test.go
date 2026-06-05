package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	assert.Equal(t, 2*time.Second, cfg.Scanner.Timeout.Duration)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.Equal(t, "text", cfg.Log.Format)
	assert.Empty(t, cfg.Scanner.Subnets)
}

func TestDefault_AdminConfig(t *testing.T) {
	cfg := config.Default()
	assert.Equal(t, "127.0.0.1:9090", cfg.Admin.Addr, "default admin addr should be loopback-only")
}

func TestDefault_ScannerConcurrencyFields(t *testing.T) {
	cfg := config.Default()
	assert.Equal(t, 50, cfg.Scanner.Workers, "default workers should be 50")
	assert.Equal(t, 65535, cfg.Scanner.MaxHosts, "default max_hosts should be 65535")
}

func TestLoad_FileNotExist(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.json")
	require.NoError(t, err, "missing config file should return defaults, not an error")
	assert.Equal(t, "inventory.db", cfg.Database.Path)
	assert.Equal(t, 5*time.Minute, cfg.Scanner.ScanInterval.Duration)
	assert.Equal(t, 2*time.Second, cfg.Scanner.Timeout.Duration)
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

func TestLoad_AdminConfig(t *testing.T) {
	data := map[string]any{
		"log": map[string]any{"level": "info", "format": "text"},
		// An off-loopback bind now requires a token (see the off-loopback
		// validation tests below); include one so this parse check passes.
		"admin": map[string]any{"addr": "0.0.0.0:9090", "auth_token": "tok"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:9090", cfg.Admin.Addr)
}

func TestLoad_AdminConfig_DefaultWhenOmitted(t *testing.T) {
	data := map[string]any{
		"log": map[string]any{"level": "info", "format": "text"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9090", cfg.Admin.Addr, "omitting admin section should keep the default")
}

func TestLoad_ScannerConcurrencyFields(t *testing.T) {
	data := map[string]any{
		"scanner": map[string]any{"workers": 25, "max_hosts": 1024},
		"log":     map[string]any{"level": "info", "format": "text"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, 25, cfg.Scanner.Workers)
	assert.Equal(t, 1024, cfg.Scanner.MaxHosts)
}

func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("INVENTORY_DB_PATH", "/env/override.db")
	t.Setenv("INVENTORY_LOG_LEVEL", "warn")
	t.Setenv("INVENTORY_LOG_FORMAT", "json")
	t.Setenv("INVENTORY_HEALTH_ADDR", "127.0.0.1:18080")
	t.Setenv("INVENTORY_ADMIN_ADDR", "127.0.0.1:19090")

	cfg, err := config.Load("/nonexistent/config.json")
	require.NoError(t, err)

	assert.Equal(t, "/env/override.db", cfg.Database.Path)
	assert.Equal(t, "warn", cfg.Log.Level)
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "127.0.0.1:18080", cfg.Health.Addr)
	assert.Equal(t, "127.0.0.1:19090", cfg.Admin.Addr)
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

func TestLoad_InvalidLogLevel(t *testing.T) {
	data := map[string]any{
		"log": map[string]any{"level": "trace", "format": "text"},
	}
	_, err := config.Load(writeTempConfig(t, data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.level")
}

func TestLoad_InvalidLogFormat(t *testing.T) {
	data := map[string]any{
		"log": map[string]any{"level": "info", "format": "yaml"},
	}
	_, err := config.Load(writeTempConfig(t, data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "log.format")
}

func TestLoad_ValidPeerAddr_HTTP(t *testing.T) {
	data := map[string]any{
		"log":      map[string]any{"level": "info", "format": "text"},
		"watchdog": map[string]any{"peer_addr": "http://localhost:8081"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8081", cfg.Watchdog.PeerAddr)
}

func TestLoad_ValidPeerAddr_HTTPS(t *testing.T) {
	data := map[string]any{
		"log":      map[string]any{"level": "info", "format": "text"},
		"watchdog": map[string]any{"peer_addr": "https://peer.example.com:8081"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, "https://peer.example.com:8081", cfg.Watchdog.PeerAddr)
}

func TestLoad_InvalidPeerAddr_BadScheme(t *testing.T) {
	data := map[string]any{
		"log":      map[string]any{"level": "info", "format": "text"},
		"watchdog": map[string]any{"peer_addr": "file:///etc/passwd"},
	}
	_, err := config.Load(writeTempConfig(t, data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peer_addr")
}

func TestLoad_InvalidPeerAddr_NoHost(t *testing.T) {
	data := map[string]any{
		"log":      map[string]any{"level": "info", "format": "text"},
		"watchdog": map[string]any{"peer_addr": "http://"},
	}
	_, err := config.Load(writeTempConfig(t, data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "peer_addr")
}

func TestLoad_AdminOffLoopbackWithoutToken_Error(t *testing.T) {
	data := map[string]any{
		"log":   map[string]any{"level": "info", "format": "text"},
		"admin": map[string]any{"addr": "0.0.0.0:9090"},
	}
	_, err := config.Load(writeTempConfig(t, data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "admin.addr")
}

func TestLoad_AdminOffLoopbackWithToken_OK(t *testing.T) {
	data := map[string]any{
		"log":   map[string]any{"level": "info", "format": "text"},
		"admin": map[string]any{"addr": "0.0.0.0:9090", "auth_token": "tok"},
	}
	cfg, err := config.Load(writeTempConfig(t, data))
	require.NoError(t, err)
	assert.Equal(t, "tok", cfg.Admin.AuthToken)
}

func TestLoad_AdminToken_EnvOverride(t *testing.T) {
	t.Setenv("INVENTORY_ADMIN_ADDR", "0.0.0.0:9090")
	t.Setenv("INVENTORY_ADMIN_TOKEN", "env-tok")

	cfg, err := config.Load("/nonexistent/config.json")
	require.NoError(t, err, "off-loopback admin bind is satisfied by the env token")
	assert.Equal(t, "env-tok", cfg.Admin.AuthToken)
}

func TestLoad_AdminTokenWorldReadable_Error(t *testing.T) {
	b, err := json.Marshal(map[string]any{
		"log":   map[string]any{"level": "info", "format": "text"},
		"admin": map[string]any{"addr": "127.0.0.1:9090", "auth_token": "tok"},
	})
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, b, 0o644))

	_, err = config.Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "chmod 600")
}

func TestDuration_UnmarshalJSON_String(t *testing.T) {
	var d config.Duration
	require.NoError(t, json.Unmarshal([]byte(`"5m"`), &d))
	assert.Equal(t, 5*time.Minute, d.Duration)
}

func TestDuration_UnmarshalJSON_Integer(t *testing.T) {
	var d config.Duration
	require.NoError(t, json.Unmarshal([]byte(`30000000000`), &d))
	assert.Equal(t, 30*time.Second, d.Duration)
}

func TestDuration_MarshalJSON_RoundTrip(t *testing.T) {
	original := config.Duration{Duration: 5 * time.Minute}

	b, err := json.Marshal(original)
	require.NoError(t, err)

	var restored config.Duration
	require.NoError(t, json.Unmarshal(b, &restored))
	assert.Equal(t, original.Duration, restored.Duration)
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
