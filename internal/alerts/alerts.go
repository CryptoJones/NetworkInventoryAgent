// Package alerts dispatches host-level inventory change events to one or
// more configured sinks (HTTP webhook, syslog). It is intentionally fire-
// and-forget — sinks log their failures and the agent keeps scanning,
// because an unreachable webhook should not stop inventory collection.
//
// Event sources today:
//
//	HostDiscovered  — IP appeared in the post-cycle host list but not
//	                  the pre-cycle one. Includes the host's IP plus
//	                  any enrichment fields (Hostname, Vendor,
//	                  DeviceType) that landed during the same cycle.
//	HostVanished    — IP was in the pre-cycle list but not after the
//	                  cycle (TTL prune deleted it, or it was removed
//	                  some other way). Includes the last-known
//	                  enrichment so the alert payload is useful even
//	                  though the host record is already gone.
//
// Port-level events (PortAppeared/PortVanished) and watchdog events are
// intentionally out of scope for the initial release — the host-level
// events satisfy the headline "something new appeared on my network"
// use case, which is what operators ask for first.
package alerts

import (
	"context"
	"log/slog"
	"time"
)

// EventType is a stable string identifier suitable for webhook routing.
// Values match the JSON wire form ("host.discovered", not "HostDiscovered")
// so downstream consumers can switch on them without case folding.
type EventType string

const (
	HostDiscovered EventType = "host.discovered"
	HostVanished   EventType = "host.vanished"
)

// Event is the wire shape sent to every sink. Fields are JSON-tagged so
// the same struct serialises directly into the webhook body and into the
// MSG portion of an RFC 5424 syslog line.
type Event struct {
	Type       EventType `json:"type"`
	IP         string    `json:"ip"`
	Hostname   string    `json:"hostname,omitempty"`
	MACAddress string    `json:"mac_address,omitempty"`
	Vendor     string    `json:"vendor,omitempty"`
	DeviceType string    `json:"device_type,omitempty"`
	Time       time.Time `json:"time"`
	Agent      string    `json:"agent"`
}

// Emitter is the surface the agent depends on. Multiplexer is the
// production implementation; tests use a recording fake.
type Emitter interface {
	Emit(ctx context.Context, ev Event)
}

// Sink is one downstream destination. Implementations live next to this
// file (webhook.go, syslog.go); third-party additions just satisfy the
// interface.
type Sink interface {
	// Emit delivers the event. A non-nil error gets logged but does not
	// propagate further — the Multiplexer always tries the remaining
	// sinks even when one fails.
	Emit(ctx context.Context, ev Event) error
	// Name identifies the sink in failure logs (e.g. "webhook",
	// "syslog"). No need for uniqueness across instances; it's a
	// debugging string, not a key.
	Name() string
	// Close is called when the agent shuts down.
	Close() error
}

// Multiplexer fans an event out to every configured sink. The zero value
// is valid and emits nowhere — useful for "alerts disabled" deployments.
type Multiplexer struct {
	sinks []Sink
}

// NewMultiplexer wraps a slice of sinks. nil entries are skipped so
// callers can pass `[]Sink{NewWebhookSink(...), NewSyslogSink(...)}`
// and either constructor returning nil silently drops it.
func NewMultiplexer(sinks ...Sink) *Multiplexer {
	m := &Multiplexer{}
	for _, s := range sinks {
		if s != nil {
			m.sinks = append(m.sinks, s)
		}
	}
	return m
}

// Emit delivers ev to every sink in parallel. Sink failures are logged
// but do not cancel sibling deliveries — a flapping webhook must not
// suppress a syslog audit trail.
func (m *Multiplexer) Emit(ctx context.Context, ev Event) {
	for _, s := range m.sinks {
		go func(sink Sink) {
			if err := sink.Emit(ctx, ev); err != nil {
				slog.Warn("alert sink error",
					"sink", sink.Name(),
					"event", string(ev.Type),
					"ip", ev.IP,
					"err", err)
			}
		}(s)
	}
}

// Close flushes every sink. Errors are logged but a failing close on one
// sink doesn't skip the others.
func (m *Multiplexer) Close() {
	for _, s := range m.sinks {
		if err := s.Close(); err != nil {
			slog.Warn("alert sink close error", "sink", s.Name(), "err", err)
		}
	}
}

// noopEmitter is what the agent gets when alerts are unconfigured. Avoids
// nil checks at every Emit call site.
type noopEmitter struct{}

func (noopEmitter) Emit(context.Context, Event) {}

// NoopEmitter returns an Emitter that drops every event. Use this rather
// than passing nil to the agent constructor.
func NoopEmitter() Emitter { return noopEmitter{} }
