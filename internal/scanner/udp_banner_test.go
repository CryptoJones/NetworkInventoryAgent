package scanner

import (
	"context"
	"net"
	"testing"
	"time"
)

// startUDPResponder starts a UDP listener that replies to each datagram with
// handler(req). A nil handler return sends nothing (simulating silence).
func startUDPResponder(t *testing.T, handler func(req []byte) []byte) (host string, port int) {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if resp := handler(append([]byte(nil), buf[:n]...)); resp != nil {
				_, _ = pc.WriteTo(resp, addr)
			}
		}
	}()
	h, p, _ := net.SplitHostPort(pc.LocalAddr().String())
	return h, atoi(t, p)
}

func TestDNSFingerprint_Identified(t *testing.T) {
	// Echo the query back with the QR (response) bit set.
	host, port := startUDPResponder(t, func(req []byte) []byte {
		if len(req) < 12 {
			return nil
		}
		req[2] |= 0x80
		return req
	})
	got := dnsFingerprint(context.Background(), host, port, 500*time.Millisecond)
	if got != "DNS" {
		t.Errorf("got %q, want DNS", got)
	}
}

func TestDNSFingerprint_NotDNS(t *testing.T) {
	// Reply that's too short / lacks the QR bit → not identified.
	host, port := startUDPResponder(t, func([]byte) []byte { return []byte{0x00, 0x00} })
	got := dnsFingerprint(context.Background(), host, port, 500*time.Millisecond)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestNTPFingerprint_WithStratum(t *testing.T) {
	host, port := startUDPResponder(t, func([]byte) []byte {
		resp := make([]byte, 48)
		resp[0] = 0x1c // LI=0, VN=3, Mode=4 (server)
		resp[1] = 2    // stratum 2
		return resp
	})
	got := ntpFingerprint(context.Background(), host, port, 500*time.Millisecond)
	if got != "NTP (stratum 2)" {
		t.Errorf("got %q, want NTP (stratum 2)", got)
	}
}

func TestNTPFingerprint_NotServerMode(t *testing.T) {
	// Mode 3 (client) reply is not a server → not identified.
	host, port := startUDPResponder(t, func([]byte) []byte {
		resp := make([]byte, 48)
		resp[0] = 0x1b
		return resp
	})
	got := ntpFingerprint(context.Background(), host, port, 500*time.Millisecond)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestUDPFingerprint_UnknownPortIsEmpty(t *testing.T) {
	if got := udpFingerprint(context.Background(), "127.0.0.1", 9999, 50*time.Millisecond); got != "" {
		t.Errorf("got %q, want empty for unfingerprinted port", got)
	}
}

func TestUDPFingerprint_NoResponderTimesOut(t *testing.T) {
	// Nothing listening: the read should time out and yield "".
	if got := dnsFingerprint(context.Background(), "127.0.0.1", 53, 100*time.Millisecond); got != "" {
		t.Errorf("got %q, want empty when no DNS responder", got)
	}
}
