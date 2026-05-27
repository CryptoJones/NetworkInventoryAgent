package scanner

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBannerBytes caps how much we read from each service. The longest
// realistic line banner is an Apache 2.4 Server header at ~120 bytes;
// MySQL greetings cap at the protocol-defined 256-byte packet.
const maxBannerBytes = 512

// lineBanner reads up to the first CR/LF or maxBannerBytes from a service
// that greets the client without prompting (SMTP, FTP, POP3, IMAP, Telnet).
// The label prefix ("SMTP: ", "FTP: ", …) keeps the stored string
// self-describing — operators inspecting Port.Service shouldn't have to
// look up the port number to know which protocol the banner came from.
func lineBanner(ctx context.Context, ip string, port int, timeout time.Duration, label string) string {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(&capReader{r: conn, n: maxBannerBytes}).ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return ""
	}
	return label + ": " + line
}

// tlsHTTPSFingerprint completes a TLS handshake to harvest the peer
// certificate's CN/SAN (which often identifies the appliance vendor —
// e.g. "*.unifi.example.com" → Ubiquiti), then sends a HEAD over the
// already-established connection to capture the Server header. Two pieces
// of identification for the cost of one connection.
//
// InsecureSkipVerify is intentional: self-signed and expired certs are the
// rule, not the exception, on internal inventory targets. We're not
// trusting the cert for security — we're scraping it for identification.
func tlsHTTPSFingerprint(ctx context.Context, ip string, port int, timeout time.Duration) string {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: timeout},
		Config: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // intentional, see comment
			MinVersion:         tls.VersionTLS10,
		},
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	var certInfo string
	if tlsConn, ok := conn.(*tls.Conn); ok {
		state := tlsConn.ConnectionState()
		if len(state.PeerCertificates) > 0 {
			cert := state.PeerCertificates[0]
			if cert.Subject.CommonName != "" {
				certInfo = "CN=" + cert.Subject.CommonName
			} else if len(cert.DNSNames) > 0 {
				certInfo = "SAN=" + cert.DNSNames[0]
			}
		}
	}

	// HEAD over the existing TLS conn so we don't burn a second dial.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		fmt.Sprintf("https://%s/", net.JoinHostPort(ip, strconv.Itoa(port))), nil)
	if err != nil && certInfo == "" {
		return ""
	}
	var serverInfo string
	if err == nil {
		if writeErr := req.Write(conn); writeErr == nil {
			resp, readErr := http.ReadResponse(bufio.NewReader(conn), req)
			if readErr == nil {
				_ = resp.Body.Close()
				if s := resp.Header.Get("Server"); s != "" {
					serverInfo = "Server=" + s
				}
			}
		}
	}

	switch {
	case certInfo != "" && serverInfo != "":
		return "HTTPS: " + certInfo + " " + serverInfo
	case serverInfo != "":
		return "HTTPS: " + serverInfo
	case certInfo != "":
		return "HTTPS: " + certInfo
	default:
		return ""
	}
}

// mysqlGreeting reads MySQL's initial handshake packet. The protocol is
// docs.oracle.com/cd/E17952_01/mysql-8.0-en/connection-phase-packets-
// protocol-handshake-v10.html — for our purposes:
//
//	bytes 0..2  : packet length (LE)
//	byte  3     : packet number (always 0 for greeting)
//	byte  4     : protocol version (almost always 10)
//	bytes 5..   : null-terminated server version string
//
// We don't need to parse past the version string. The handshake is the
// server's first packet — we never send anything, so this is passive.
func mysqlGreeting(ctx context.Context, ip string, port int, timeout time.Duration) string {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n < 6 {
		return ""
	}
	// Header sanity: byte 4 is the protocol version. MySQL has used
	// version 10 since the 3.21 era (~1998). MariaDB and Percona too.
	if buf[4] != 10 {
		return ""
	}
	// Version string starts at byte 5, null-terminated, capped at the
	// remainder of the read so we don't run off the end if the server
	// sends less than expected.
	end := 5
	for end < n && buf[end] != 0 {
		end++
	}
	if end == 5 {
		return ""
	}
	return "MySQL: " + string(buf[5:end])
}

// capReader wraps an io.Reader with a hard byte cap, defended against a
// peer that sends data without an end-of-line for longer than we'd want
// to wait. Used by lineBanner.
type capReader struct {
	r interface {
		Read([]byte) (int, error)
	}
	n int
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, fmt.Errorf("banner exceeded %d bytes", maxBannerBytes)
	}
	if len(p) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= n
	return n, err
}
