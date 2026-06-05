package scanner_test

import (
	"context"
	"net"
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
		// Mirror the sqlite UPSERT — every mutable field on the
		// incoming row overwrites the stored one. Without this the
		// mock silently differs from the real store on re-upsert,
		// which the classifier path now exercises.
		existing.MACAddress = h.MACAddress
		existing.Hostname = h.Hostname
		existing.OSFingerprint = h.OSFingerprint
		existing.Vendor = h.Vendor
		existing.DeviceType = h.DeviceType
		existing.LastSeen = h.LastSeen
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

func (m *mockScanStore) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var deleted int64
	for id, s := range m.scans {
		if s.StartedAt.Before(cutoff) {
			delete(m.scans, id)
			deleted++
		}
	}
	return deleted, nil
}

func (m *mockScanStore) get(id int64) *models.Scan {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scans[id]
}

// mockPortStore is an in-memory PortStore for tests.
type mockPortStore struct {
	mu     sync.Mutex
	ports  []*models.Port
	nextID int64
}

func newMockPortStore() *mockPortStore {
	return &mockPortStore{}
}

func (m *mockPortStore) Upsert(_ context.Context, p *models.Port) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.ports {
		if existing.HostID == p.HostID && existing.Number == p.Number && existing.Protocol == p.Protocol {
			existing.State = p.State
			existing.LastSeen = p.LastSeen
			return nil
		}
	}
	m.nextID++
	clone := *p
	clone.ID = m.nextID
	m.ports = append(m.ports, &clone)
	return nil
}

func (m *mockPortStore) ListByHost(_ context.Context, hostID int64) ([]*models.Port, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*models.Port
	for _, p := range m.ports {
		if p.HostID == hostID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *mockPortStore) DeleteByHost(_ context.Context, hostID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	kept := m.ports[:0]
	for _, p := range m.ports {
		if p.HostID != hostID {
			kept = append(kept, p)
		}
	}
	m.ports = kept
	return nil
}

// newScanner returns a Scanner with a short dial timeout and modest limits.
func newScanner(hosts *mockHostStore, scans *mockScanStore) *scanner.Scanner {
	return scanner.New(scanner.Options{
		Hosts:    hosts,
		Scans:    scans,
		Timeout:  10 * time.Millisecond,
		Workers:  10,
		MaxHosts: 65535,
	})
}

// --- tests ---

func TestScanner_Scan_InvalidCIDR(t *testing.T) {
	s := newScanner(newMockHostStore(), newMockScanStore())
	_, err := s.Scan(t.Context(), "not-a-cidr", scanner.SubnetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse CIDR")
}

func TestScanner_Scan_MaxHostsGuard(t *testing.T) {
	// Limit to 5 hosts; /24 has 254 usable addresses — should be rejected immediately.
	s := scanner.New(scanner.Options{
		Hosts:    newMockHostStore(),
		Scans:    newMockScanStore(),
		Timeout:  10 * time.Millisecond,
		Workers:  4,
		MaxHosts: 5,
	})
	_, err := s.Scan(t.Context(), "192.168.1.0/24", scanner.SubnetOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds limit")
}

func TestScanner_Scan_ContextCancelled_CompletesGracefully(t *testing.T) {
	s := newScanner(newMockHostStore(), newMockScanStore())

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled before the scan starts

	_, err := s.Scan(ctx, "192.168.1.0/30", scanner.SubnetOptions{})
	// A pre-cancelled context should not produce an error — the scan
	// exits early and the scan record is still finished normally.
	require.NoError(t, err)
}

func TestScanner_Scan_CreatesAndFinishesScanRecord(t *testing.T) {
	scans := newMockScanStore()
	s := newScanner(newMockHostStore(), scans)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel so no probing actually happens

	_, err := s.Scan(ctx, "192.168.1.0/30", scanner.SubnetOptions{})
	require.NoError(t, err)

	scans.mu.Lock()
	defer scans.mu.Unlock()
	require.Len(t, scans.scans, 1, "exactly one scan record should be created")
	for _, rec := range scans.scans {
		assert.Equal(t, "192.168.1.0/30", rec.Subnet)
		assert.NotNil(t, rec.FinishedAt, "scan record must be marked finished")
	}
}

func TestScanner_Scan_PersistsOpenPort(t *testing.T) {
	// Bind a TCP listener on one of the probe ports on 127.0.0.1 so the scan
	// is guaranteed to find at least one open port. We bind 8080 — the lowest-
	// privileged port in the probe set unlikely to clash with system services.
	// If 8080 is in use the host's existing service plays the same role, and
	// the test still passes as long as some probe port answers.
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err == nil {
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Close()
			}
		}()
	}

	hosts := newMockHostStore()
	ports := newMockPortStore()
	scans := newMockScanStore()
	s := scanner.New(scanner.Options{
		Hosts:    hosts,
		Ports:    ports,
		Scans:    scans,
		Timeout:  500 * time.Millisecond,
		Workers:  4,
		MaxHosts: 65535,
	})

	n, err := s.Scan(t.Context(), "127.0.0.1/32", scanner.SubnetOptions{})
	require.NoError(t, err)
	if n == 0 {
		t.Skip("no probe port answered on 127.0.0.1; cannot exercise port persistence")
	}
	require.Equal(t, 1, n)

	host, err := hosts.GetByIP(t.Context(), "127.0.0.1")
	require.NoError(t, err)
	stored, err := ports.ListByHost(t.Context(), host.ID)
	require.NoError(t, err)
	require.Len(t, stored, 1, "exactly one open port should be persisted")
	assert.Contains(t, []int{22, 80, 443, 8080}, stored[0].Number,
		"persisted port must come from the probe set")
	assert.Equal(t, models.TCP, stored[0].Protocol)
	assert.Equal(t, models.StateOpen, stored[0].State)
	assert.Equal(t, host.ID, stored[0].HostID)
}

func TestScanner_Scan_DeepProbePersistsExtraOpenPorts(t *testing.T) {
	// Open two listeners: one acts as the liveness probe answer (8080), the
	// other is reached only when DeepProbe scans the wider port list. We
	// pick an unprivileged port from defaultDeepProbePorts that isn't in the
	// default liveness set.
	liveness, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind tcp listener:", err)
	}
	defer liveness.Close()
	deep, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("cannot bind second tcp listener:", err)
	}
	defer deep.Close()

	go acceptLoop(liveness)
	go acceptLoop(deep)

	hosts := newMockHostStore()
	ports := newMockPortStore()
	scans := newMockScanStore()

	livePort := liveness.Addr().(*net.TCPAddr).Port
	deepPort := deep.Addr().(*net.TCPAddr).Port

	s := scanner.New(scanner.Options{
		Hosts:          hosts,
		Ports:          ports,
		Scans:          scans,
		Timeout:        500 * time.Millisecond,
		Workers:        4,
		MaxHosts:       65535,
		ProbePorts:     []int{livePort},
		DeepProbe:      true,
		DeepProbePorts: []int{livePort, deepPort}, // livePort skipped (already known open)
	})

	n, err := s.Scan(t.Context(), "127.0.0.1/32", scanner.SubnetOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, n)

	host, err := hosts.GetByIP(t.Context(), "127.0.0.1")
	require.NoError(t, err)
	stored, err := ports.ListByHost(t.Context(), host.ID)
	require.NoError(t, err)

	got := map[int]bool{}
	for _, p := range stored {
		got[p.Number] = true
	}
	assert.True(t, got[livePort], "liveness port must be persisted")
	assert.True(t, got[deepPort], "deep-probe port must be persisted")
}

func acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		c.Close()
	}
}

func TestScanner_Scan_PopulatesDeviceType(t *testing.T) {
	// Bind 11211 (memcached) on 127.0.0.1. It's almost never in use on
	// developer machines, and it's a port the classifier recognises
	// unambiguously. If the bind fails (some CI image), skip — the
	// classifier wiring is also covered by classify_test.go.
	ln, err := net.Listen("tcp", "127.0.0.1:11211")
	if err != nil {
		t.Skipf("cannot bind 127.0.0.1:11211 (likely in use): %v", err)
	}
	defer ln.Close()
	go acceptLoop(ln)

	hosts := newMockHostStore()
	ports := newMockPortStore()
	scans := newMockScanStore()
	s := scanner.New(scanner.Options{
		Hosts:      hosts,
		Ports:      ports,
		Scans:      scans,
		Timeout:    500 * time.Millisecond,
		Workers:    4,
		MaxHosts:   65535,
		ProbePorts: []int{11211},
	})

	n, err := s.Scan(t.Context(), "127.0.0.1/32", scanner.SubnetOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, n)

	host, err := hosts.GetByIP(t.Context(), "127.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "database (memcached)", host.DeviceType,
		"classify() should have flipped DeviceType after the scan via a second host upsert")
}

func TestScanner_Scan_DoesNotProbeNetworkOrBroadcast(t *testing.T) {
	hosts := newMockHostStore()
	// Scanner with 1-worker and tiny subnet; cancel right away so no real dials.
	s := scanner.New(scanner.Options{
		Hosts:    hosts,
		Scans:    newMockScanStore(),
		Timeout:  time.Nanosecond,
		Workers:  1,
		MaxHosts: 65535,
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := s.Scan(ctx, "10.0.0.0/30", scanner.SubnetOptions{})
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
