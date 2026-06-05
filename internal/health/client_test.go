package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
)

func TestClient_Ping_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, health.NewClient(srv.URL).Ping(context.Background()))
}

func TestClient_Ping_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := health.NewClient(srv.URL).Ping(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestClient_Ping_SendsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	require.NoError(t, health.NewAuthedClient(srv.URL, "tok").Ping(context.Background()))
	assert.Equal(t, "Bearer tok", gotAuth)
}

func TestClient_Ping_ConnError(t *testing.T) {
	// Nothing is listening on this address; Do() should fail.
	err := health.NewClient("http://127.0.0.1:1").Ping(context.Background())
	require.Error(t, err)
}

func TestClient_FetchStatus_OK(t *testing.T) {
	want := health.Status{Name: "peer", Healthy: true, HostCount: 7, ScanCount: 3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/status", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	got, err := health.NewClient(srv.URL).FetchStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "peer", got.Name)
	assert.True(t, got.Healthy)
	assert.Equal(t, 7, got.HostCount)
	assert.Equal(t, 3, got.ScanCount)
}

func TestClient_FetchStatus_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := health.NewClient(srv.URL).FetchStatus(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode status")
}
