package admin_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
)

// newServerWithTrigger starts an admin server wired with the given trigger.
func newServerWithTrigger(t *testing.T, trigger admin.Trigger, status func() health.Status) *admin.Server {
	t.Helper()
	srv, err := admin.NewServer(":0", "test-agent",
		&mockHostStore{}, &mockPortStore{}, &mockScanStore{},
		status, trigger, admin.ServerOptions{},
	)
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// scrapeCSRF GETs the dashboard and extracts the embedded CSRF token.
func scrapeCSRF(t *testing.T, srv *admin.Server) string {
	t.Helper()
	resp := get(t, srv, "/")
	defer func() { _ = resp.Body.Close() }()
	m := csrfRe.FindStringSubmatch(readBody(t, resp))
	require.Len(t, m, 2, "dashboard should embed a CSRF token")
	return m[1]
}

func postScan(t *testing.T, srv *admin.Server, csrf string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/scan", nil)
	require.NoError(t, err)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestHandleWatchdog_NoPeer(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	resp := get(t, srv, "/watchdog")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestHandleWatchdog_WithPeer(t *testing.T) {
	status := func() health.Status {
		return health.Status{
			Name:    "test-agent",
			Healthy: true,
			Peer: &health.PeerStatus{
				Addr:          "http://neuromancer:8081",
				Reachable:     true,
				LastCheckedAt: time.Now(),
				PeerHostCount: 12,
			},
		}
	}
	srv := newServerWithTrigger(t, nil, status)
	resp := get(t, srv, "/watchdog")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, readBody(t, resp), "neuromancer")
}

func TestHandleScanTrigger_NotWired(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{}) // nil trigger
	resp := postScan(t, srv, scrapeCSRF(t, srv))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode)
}

func TestHandleScanTrigger_Success(t *testing.T) {
	srv := newServerWithTrigger(t, func() bool { return true }, healthyStatus)
	resp := postScan(t, srv, scrapeCSRF(t, srv))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestHandleScanTrigger_AlreadyPending(t *testing.T) {
	srv := newServerWithTrigger(t, func() bool { return false }, healthyStatus)
	resp := postScan(t, srv, scrapeCSRF(t, srv))
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestHandleScanTrigger_MissingCSRF(t *testing.T) {
	srv := newServerWithTrigger(t, func() bool { return true }, healthyStatus)
	resp := postScan(t, srv, "") // no token
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "POST without CSRF token must be rejected")
}
