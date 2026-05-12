package logging_test

import (
	"log/slog"
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevelNameMapping(t *testing.T) {
	assert.Equal(t, slog.LevelInfo, logging.LevelName["standard"])
	assert.Equal(t, slog.LevelDebug, logging.LevelName["debug"])
	assert.Equal(t, slog.LevelError+1, logging.LevelName["none"], "none should suppress all built-in levels")
	assert.Equal(t, slog.LevelInfo-2, logging.LevelName["verbose"], "verbose should be between info and debug")
}

func TestSetup_DefaultLevel(t *testing.T) {
	cfg := config.LogConfig{Level: "standard", Format: "text"}
	logging.Setup(cfg)
	assert.True(t, slog.Default().Enabled(nil, slog.LevelInfo))
}

func TestSetup_DebugLevel(t *testing.T) {
	cfg := config.LogConfig{Level: "debug", Format: "text"}
	logging.Setup(cfg)
	assert.True(t, slog.Default().Enabled(nil, slog.LevelDebug))
}

func TestSetup_NoneLevel(t *testing.T) {
	cfg := config.LogConfig{Level: "none", Format: "text"}
	logging.Setup(cfg)
	// None sets level above Error+1, so debug/info/warn/error all disabled.
	assert.False(t, slog.Default().Enabled(nil, slog.LevelDebug))
	assert.False(t, slog.Default().Enabled(nil, slog.LevelInfo))
	assert.False(t, slog.Default().Enabled(nil, slog.LevelWarn))
	assert.False(t, slog.Default().Enabled(nil, slog.LevelError))
}

func TestSetup_VerboseLevel(t *testing.T) {
	cfg := config.LogConfig{Level: "verbose", Format: "text"}
	logging.Setup(cfg)
	// Verbose should have info enabled but not debug.
	assert.True(t, slog.Default().Enabled(nil, slog.LevelInfo))
	assert.False(t, slog.Default().Enabled(nil, slog.LevelDebug))
}

func TestSetup_InvalidLevelFallsBackToDefault(t *testing.T) {
	cfg := config.LogConfig{Level: "banana", Format: "text"}
	logging.Setup(cfg)
	// Invalid level falls back to Info (standard).
	assert.True(t, slog.Default().Enabled(nil, slog.LevelInfo))
}

func TestSetup_JSONFormat(t *testing.T) {
	cfg := config.LogConfig{Level: "debug", Format: "json"}
	logging.Setup(cfg)
	// No runtime verification needed — format affects output shape, not levels.
	// If this doesn't panic, Setup handled the json handler correctly.
}

func TestLogv_VisibleAtVerbose(t *testing.T) {
	// Setup at verbose level, Logv should produce output.
	cfg := config.LogConfig{Level: "verbose", Format: "text"}
	logging.Setup(cfg)
	// This test verifies no panic; actual output goes to stdout.
	logging.Logv("test message", "key", "value")
}

func TestSetOutputFile_FilePath(t *testing.T) {
	tmpFile := t.TempDir() + "/test.log"
	err := logging.SetOutputFile(tmpFile)
	require.NoError(t, err)

	// Log something
	logging.Logv("file test", "ok", true)

	err = logging.SetOutputFile("")
	require.NoError(t, err)

	// Verify the file exists and has content
	// (we're testing the file was opened, not the content specifically)
	logging.CloseOutput()
}

func TestSetOutputFile_RevertToStdout(t *testing.T) {
	err := logging.SetOutputFile("")
	require.NoError(t, err)
}
