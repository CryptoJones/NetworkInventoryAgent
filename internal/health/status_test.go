package health_test

import (
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTracker_Defaults(t *testing.T) {
	tr := health.NewTracker("agent1")
	s := tr.Get()

	assert.Equal(t, "agent1", s.Name)
	assert.True(t, s.Healthy)
	assert.Nil(t, s.LastScanAt)
	assert.Equal(t, 0, s.HostCount)
	assert.Equal(t, 0, s.ScanCount)
	assert.False(t, s.StartedAt.IsZero())
}

func TestTracker_SetHostCount(t *testing.T) {
	tr := health.NewTracker("a")
	tr.SetHostCount(42)
	assert.Equal(t, 42, tr.Get().HostCount)
}

func TestTracker_RecordScan(t *testing.T) {
	tr := health.NewTracker("a")
	before := time.Now()
	tr.RecordScan()
	tr.RecordScan()

	s := tr.Get()
	require.Equal(t, 2, s.ScanCount)
	require.NotNil(t, s.LastScanAt)
	assert.False(t, s.LastScanAt.Before(before), "LastScanAt should not be before the test started")
}

func TestTracker_SetHealthy(t *testing.T) {
	tr := health.NewTracker("a")
	assert.True(t, tr.Get().Healthy)

	tr.SetHealthy(false)
	assert.False(t, tr.Get().Healthy)

	tr.SetHealthy(true)
	assert.True(t, tr.Get().Healthy)
}
