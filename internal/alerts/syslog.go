package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// SyslogSink sends RFC 5424 syslog lines over UDP or TCP. Stdlib
// `log/syslog` only exists on Unix, but the agent runs on Windows too, so
// the protocol is hand-rolled here (it's tiny — one printf-style line).
//
// One persistent connection per sink instance (re-dialled on write
// failure). Best-effort: events are dropped when the receiver is
// unreachable, with the failure surfacing in the Multiplexer's slog
// output. Syslog is fire-and-forget by design; we honour that.
type SyslogSink struct {
	addr     string
	scheme   string // "udp" or "tcp"
	tag      string
	facility int
	hostname string

	mu   sync.Mutex
	conn net.Conn
}

// NewSyslogSink parses addr as a URL ("udp://syslog.example:514" or
// "tcp://..."), establishes the first connection, and returns the sink.
// Returns nil when addr is empty so wiring stays guard-free at the call
// site.
//
// facility is the RFC 5424 facility number (0–23). Default 16 (local0).
// tag is the APP-NAME field; default "network-inventory".
func NewSyslogSink(addr, tag string, facility int) (*SyslogSink, error) {
	if addr == "" {
		return nil, nil
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("parse syslog addr %q: %w", addr, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "udp" && scheme != "tcp" {
		return nil, fmt.Errorf("syslog scheme %q not supported; use udp:// or tcp://", scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("syslog addr missing host:port: %q", addr)
	}
	if tag == "" {
		tag = "network-inventory"
	}
	if facility < 0 || facility > 23 {
		facility = 16 // local0
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "-"
	}
	s := &SyslogSink{
		addr:     u.Host,
		scheme:   scheme,
		tag:      tag,
		facility: facility,
		hostname: hostname,
	}
	// Open the first connection eagerly so config errors surface at
	// boot, not on the first event.
	if err := s.dial(); err != nil {
		return nil, fmt.Errorf("dial syslog %s://%s: %w", scheme, u.Host, err)
	}
	return s, nil
}

// Name implements Sink.
func (*SyslogSink) Name() string { return "syslog" }

// Emit writes one RFC 5424 line. On a write failure the connection is
// closed and re-dialled once; if the redial succeeds the line is retried.
func (s *SyslogSink) Emit(ctx context.Context, ev Event) error {
	line, err := s.format(ev)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Honour ctx deadline if it's tighter than our default.
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Second)
	}
	if s.conn != nil {
		_ = s.conn.SetWriteDeadline(deadline)
		if _, err := s.conn.Write([]byte(line)); err == nil {
			return nil
		}
		_ = s.conn.Close()
		s.conn = nil
	}
	if err := s.dial(); err != nil {
		return err
	}
	_ = s.conn.SetWriteDeadline(deadline)
	_, err = s.conn.Write([]byte(line))
	return err
}

// Close releases the underlying socket.
func (s *SyslogSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		return err
	}
	return nil
}

func (s *SyslogSink) dial() error {
	c, err := net.DialTimeout(s.scheme, s.addr, 2*time.Second)
	if err != nil {
		return err
	}
	s.conn = c
	return nil
}

// format builds an RFC 5424 line:
//
//	<PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID - MSG\n
//
// MSG is the JSON-encoded Event, which lets downstream syslog parsers
// (rsyslog mmjsonparse, syslog-ng's json-parser, Splunk INDEXED_EXTRACTIONS)
// pull the same fields the webhook receiver gets, no separate format.
func (s *SyslogSink) format(ev Event) (string, error) {
	const severityInfo = 6 // Informational
	pri := s.facility*8 + severityInfo
	ts := ev.Time.UTC().Format(time.RFC3339Nano)

	body, err := json.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("marshal event: %w", err)
	}
	procid := os.Getpid()
	msgid := string(ev.Type)
	return fmt.Sprintf("<%d>1 %s %s %s %d %s - %s\n",
		pri, ts, s.hostname, s.tag, procid, msgid, body), nil
}
