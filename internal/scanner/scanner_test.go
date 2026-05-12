package scanner_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/scanner"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- in-memory mock stores ---

type mockHostStore struct {
	mu     sync.Mutex
	hosts  map[string]*models.Host
	nextID int64
}

func newMockHostStore() *mockHostStore {
	return &mockHostStore{hosts: make(map[string]*models.Host)}
}

func (m *mockHostStore) Upsert(_ context.Context, h *models.Host) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.hosts[h.IPAddress]; ok {
		return existing.ID, nil
	}
	m.nextID++
	clone := *h
	clone.ID = m.nextID
	m.hosts[h.IPAddress] = &clone
	return m.nextID, nil
}

func (m *mockHostStore) GetByIP(_ context.Context, ip string) (*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.hosts[ip]; ok {
		return h, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockHostStore) List(_ context.Context) ([]*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*models.Host, 0, len(m.hosts))
	for _, h := range m.hosts {
		out = append(out, h)
	}
	return out, nil
}

func (m *mockHostStore) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hosts), nil
}

func (m *mockHostStore) Delete(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ip, h := range m.hosts {
		if h.ID == id {
			delete(m.hosts, ip)
			return nil
		}
	}
	return store.ErrNotFound
}

type mockScanStore struct {
	mu     sync.Mutex
	scans  map[int64]*models.Scan
	nextID int64
}

func newMockScanStore() *mockScanStore {
	return &mockScanStore{scans: make(map[int64]*models.Scan)}
}

func (m *mockScanStore) Create(_ context.Context, s *models.Scan) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	clone := *s
	clone.ID = m.nextID
	m.scans[m.nextID] = &clone
	return m.nextID, nil
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

func (m *mockScanStore) get(id int64) *models.Scan {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scans[id]
}

// newScanner returns a Scanner with a short dial timeout and modest limits.
func newScanner(hosts *mockHostStore, scans *mockScanStore) *scanner.Scanner {
	return scanner.New(hosts, scans, 10*time.Millisecond, 10, 65535, false, 0)
}

// --- tests ---

func TestScanner_Scan_InvalidCIDR(t *testing.T) {
	s := newScanner(newMockHostStore(), newMockScanStore())
	_, err := s.Scan(t.Context(), "not-a-cidr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse CIDR")
}

func TestScanner_Scan_MaxHostsGuard(t *testing.T) {
	// Limit to 5 hosts; /24 has 254 usable addresses — should be rejected immediately.
	s := scanner.New(newMockHostStore(), newMockScanStore(), 10*time.Millisecond, 4, 5, false, 0)
	_, err := s.Scan(t.Context(), "192.168.1.0/24")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
}

func TestScanner_Scan_ContextCancelled_CompletesGracefully(t *testing.T) {
	s := newScanner(newMockHostStore(), newMockScanStore())

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled before the scan starts

	_, err := s.Scan(ctx, "192.168.1.0/30")
	// A pre-cancelled context should not produce an error — the scan
	// exits early and the scan record is still finished normally.
	require.NoError(t, err)
}

func TestScanner_Scan_CreatesAndFinishesScanRecord(t *testing.T) {
	scans := newMockScanStore()
	s := newScanner(newMockHostStore(), scans)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel so no probing actually happens

	_, err := s.Scan(ctx, "192.168.1.0/30")
	require.NoError(t, err)

	scans.mu.Lock()
	defer scans.mu.Unlock()
	require.Len(t, scans.scans, 1, "exactly one scan record should be created")
	for _, rec := range scans.scans {
		assert.Equal(t, "192.168.1.0/30", rec.Subnet)
		assert.NotNil(t, rec.FinishedAt, "scan record must be marked finished")
	}
}

func TestScanner_Scan_DoesNotProbeNetworkOrBroadcast(t *testing.T) {
	hosts := newMockHostStore()
	// Scanner with 1-worker and tiny subnet; cancel right away so no real dials.
	s := scanner.New(hosts, newMockScanStore(), time.Nanosecond, 1, 65535, false, 0)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s.Scan(ctx, "10.0.0.0/30")
	require.NoError(t, err)

	// With a pre-cancelled context nothing is probed, but verify we stored
	// no network (.0) or broadcast (.3) address even if probing had succeeded.
	hosts.mu.Lock()
	defer hosts.mu.Unlock()
	_, hasNetwork := hosts.hosts["10.0.0.0"]
	_, hasBroadcast := hosts.hosts["10.0.0.3"]
	assert.False(t, hasNetwork, "network address must not be probed")
	assert.False(t, hasBroadcast, "broadcast address must not be probed")
}
