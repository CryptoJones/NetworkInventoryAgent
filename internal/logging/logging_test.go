package logging_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/logging"
)

func TestSetup_LevelParsing(t *testing.T) {
	cases := []struct {
		level         string
		debugEnabled  bool
		infoEnabled   bool
		warnEnabled   bool
		errorOnlyName string
	}{
		{"debug", true, true, true, ""},
		{"info", false, true, true, ""},
		{"warn", false, false, true, ""},
		{"error", false, false, false, ""},
		{"bogus", false, true, true, ""}, // unknown falls back to info
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.level, func(t *testing.T) {
			logging.Setup(config.LogConfig{Level: c.level, Format: "text"}, "")
			h := slog.Default().Handler()
			assert.Equal(t, c.debugEnabled, h.Enabled(ctx, slog.LevelDebug), "debug")
			assert.Equal(t, c.infoEnabled, h.Enabled(ctx, slog.LevelInfo), "info")
			assert.Equal(t, c.warnEnabled, h.Enabled(ctx, slog.LevelWarn), "warn")
		})
	}
}

func TestSetup_JSONFormatAndAgentName(t *testing.T) {
	out := captureStdout(t, func() {
		logging.Setup(config.LogConfig{Level: "info", Format: "json"}, "wintermute")
		slog.Info("hello", "k", "v")
	})
	assert.True(t, strings.HasPrefix(strings.TrimSpace(out), "{"), "json output should be an object: %q", out)
	assert.Contains(t, out, `"msg":"hello"`)
	assert.Contains(t, out, `"agent":"wintermute"`)
	assert.Contains(t, out, `"k":"v"`)
}

func TestSetup_TextFormatNoName(t *testing.T) {
	out := captureStdout(t, func() {
		logging.Setup(config.LogConfig{Level: "info", Format: "text"}, "")
		slog.Info("hello")
	})
	assert.Contains(t, out, "hello")
	assert.NotContains(t, out, "agent=", "no agent field when name is empty")
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written. Setup captures os.Stdout at call time, so fn must call Setup
// (not just log) inside the capture window.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}
