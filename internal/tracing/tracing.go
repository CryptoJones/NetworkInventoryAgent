// Package tracing initialises a process-global OpenTelemetry TracerProvider
// and exposes thin helpers for wrapping HTTP handlers and clients.
//
// The agent does HTTP-in (admin + health), HTTP-out (watchdog), and DB I/O.
// Tracing is cheap to wire and pays off the first time something is slow,
// so we ship it default-on with an OTLP/HTTP exporter pointed at the
// standard OTEL_EXPORTER_OTLP_ENDPOINT env var. When the env var is empty
// the SDK uses a no-op exporter — spans are created but discarded, so the
// instrumentation overhead is negligible (one allocation per span).
//
// The package is deliberately small. It does not expose otel internals;
// callers wrap handlers with HTTPMiddleware and clients with HTTPClient,
// and that's the whole surface.
package tracing

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup installs a global TracerProvider with an OTLP/HTTP exporter and the
// W3C TraceContext propagator. serviceName is recorded as the otel resource's
// service.name attribute; endpoint, when empty, lets the SDK pick from
// OTEL_EXPORTER_OTLP_ENDPOINT (or run as a no-op). Returns a shutdown func
// that flushes pending spans; callers must invoke it on agent stop.
func Setup(ctx context.Context, serviceName, endpoint string) (func(context.Context) error, error) {
	opts := []otlptracehttp.Option{}
	if endpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpointURL(endpoint))
	}
	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// HTTPMiddleware wraps an http.Handler with otelhttp's span-producing
// middleware. Each request becomes a server span named after operation.
func HTTPMiddleware(operation string, next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, operation)
}

// HTTPClient returns an *http.Client whose Transport produces client spans
// for every request, propagating the active context's trace headers
// downstream. base may be nil; defaults to http.DefaultTransport.
func HTTPClient(base http.RoundTripper) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: otelhttp.NewTransport(base)}
}
