package scanner

import (
	"context"
	"net"
	"strconv"
	"time"
)

// udpFingerprint sends a protocol-specific probe for well-known UDP ports
// and returns a Service label, or "" when the port isn't one we fingerprint
// or the peer didn't answer recognisably. Called only after the port is
// already known open (see udpScan), so the extra packet is cheap.
func udpFingerprint(ctx context.Context, ip string, port int, timeout time.Duration) string {
	switch port {
	case 53:
		return dnsFingerprint(ctx, ip, port, timeout)
	case 123:
		return ntpFingerprint(ctx, ip, port, timeout)
	default:
		return ""
	}
}

// dnsFingerprint sends a standard DNS query (A record for the root, with a
// fixed transaction ID) and confirms the reply is a DNS response: a payload
// of at least the 12-byte header, the QR bit set, and the transaction ID
// echoed back. That identifies the service without depending on the answer
// contents (REFUSED still proves it's DNS).
func dnsFingerprint(ctx context.Context, ip string, port int, timeout time.Duration) string {
	const id0, id1 = 0x13, 0x37
	query := []byte{
		id0, id1, // transaction ID
		0x01, 0x00, // flags: RD=1
		0x00, 0x01, // QDCOUNT=1
		0x00, 0x00, // ANCOUNT
		0x00, 0x00, // NSCOUNT
		0x00, 0x00, // ARCOUNT
		0x00,       // QNAME = root (empty label)
		0x00, 0x01, // QTYPE=A
		0x00, 0x01, // QCLASS=IN
	}
	resp, ok := udpExchange(ctx, ip, port, timeout, query, 512)
	if !ok || len(resp) < 12 {
		return ""
	}
	if resp[0] != id0 || resp[1] != id1 {
		return "" // ID mismatch — not a reply to our query
	}
	if resp[2]&0x80 == 0 {
		return "" // QR bit not set — not a response
	}
	return "DNS"
}

// ntpFingerprint sends an NTP v3 client request (mode 3) and inspects the
// reply: a 48-byte packet whose mode field is 4 (server) marks NTP. The
// stratum byte, when valid (1–15), is appended for a useful detail.
func ntpFingerprint(ctx context.Context, ip string, port int, timeout time.Duration) string {
	req := make([]byte, 48)
	req[0] = 0x1b // LI=0, VN=3, Mode=3 (client)
	resp, ok := udpExchange(ctx, ip, port, timeout, req, 48)
	if !ok || len(resp) < 48 {
		return ""
	}
	if mode := resp[0] & 0x07; mode != 4 {
		return "" // not a server reply
	}
	if stratum := resp[1]; stratum >= 1 && stratum <= 15 {
		return "NTP (stratum " + strconv.Itoa(int(stratum)) + ")"
	}
	return "NTP"
}

// udpExchange dials the UDP port, writes payload, and reads up to readcap
// bytes of the reply. Returns (reply, true) on a non-empty read.
func udpExchange(ctx context.Context, ip string, port int, timeout time.Duration, payload []byte, readCap int) ([]byte, bool) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return nil, false
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(payload); err != nil {
		return nil, false
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, readCap)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return nil, false
	}
	return buf[:n], true
}
