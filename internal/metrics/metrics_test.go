package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounterMonotonic(t *testing.T) {
	c := &Counter{}
	c.Inc()
	c.Add(4)
	c.Add(-99) // ignored
	if got := c.Get(); got != 5 {
		t.Fatalf("want 5, got %d", got)
	}
}

func TestGaugeSet(t *testing.T) {
	g := &Gauge{}
	g.Set(7)
	g.Inc()
	g.Dec()
	g.Dec()
	if got := g.Get(); got != 6 {
		t.Fatalf("want 6, got %d", got)
	}
}

func TestHandlerExposesRegistered(t *testing.T) {
	r := NewRegistry()
	r.Counter("test_total", "a test counter").Inc()
	r.Gauge("test_value", "a test gauge").Set(42)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		"# HELP test_total a test counter",
		"# TYPE test_total counter",
		"test_total 1",
		"# HELP test_value a test gauge",
		"# TYPE test_value gauge",
		"test_value 42",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in output:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("want text/plain content-type, got %q", ct)
	}
}

func TestCounterReuseSameInstance(t *testing.T) {
	r := NewRegistry()
	c1 := r.Counter("dup_total", "first help")
	c2 := r.Counter("dup_total", "second help")
	if c1 != c2 {
		t.Fatal("repeated Counter() with same name must return the same instance")
	}
}
