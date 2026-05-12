package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/web"
	"github.com/Ronin48/NetworkInventoryAgent/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHostStore implements store.HostStore for tests.
type mockHostStore struct {
	hosts map[int64]*models.Host
	byIP  map[string]*models.Host
}

func newMockHostStore() *mockHostStore {
	return &mockHostStore{
		hosts: make(map[int64]*models.Host),
		byIP:  make(map[string]*models.Host),
	}
}

func (m *mockHostStore) Upsert(ctx context.Context, h *models.Host) (int64, error) {
	if h.ID == 0 {
		h.ID = int64(len(m.hosts) + 1)
	}
	m.hosts[h.ID] = h
	m.byIP[h.IPAddress] = h
	return h.ID, nil
}

func (m *mockHostStore) GetByIP(ctx context.Context, ip string) (*models.Host, error) {
	if h, ok := m.byIP[ip]; ok {
		return h, nil
	}
	return nil, errNotFound
}

func (m *mockHostStore) List(ctx context.Context) ([]*models.Host, error) {
	var result []*models.Host
	for _, h := range m.hosts {
		result = append(result, h)
	}
	return result, nil
}

func (m *mockHostStore) Count(ctx context.Context) (int, error) {
	return len(m.hosts), nil
}

func (m *mockHostStore) Delete(ctx context.Context, id int64) error {
	h, ok := m.hosts[id]
	if !ok {
		return errNotFound
	}
	delete(m.hosts, id)
	delete(m.byIP, h.IPAddress)
	return nil
}

// mockPortStore implements store.PortStore for tests.
type mockPortStore struct {
	ports map[int64][]*models.Port
}

func newMockPortStore() *mockPortStore {
	return &mockPortStore{ports: make(map[int64][]*models.Port)}
}

func (m *mockPortStore) Upsert(ctx context.Context, p *models.Port) error {
	m.ports[p.HostID] = append(m.ports[p.HostID], p)
	return nil
}

func (m *mockPortStore) ListByHost(ctx context.Context, hostID int64) ([]*models.Port, error) {
	return m.ports[hostID], nil
}

func (m *mockPortStore) DeleteByHost(ctx context.Context, hostID int64) error {
	delete(m.ports, hostID)
	return nil
}

// mockScanStore implements store.ScanStore for tests.
type mockScanStore struct {
	scans []*models.Scan
}

func newMockScanStore() *mockScanStore {
	return &mockScanStore{}
}

func (m *mockScanStore) Create(ctx context.Context, s *models.Scan) (int64, error) {
	s.ID = int64(len(m.scans) + 1)
	m.scans = append(m.scans, s)
	return s.ID, nil
}

func (m *mockScanStore) Finish(ctx context.Context, id int64, hostsFound int, finishedAt time.Time) error {
	for _, s := range m.scans {
		if s.ID == id {
			s.HostsFound = hostsFound
			s.FinishedAt = &finishedAt
			return nil
		}
	}
	return errNotFound
}

func (m *mockScanStore) List(ctx context.Context) ([]*models.Scan, error) {
	// Return newest first
	result := make([]*models.Scan, len(m.scans))
	copy(result, m.scans)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result, nil
}

var errNotFound = fmt.Errorf("not found")

// --- tests ---

func TestAPIHosts_returnsJSON(t *testing.T) {
	hosts := newMockHostStore()
	hosts.Upsert(t.Context(), &models.Host{ID: 1, IPAddress: "10.0.0.1"})
	hosts.Upsert(t.Context(), &models.Host{ID: 2, IPAddress: "10.0.0.2"})

	tracker := health.NewTracker("test-agent")
	srv := web.NewServer("127.0.0.1:0", hosts, newMockPortStore(), newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hosts", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []*models.Host
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestAPIScans_returnsJSON(t *testing.T) {
	scans := newMockScanStore()
	now := time.Now().UTC()
	scans.Create(t.Context(), &models.Scan{Subnet: "10.0.0.0/24", StartedAt: now})

	tracker := health.NewTracker("test-agent")
	srv := web.NewServer("127.0.0.1:0", newMockHostStore(), newMockPortStore(), scans, tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/scans", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp []*models.Scan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "10.0.0.0/24", resp[0].Subnet)
}

func TestAPIStatus_returnsTrackerData(t *testing.T) {
	tracker := health.NewTracker("test-agent")
	tracker.SetHostCount(42)
	tracker.RecordScan()

	srv := web.NewServer("127.0.0.1:0", newMockHostStore(), newMockPortStore(), newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var status health.Status
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &status))
	assert.Equal(t, "test-agent", status.Name)
	assert.Equal(t, 42, status.HostCount)
	assert.Equal(t, 1, status.ScanCount)
}

func TestAPIHostByID_notFoundReturns404(t *testing.T) {
	tracker := health.NewTracker("test-agent")
	srv := web.NewServer("127.0.0.1:0", newMockHostStore(), newMockPortStore(), newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/999", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIHostByID_returnsHostWithPorts(t *testing.T) {
	hosts := newMockHostStore()
	hostID, _ := hosts.Upsert(t.Context(), &models.Host{ID: 1, IPAddress: "10.0.0.1"})

	ports := newMockPortStore()
	ports.Upsert(t.Context(), &models.Port{HostID: hostID, Number: 80, Protocol: "tcp"})

	tracker := health.NewTracker("test-agent")
	srv := web.NewServer("127.0.0.1:0", hosts, ports, newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hosts/10.0.0.1", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var host models.Host
	require.NoError(t, json.Unmarshal(resp["host"], &host))
	assert.Equal(t, "10.0.0.1", host.IPAddress)

	var portList []*models.Port
	require.NoError(t, json.Unmarshal(resp["ports"], &portList))
	assert.Len(t, portList, 1)
	assert.Equal(t, 80, portList[0].Number)
}

func TestDashboardHTML_rendersWithoutError(t *testing.T) {
	hosts := newMockHostStore()
	hosts.Upsert(t.Context(), &models.Host{ID: 1, IPAddress: "10.0.0.1"})

	tracker := health.NewTracker("wintermute")
	srv := web.NewServer("127.0.0.1:0", hosts, newMockPortStore(), newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "wintermute")
	assert.Contains(t, w.Body.String(), "10.0.0.1")
}

func TestDashboardHTML_showsNoHosts(t *testing.T) {
	tracker := health.NewTracker("neuromancer")
	srv := web.NewServer("127.0.0.1:0", newMockHostStore(), newMockPortStore(), newMockScanStore(), tracker)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "neuromancer")
	assert.NotContains(t, w.Body.String(), "10.0.0")
}
