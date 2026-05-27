package alerts_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/alerts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMultiplexer_FansOutToAllSinks(t *testing.T) {
	a := &recordingSink{name: "a"}
	b := &recordingSink{name: "b"}
	mux := alerts.NewMultiplexer(a, b)

	ev := alerts.Event{Type: alerts.HostDiscovered, IP: "10.0.0.1", Time: time.Now()}
	mux.Emit(context.Background(), ev)

	// Multiplexer emits in goroutines — give them a beat.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if a.count.Load() > 0 && b.count.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), a.count.Load())
	assert.Equal(t, int32(1), b.count.Load())
}

func TestMultiplexer_SinkErrorDoesNotStopOthers(t *testing.T) {
	bad := &recordingSink{name: "bad", err: io.ErrUnexpectedEOF}
	good := &recordingSink{name: "good"}
	mux := alerts.NewMultiplexer(bad, good)

	mux.Emit(context.Background(), alerts.Event{Type: alerts.HostDiscovered, IP: "10.0.0.1"})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if good.count.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int32(1), good.count.Load(), "a sibling sink must still receive the event")
}

func TestNoopEmitter_DoesNotPanic(t *testing.T) {
	e := alerts.NoopEmitter()
	e.Emit(context.Background(), alerts.Event{})
}

func TestWebhookSink_PostsEvent(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
		auth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ = io.ReadAll(r.Body)
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := alerts.NewWebhookSink(srv.URL, "Bearer s3cret")
	require.NotNil(t, sink)
	err := sink.Emit(context.Background(), alerts.Event{
		Type: alerts.HostDiscovered,
		IP:   "10.0.0.5",
		Time: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "Bearer s3cret", auth, "auth header must round-trip")
	var got alerts.Event
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, alerts.HostDiscovered, got.Type)
	assert.Equal(t, "10.0.0.5", got.IP)
}

func TestWebhookSink_RetriesOn500(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := alerts.NewWebhookSink(srv.URL, "")
	err := sink.Emit(context.Background(), alerts.Event{IP: "10.0.0.5"})
	require.NoError(t, err)
	assert.Equal(t, int32(2), hits.Load(), "must retry once on 5xx")
}

func TestWebhookSink_NoRetryOn400(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	sink := alerts.NewWebhookSink(srv.URL, "")
	err := sink.Emit(context.Background(), alerts.Event{IP: "10.0.0.5"})
	require.Error(t, err)
	assert.Equal(t, int32(1), hits.Load(), "4xx must not be retried")
}

func TestWebhookSink_NilOnEmptyURL(t *testing.T) {
	assert.Nil(t, alerts.NewWebhookSink("", ""))
}

func TestSyslogSink_FormatsRFC5424OverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer pc.Close()

	addr := "udp://" + pc.LocalAddr().String()
	sink, err := alerts.NewSyslogSink(addr, "test-app", 16)
	require.NoError(t, err)
	defer func() { _ = sink.Close() }()

	go func() {
		_ = sink.Emit(context.Background(), alerts.Event{
			Type: alerts.HostDiscovered,
			IP:   "10.0.0.5",
			Time: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
		})
	}()

	buf := make([]byte, 1500)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	require.NoError(t, err)
	line := string(buf[:n])

	assert.True(t, strings.HasPrefix(line, "<134>1 "),
		"facility 16 + severity 6 = PRI 134; got %q", line[:min(len(line), 20)])
	assert.Contains(t, line, "test-app", "tag must appear in APP-NAME field")
	assert.Contains(t, line, "host.discovered", "MSGID must be the event type")
	assert.Contains(t, line, `"ip":"10.0.0.5"`, "MSG should carry the event JSON")
}

func TestSyslogSink_RejectsBadScheme(t *testing.T) {
	_, err := alerts.NewSyslogSink("file:///tmp/syslog", "tag", 16)
	assert.Error(t, err)
}

func TestSyslogSink_NilOnEmptyAddr(t *testing.T) {
	s, err := alerts.NewSyslogSink("", "tag", 16)
	require.NoError(t, err)
	assert.Nil(t, s)
}

// --- helpers ---

type recordingSink struct {
	name  string
	err   error
	count atomic.Int32
}

func (r *recordingSink) Emit(_ context.Context, _ alerts.Event) error {
	r.count.Add(1)
	return r.err
}
func (r *recordingSink) Name() string { return r.name }
func (*recordingSink) Close() error   { return nil }
