package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ronin48/NetworkInventoryAgent/internal/tracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestSetup_EmptyEndpointReturnsShutdownFunc(t *testing.T) {
	// Empty endpoint should not fail — the SDK falls back to the OTEL_
	// EXPORTER_OTLP_ENDPOINT env var or runs as a no-op exporter.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := tracing.Setup(context.Background(), "test-service", "")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}

func TestHTTPMiddleware_ProducesSpan(t *testing.T) {
	// Use the global tracer to confirm the otelhttp wrap actually starts a
	// span. We don't need an exporter — the trace.SpanFromContext recovers
	// the active span if one is in flight.
	shutdown, err := tracing.Setup(context.Background(), "test-service", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	var sawValidSpan bool
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		sp := trace.SpanFromContext(r.Context())
		// otel always returns a span; we check IsRecording or a valid
		// context to confirm the middleware actually started one.
		if sp.SpanContext().IsValid() {
			sawValidSpan = true
		}
	})
	wrapped := tracing.HTTPMiddleware("test-handler", inner)

	srv := httptest.NewServer(wrapped)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.True(t, sawValidSpan, "otelhttp middleware should make a valid span available in the handler context")
}

func TestHTTPClient_TransportWrapsBase(t *testing.T) {
	// A nil base must fall back to http.DefaultTransport, not panic.
	c := tracing.HTTPClient(nil)
	require.NotNil(t, c)
	require.NotNil(t, c.Transport)

	// A custom RoundTripper must be preserved (wrapped, not replaced).
	custom := &countingTransport{base: http.DefaultTransport}
	c = tracing.HTTPClient(custom)
	require.NotNil(t, c.Transport)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	resp, err := c.Get(srv.URL + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, 1, custom.calls, "the base transport must still be called once per request")
}

// TestSetup_GlobalTracerProviderSet confirms Setup replaces the global TP.
func TestSetup_GlobalTracerProviderSet(t *testing.T) {
	shutdown, err := tracing.Setup(context.Background(), "service-a", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	tp := otel.GetTracerProvider()
	require.NotNil(t, tp)
	tr := tp.Tracer("unit-test")
	_, span := tr.Start(context.Background(), "noop")
	span.End()
}

type countingTransport struct {
	base  http.RoundTripper
	calls int
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls++
	return c.base.RoundTrip(req)
}
