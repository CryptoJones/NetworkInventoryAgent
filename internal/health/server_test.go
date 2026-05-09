package health_test

import (
	"context"
	"net"
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startServer spins up a health server on a random port and registers cleanup.
func startServer(t *testing.T, tracker *health.Tracker) (baseURL string) {
	t.Helper()
	srv := health.NewServer(":0", tracker)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) }) //nolint:errcheck

	_, port, err := net.SplitHostPort(srv.Addr())
	require.NoError(t, err)
	return "http://127.0.0.1:" + port
}

func TestServer_Ping_Healthy(t *testing.T) {
	tr := health.NewTracker("test")
	url := startServer(t, tr)

	client := health.NewClient(url)
	assert.NoError(t, client.Ping(context.Background()))
}

func TestServer_Ping_Unhealthy(t *testing.T) {
	tr := health.NewTracker("test")
	tr.SetHealthy(false)
	url := startServer(t, tr)

	client := health.NewClient(url)
	err := client.Ping(context.Background())
	assert.Error(t, err, "unhealthy server should return an error")
}

func TestServer_FetchStatus_ReturnsFields(t *testing.T) {
	tr := health.NewTracker("alpha")
	tr.SetHostCount(7)
	tr.RecordScan()
	tr.RecordScan()
	url := startServer(t, tr)

	client := health.NewClient(url)
	s, err := client.FetchStatus(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "alpha", s.Name)
	assert.True(t, s.Healthy)
	assert.Equal(t, 7, s.HostCount)
	assert.Equal(t, 2, s.ScanCount)
	assert.NotNil(t, s.LastScanAt)
}

func TestServer_FetchStatus_NoScansYet(t *testing.T) {
	tr := health.NewTracker("beta")
	url := startServer(t, tr)

	client := health.NewClient(url)
	s, err := client.FetchStatus(context.Background())
	require.NoError(t, err)

	assert.Nil(t, s.LastScanAt)
	assert.Equal(t, 0, s.ScanCount)
}
