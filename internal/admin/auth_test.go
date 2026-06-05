package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
)

const testAdminToken = "s3cr3t-admin-token"

// newAuthedServer starts an admin server gated by testAdminToken. A non-nil
// trigger is wired so POST /scan exercises the auth gate (which must run before
// the CSRF check).
func newAuthedServer(t *testing.T) *admin.Server {
	t.Helper()
	srv, err := admin.NewServer(":0", "test-agent",
		&mockHostStore{}, &mockPortStore{}, &mockScanStore{},
		healthyStatus, func() bool { return true },
		admin.ServerOptions{AuthToken: testAdminToken},
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

// doReq issues a request to the server with optional mutation (headers/auth).
func doReq(t *testing.T, srv *admin.Server, method, path string, mut func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+srv.Addr()+path, nil)
	require.NoError(t, err)
	if mut != nil {
		mut(req)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func TestAuth_NoCredentials_401WithChallenge(t *testing.T) {
	srv := newAuthedServer(t)
	resp := doReq(t, srv, http.MethodGet, "/", nil)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Basic")
}

func TestAuth_CorrectBearer_200(t *testing.T) {
	srv := newAuthedServer(t)
	resp := doReq(t, srv, http.MethodGet, "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+testAdminToken)
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_CorrectBasicPassword_200(t *testing.T) {
	srv := newAuthedServer(t)
	resp := doReq(t, srv, http.MethodGet, "/", func(r *http.Request) {
		r.SetBasicAuth("anyuser", testAdminToken)
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAuth_WrongBearer_401(t *testing.T) {
	srv := newAuthedServer(t)
	resp := doReq(t, srv, http.MethodGet, "/", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer wrong")
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAuth_WrongBasicPassword_401(t *testing.T) {
	srv := newAuthedServer(t)
	resp := doReq(t, srv, http.MethodGet, "/", func(r *http.Request) {
		r.SetBasicAuth("anyuser", "wrong")
	})
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// Export and the scan trigger must be behind the same gate. The unauthenticated
// POST /scan must be rejected by auth (401) before CSRF runs (403).
func TestAuth_GatesExportAndScanTrigger(t *testing.T) {
	srv := newAuthedServer(t)

	exp := doReq(t, srv, http.MethodGet, "/export.json", nil)
	_ = exp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, exp.StatusCode, "export must require auth")

	scan := doReq(t, srv, http.MethodPost, "/scan", nil)
	_ = scan.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, scan.StatusCode, "auth must precede CSRF on POST /scan")
}

// Regression guard: the loopback default (no token) stays credential-free.
func TestAuth_EmptyToken_NoGate(t *testing.T) {
	srv := newTestServer(t, &mockHostStore{}, &mockPortStore{}, &mockScanStore{})
	resp := doReq(t, srv, http.MethodGet, "/", nil)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
