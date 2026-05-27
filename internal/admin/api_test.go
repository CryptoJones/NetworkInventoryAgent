package admin_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// fixtureHosts returns a small inventory exercising every filter dimension.
func fixtureHosts() []*models.Host {
	now := time.Now()
	return []*models.Host{
		{ID: 1, IPAddress: "10.0.0.10", Hostname: "router.lan", Vendor: "MikroTik", DeviceType: "router", LastSeen: now},
		{ID: 2, IPAddress: "10.0.0.20", Hostname: "printer.lan", Vendor: "HP", DeviceType: "printer", LastSeen: now},
		{ID: 3, IPAddress: "10.0.1.30", Hostname: "db.lan", Vendor: "", DeviceType: "database (postgres)", LastSeen: now},
		{ID: 4, IPAddress: "10.0.1.40", Hostname: "build-server.lan", Vendor: "", DeviceType: "linux-host", LastSeen: now},
		{ID: 5, IPAddress: "192.168.1.5", Hostname: "guest-laptop", Vendor: "Apple", DeviceType: "", LastSeen: now},
	}
}

func fixturePorts() []*models.Port {
	return []*models.Port{
		// Host 1: SSH + 8728 (mikrotik api)
		{HostID: 1, Number: 22, Protocol: models.TCP, State: models.StateOpen},
		{HostID: 1, Number: 8728, Protocol: models.TCP, State: models.StateOpen},
		// Host 2: 9100 (printer)
		{HostID: 2, Number: 9100, Protocol: models.TCP, State: models.StateOpen},
		// Host 3: 5432 postgres
		{HostID: 3, Number: 5432, Protocol: models.TCP, State: models.StateOpen},
		// Host 4: 22 ssh
		{HostID: 4, Number: 22, Protocol: models.TCP, State: models.StateOpen},
		// Host 5: no ports
	}
}

// decodeJSON reads the body but leaves closing to the caller — bodyclose
// lint can't trace closes through this helper, so callers `defer
// resp.Body.Close()` at the call site.
func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(body, out), "body: %s", body)
}

func TestAPIHosts_UnfilteredListsAll(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Total  int            `json:"total"`
		Limit  int            `json:"limit"`
		Offset int            `json:"offset"`
		Hosts  []*models.Host `json:"hosts"`
	}
	decodeJSON(t, resp, &body)
	assert.Equal(t, 5, body.Total)
	assert.Equal(t, 100, body.Limit)
	assert.Len(t, body.Hosts, 5)
}

func TestAPIHosts_VendorFilter(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?vendor=MikroTik")
	defer func() { _ = resp.Body.Close() }()
	var body struct{ Total int }
	decodeJSON(t, resp, &body)
	assert.Equal(t, 1, body.Total)
}

func TestAPIHosts_DeviceTypeFilter(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?device_type=printer")
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Total int
		Hosts []*models.Host
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, 1, body.Total)
	assert.Equal(t, "10.0.0.20", body.Hosts[0].IPAddress)
}

func TestAPIHosts_HostnameSubstringCaseInsensitive(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?hostname=LAN")
	defer func() { _ = resp.Body.Close() }()
	var body struct{ Total int }
	decodeJSON(t, resp, &body)
	// router.lan + printer.lan + db.lan + build-server.lan = 4
	assert.Equal(t, 4, body.Total)
}

func TestAPIHosts_SubnetFilter(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?subnet=10.0.0.0/24")
	defer func() { _ = resp.Body.Close() }()
	var body struct{ Total int }
	decodeJSON(t, resp, &body)
	assert.Equal(t, 2, body.Total, "only 10.0.0.10 and 10.0.0.20 are in 10.0.0.0/24")
}

func TestAPIHosts_PortFilter(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?port=22")
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Total int
		Hosts []*models.Host
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, 2, body.Total, "hosts 1 and 4 have port 22 open")
	gotIPs := map[string]bool{body.Hosts[0].IPAddress: true, body.Hosts[1].IPAddress: true}
	assert.True(t, gotIPs["10.0.0.10"])
	assert.True(t, gotIPs["10.0.1.40"])
}

func TestAPIHosts_CombinedFilters(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	// All three filters must match for a host to appear:
	// subnet 10.0.0.0/16 + device_type linux-host + port 22 → host 4 only
	resp := get(t, srv, "/api/v1/hosts?subnet=10.0.0.0/16&device_type=linux-host&port=22")
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Total int
		Hosts []*models.Host
	}
	decodeJSON(t, resp, &body)
	require.Equal(t, 1, body.Total)
	assert.Equal(t, "10.0.1.40", body.Hosts[0].IPAddress)
}

func TestAPIHosts_Pagination(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?limit=2&offset=2")
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Total  int
		Limit  int
		Offset int
		Hosts  []*models.Host
	}
	decodeJSON(t, resp, &body)
	assert.Equal(t, 5, body.Total, "total is the pre-pagination count")
	assert.Equal(t, 2, body.Limit)
	assert.Equal(t, 2, body.Offset)
	assert.Len(t, body.Hosts, 2)
}

func TestAPIHosts_PaginationOffsetPastTotal(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts?offset=999")
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Total int
		Hosts []*models.Host
	}
	decodeJSON(t, resp, &body)
	assert.Equal(t, 5, body.Total)
	assert.Len(t, body.Hosts, 0, "offset past total should empty the slice, not 500")
}

func TestAPIHosts_InvalidSubnetReturns400(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	resp := get(t, srv, "/api/v1/hosts?subnet=not-a-cidr")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()
}

func TestAPIHosts_InvalidPortReturns400(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	resp := get(t, srv, "/api/v1/hosts?port=99999")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	_ = resp.Body.Close()
}

func TestAPIHostDetail_OK(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{ports: fixturePorts()},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts/10.0.0.10")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Host  *models.Host
		Ports []*models.Port
	}
	decodeJSON(t, resp, &body)
	require.NotNil(t, body.Host)
	assert.Equal(t, "10.0.0.10", body.Host.IPAddress)
	assert.Len(t, body.Ports, 2, "host 1 has SSH + 8728")
}

func TestAPIHostDetail_NotFound(t *testing.T) {
	srv := newTestServer(t,
		&mockHostStore{hosts: fixtureHosts()},
		&mockPortStore{},
		&mockScanStore{},
	)
	resp := get(t, srv, "/api/v1/hosts/192.168.99.99")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	_ = resp.Body.Close()
}
