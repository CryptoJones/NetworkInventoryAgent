package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// --- mock stores ---

type mockHostStore struct {
	mu    sync.Mutex
	hosts []*models.Host
	err   error
}

func (m *mockHostStore) Upsert(_ context.Context, h *models.Host) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h.ID = int64(len(m.hosts) + 1)
	m.hosts = append(m.hosts, h)
	return h.ID, m.err
}

func (m *mockHostStore) List(_ context.Context) ([]*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hosts, m.err
}

func (m *mockHostStore) GetByIP(_ context.Context, ip string) (*models.Host, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	for _, h := range m.hosts {
		if h.IPAddress == ip {
			return h, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockHostStore) Count(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hosts), m.err
}

func (m *mockHostStore) Delete(_ context.Context, _ int64) error {
	return nil
}

type mockPortStore struct {
	mu    sync.Mutex
	ports []*models.Port
	err   error
}

func (m *mockPortStore) Upsert(_ context.Context, p *models.Port) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ports = append(m.ports, p)
	return m.err
}

func (m *mockPortStore) ListByHost(_ context.Context, hostID int64) ([]*models.Port, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	var out []*models.Port
	for _, p := range m.ports {
		if p.HostID == hostID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *mockPortStore) DeleteByHost(_ context.Context, _ int64) error {
	return nil
}

type mockScanStore struct {
	mu    sync.Mutex
	scans []*models.Scan
	err   error
}

func (m *mockScanStore) Create(_ context.Context, s *models.Scan) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.ID = int64(len(m.scans) + 1)
	m.scans = append(m.scans, s)
	return s.ID, m.err
}

func (m *mockScanStore) Finish(_ context.Context, id int64, hostsFound int, finishedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scans {
		if s.ID == id {
			s.FinishedAt = &finishedAt
			s.HostsFound = hostsFound
		}
	}
	return m.err
}

func (m *mockScanStore) List(_ context.Context) ([]*models.Scan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scans, m.err
}

// --- helpers ---

func healthyStatus() health.Status {
	return health.Status{
		Name:      "test-agent",
		Healthy:   true,
		StartedAt: time.Now(),
	}
}

func newTestServer(t *testing.T, hosts *mockHostStore, ports *mockPortStore, scans *mockScanStore) *admin.Server {
	t.Helper()
	srv, err := admin.NewServer(":0", "test-agent", hosts, ports, scans, healthyStatus, nil)
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

func get(t *testing.T, srv *admin.Server, path string) *http.Response {
	t.Helper()
	resp, err := http.Get("http://" + srv.Addr() + path)
	require.NoError(t, err)
	return resp
}

// --- tests ---

func TestNewServer_ParsesTemplates(t *testing.T) {
	_, err := admin.NewServer(":0", "agent", &mockHostStore{}, &mockPortStore{}, &mockScanStore{}, healthyStatus, nil)
	require.NoError(t, err, "template parsing should succeed on a clean build")
}

func TestServer_Start_ListensOnRandomPort(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	assert.NotEmpty(t, srv.Addr())
	assert.NotEqual(t, ":0", srv.Addr())
}

func TestHandleDashboard_OK(t *testing.T) {
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 1, IPAddress: "10.0.0.1", LastSeen: time.Now()},
	}}
	scans := &mockScanStore{scans: []*models.Scan{
		{ID: 1, Subnet: "10.0.0.0/24", StartedAt: time.Now(), HostsFound: 1},
	}}
	srv := newTestServer(t, hosts, &mockPortStore{}, scans)

	resp := get(t, srv, "/")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestHandleDashboard_ContainsDashboardContent(t *testing.T) {
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 1, IPAddress: "192.168.1.10", LastSeen: time.Now()},
		{ID: 2, IPAddress: "192.168.1.11", LastSeen: time.Now()},
	}}
	srv := newTestServer(t, hosts, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/")
	defer resp.Body.Close()

	body := readBody(t, resp)
	assert.Contains(t, body, "Dashboard")
	assert.Contains(t, body, "Total Hosts")
}

func TestHandleHosts_OK(t *testing.T) {
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 1, IPAddress: "10.0.0.1", Hostname: "router", LastSeen: time.Now()},
		{ID: 2, IPAddress: "10.0.0.2", LastSeen: time.Now()},
	}}
	srv := newTestServer(t, hosts, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/hosts")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "10.0.0.1")
	assert.Contains(t, body, "router")
	assert.Contains(t, body, "10.0.0.2")
}

func TestHandleHosts_EmptyInventory(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/hosts")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "No hosts discovered yet")
}

func TestHandleHostDetail_Found(t *testing.T) {
	now := time.Now()
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 42, IPAddress: "10.0.0.5", Hostname: "myhost", FirstSeen: now, LastSeen: now},
	}}
	ports := &mockPortStore{ports: []*models.Port{
		{HostID: 42, Number: 80, Protocol: models.TCP, State: models.StateOpen, Service: "http", FirstSeen: now, LastSeen: now},
		{HostID: 42, Number: 443, Protocol: models.TCP, State: models.StateOpen, Service: "https", FirstSeen: now, LastSeen: now},
	}}
	srv := newTestServer(t, hosts, ports, &mockScanStore{})

	resp := get(t, srv, "/hosts/10.0.0.5")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "10.0.0.5")
	assert.Contains(t, body, "myhost")
	assert.Contains(t, body, "80")
	assert.Contains(t, body, "http")
	assert.Contains(t, body, "443")
	assert.Contains(t, body, "https")
}

func TestHandleHostDetail_NotFound(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/hosts/1.2.3.4")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandleHostDetail_NoPorts(t *testing.T) {
	now := time.Now()
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 7, IPAddress: "10.0.0.7", FirstSeen: now, LastSeen: now},
	}}
	srv := newTestServer(t, hosts, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/hosts/10.0.0.7")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "No ports recorded for this host yet")
}

func TestHandleScans_OK(t *testing.T) {
	now := time.Now()
	fin := now.Add(10 * time.Second)
	scans := &mockScanStore{scans: []*models.Scan{
		{ID: 1, Subnet: "192.168.0.0/24", StartedAt: now, FinishedAt: &fin, HostsFound: 5},
		{ID: 2, Subnet: "10.0.0.0/24", StartedAt: now},
	}}
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, scans)

	resp := get(t, srv, "/scans")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "192.168.0.0/24")
	assert.Contains(t, body, "10.0.0.0/24")
	assert.Contains(t, body, "done")
	assert.Contains(t, body, "in progress")
}

func TestHandleScans_Empty(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/scans")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "No scans recorded yet")
}

func TestHandleHosts_StoreError(t *testing.T) {
	hosts := &mockHostStore{err: errors.New("db exploded")}
	srv := newTestServer(t, hosts, &mockPortStore{}, &mockScanStore{})

	resp := get(t, srv, "/hosts")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestHandleDashboard_SurfacesLoadErrors(t *testing.T) {
	// All three backing queries fail: the dashboard must still render 200
	// (it's the operator's only window) but show an inline error banner.
	hosts := &mockHostStore{err: errors.New("hosts blew up")}
	scans := &mockScanStore{err: errors.New("scans blew up")}
	srv := newTestServer(t, hosts, &mockPortStore{}, scans)

	resp := get(t, srv, "/")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	assert.Contains(t, body, "Some data failed to load")
	assert.Contains(t, body, "host count")
	assert.Contains(t, body, "recent scans")
}

func TestSecurityHeaders_Present(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	resp := get(t, srv, "/")
	defer resp.Body.Close()
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", resp.Header.Get("Referrer-Policy"))
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
}

func TestHandleScans_StoreError(t *testing.T) {
	scans := &mockScanStore{err: errors.New("db gone")}
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, scans)

	resp := get(t, srv, "/scans")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestAllPages_ContentType(t *testing.T) {
	now := time.Now()
	hosts := &mockHostStore{hosts: []*models.Host{
		{ID: 1, IPAddress: "10.0.0.1", FirstSeen: now, LastSeen: now},
	}}
	srv := newTestServer(t, hosts, &mockPortStore{}, &mockScanStore{})

	paths := []string{"/", "/hosts", "/hosts/10.0.0.1", "/scans"}
	for _, p := range paths {
		resp := get(t, srv, p)
		_ = resp.Body.Close()
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html", "path %s", p)
	}
}

func TestServer_Shutdown(t *testing.T) {
	srv, err := admin.NewServer(":0", "agent", &mockHostStore{}, &mockPortStore{}, &mockScanStore{}, healthyStatus, nil)
	require.NoError(t, err)
	require.NoError(t, srv.Start())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))

	// after shutdown the address should still be recorded
	assert.NotEmpty(t, srv.Addr())
}

// --- test helper to avoid importing io/ioutil ---

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	req := httptest.NewRecorder()
	_ = req
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String()
}
