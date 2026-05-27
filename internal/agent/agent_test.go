package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/agent"
	"github.com/Ronin48/NetworkInventoryAgent/internal/alerts"
	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// recordingEmitter captures events for diff assertions.
type recordingEmitter struct {
	mu     sync.Mutex
	events []alerts.Event
}

func (r *recordingEmitter) Emit(_ context.Context, ev alerts.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingEmitter) snapshot() []alerts.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]alerts.Event, len(r.events))
	copy(out, r.events)
	return out
}

// --- minimal mock stores ---

type mockHostStore struct {
	mu        sync.Mutex
	hosts     map[int64]*models.Host
	nextID    int64
	listErr   error
	countErr  error
	deleteErr error
	// onListInsert, when non-nil, is upserted into the store BEFORE the
	// SECOND List() call returns — simulating "a new host appeared
	// between the pre- and post-cycle snapshots" without needing a real
	// scanner. Counted by listCalls so the hook fires exactly once,
	// during the post-cycle list.
	onListInsert *models.Host
	listCalls    int
}

func newMockHostStore() *mockHostStore {
	return &mockHostStore{hosts: make(map[int64]*models.Host)}
}

func (m *mockHostStore) Upsert(_ context.Context, h *models.Host) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	clone := *h
	clone.ID = m.nextID
	m.hosts[clone.ID] = &clone
	return clone.ID, nil
}

func (m *mockHostStore) GetByIP(_ context.Context, ip string) (*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, h := range m.hosts {
		if h.IPAddress == ip {
			return h, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockHostStore) List(_ context.Context) ([]*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	m.listCalls++
	if m.onListInsert != nil && m.listCalls == 2 {
		m.nextID++
		clone := *m.onListInsert
		clone.ID = m.nextID
		m.hosts[clone.ID] = &clone
		m.onListInsert = nil
	}
	out := make([]*models.Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		out = append(out, h)
	}
	return out, nil
}

func (m *mockHostStore) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.countErr != nil {
		return 0, m.countErr
	}
	return len(m.hosts), nil
}

func (m *mockHostStore) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.hosts, id)
	return nil
}

type mockPortStore struct{}

func (mockPortStore) Upsert(context.Context, *models.Port) error                { return nil }
func (mockPortStore) ListByHost(context.Context, int64) ([]*models.Port, error) { return nil, nil }
func (mockPortStore) DeleteByHost(context.Context, int64) error                 { return nil }

type mockScanStore struct {
	mu     sync.Mutex
	scans  map[int64]*models.Scan
	nextID atomic.Int64
}

func newMockScanStore() *mockScanStore {
	return &mockScanStore{scans: make(map[int64]*models.Scan)}
}

func (m *mockScanStore) Create(_ context.Context, s *models.Scan) (int64, error) {
	id := m.nextID.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := *s
	clone.ID = id
	m.scans[id] = &clone
	return id, nil
}

func (m *mockScanStore) Finish(_ context.Context, id int64, hostsFound int, finishedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scans[id]
	if !ok {
		return store.ErrNotFound
	}
	s.HostsFound = hostsFound
	s.FinishedAt = &finishedAt
	return nil
}

func (m *mockScanStore) List(_ context.Context) ([]*models.Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*models.Scan, 0, len(m.scans))
	for _, s := range m.scans {
		out = append(out, s)
	}
	return out, nil
}

// --- tests ---

// TestAgent_TriggerCoalesces verifies the buffered trigger channel: the first
// call to Trigger() succeeds, subsequent calls return false until the queued
// trigger has been consumed.
func TestAgent_TriggerCoalesces(t *testing.T) {
	a := agent.New(
		"test",
		config.ScannerConfig{},
		newMockHostStore(),
		mockPortStore{},
		newMockScanStore(),
		health.NewTracker("test"),
		nil,
	)
	assert.True(t, a.Trigger(), "first Trigger() must enqueue")
	assert.False(t, a.Trigger(), "second Trigger() must coalesce, not enqueue")
}

// TestAgent_CycleMarksHealthyOnCleanRun verifies the Healthy flag flips to
// true after a successful cycle, even when no subnets are configured.
func TestAgent_CycleMarksHealthyOnCleanRun(t *testing.T) {
	tracker := health.NewTracker("test")
	tracker.SetHealthy(false) // start unhealthy so we can observe the flip

	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
		},
		newMockHostStore(),
		mockPortStore{},
		newMockScanStore(),
		tracker,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	assert.True(t, tracker.Get().Healthy, "cycle with no errors should report healthy")
	assert.Greater(t, tracker.Get().ScanCount, 0, "at least the immediate cycle should run")
}

// TestAgent_CycleMarksUnhealthyOnCountFailure verifies the Healthy flag flips
// to false when the post-cycle Count call fails — that's the signal that the
// DB write path is broken, not just a flaky scan.
func TestAgent_CycleMarksUnhealthyOnCountFailure(t *testing.T) {
	hosts := newMockHostStore()
	hosts.countErr = errors.New("db gone")

	tracker := health.NewTracker("test")
	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
		},
		hosts,
		mockPortStore{},
		newMockScanStore(),
		tracker,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	assert.False(t, tracker.Get().Healthy, "Count() failure must flip Healthy to false")
}

// TestAgent_PrunesStaleHosts verifies the HostTTL pruning logic.
func TestAgent_PrunesStaleHosts(t *testing.T) {
	hosts := newMockHostStore()
	// Seed two hosts: one recent, one stale.
	_, err := hosts.Upsert(context.Background(), &models.Host{
		IPAddress: "10.0.0.1",
		LastSeen:  time.Now(),
	})
	require.NoError(t, err)
	_, err = hosts.Upsert(context.Background(), &models.Host{
		IPAddress: "10.0.0.2",
		LastSeen:  time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)

	tracker := health.NewTracker("test")
	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
			HostTTL:      config.Duration{Duration: 1 * time.Hour},
		},
		hosts,
		mockPortStore{},
		newMockScanStore(),
		tracker,
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	remaining, err := hosts.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "the stale host should have been pruned")
	if len(remaining) > 0 {
		assert.Equal(t, "10.0.0.1", remaining[0].IPAddress)
	}
}

// TestAgent_PruneDisabledWithoutTTL verifies that pruning is off by default.
func TestAgent_PruneDisabledWithoutTTL(t *testing.T) {
	hosts := newMockHostStore()
	_, _ = hosts.Upsert(context.Background(), &models.Host{
		IPAddress: "10.0.0.2",
		LastSeen:  time.Now().Add(-24 * time.Hour),
	})

	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
			// HostTTL left zero
		},
		hosts,
		mockPortStore{},
		newMockScanStore(),
		health.NewTracker("test"),
		nil,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	remaining, err := hosts.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "with HostTTL=0 no pruning should happen")
}

// TestAgent_EmitsHostVanishedOnPrune verifies that a host pruned via
// the HostTTL path produces a host.vanished alert. The discovered path
// is exercised indirectly: a host present in the pre-cycle snapshot but
// gone from the post-cycle snapshot (due to prune) must alert.
func TestAgent_EmitsHostVanishedOnPrune(t *testing.T) {
	hosts := newMockHostStore()
	_, err := hosts.Upsert(context.Background(), &models.Host{
		IPAddress: "10.0.0.42",
		LastSeen:  time.Now().Add(-24 * time.Hour),
	})
	require.NoError(t, err)

	rec := &recordingEmitter{}
	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
			HostTTL:      config.Duration{Duration: time.Hour},
		},
		hosts,
		mockPortStore{},
		newMockScanStore(),
		health.NewTracker("test"),
		rec,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	evs := rec.snapshot()
	require.NotEmpty(t, evs, "prune should have fired at least one vanished event")
	found := false
	for _, ev := range evs {
		if ev.Type == alerts.HostVanished && ev.IP == "10.0.0.42" {
			found = true
			assert.Equal(t, "test", ev.Agent)
		}
	}
	assert.True(t, found, "expected host.vanished for 10.0.0.42; got %v", evs)
}

// TestAgent_EmitsHostDiscoveredOnNewHost verifies that a host inserted
// mid-cycle (here, pre-seeded via the mock between snapshots) produces
// a host.discovered alert.
func TestAgent_EmitsHostDiscoveredOnNewHost(t *testing.T) {
	hosts := newMockHostStore()
	hosts.onListInsert = &models.Host{
		IPAddress:  "10.0.0.99",
		Hostname:   "newbox.local",
		DeviceType: "linux-host",
		LastSeen:   time.Now(),
	}

	rec := &recordingEmitter{}
	a := agent.New(
		"test",
		config.ScannerConfig{
			Subnets:      nil,
			ScanInterval: config.Duration{Duration: 50 * time.Millisecond},
		},
		hosts,
		mockPortStore{},
		newMockScanStore(),
		health.NewTracker("test"),
		rec,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	evs := rec.snapshot()
	found := false
	for _, ev := range evs {
		if ev.Type == alerts.HostDiscovered && ev.IP == "10.0.0.99" {
			found = true
			assert.Equal(t, "newbox.local", ev.Hostname)
			assert.Equal(t, "linux-host", ev.DeviceType)
		}
	}
	assert.True(t, found, "expected host.discovered for 10.0.0.99; got %v", evs)
}
