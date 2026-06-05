package scanner

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLineBanner_SMTP(t *testing.T) {
	greeting := "220 mx.example.com ESMTP Postfix (Debian)\r\n"
	addr := startLineGreeting(t, greeting)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port := atoi(t, portStr)

	got := lineBanner(context.Background(), host, port, 500*time.Millisecond, "SMTP")
	if got != "SMTP: 220 mx.example.com ESMTP Postfix (Debian)" {
		t.Errorf("got %q", got)
	}
}

func TestLineBanner_FTP(t *testing.T) {
	addr := startLineGreeting(t, "220 (vsFTPd 3.0.3)\r\n")
	host, portStr, _ := net.SplitHostPort(addr)
	got := lineBanner(context.Background(), host, atoi(t, portStr), 500*time.Millisecond, "FTP")
	if got != "FTP: 220 (vsFTPd 3.0.3)" {
		t.Errorf("got %q", got)
	}
}

func TestLineBanner_EmptyServerReturnsEmpty(t *testing.T) {
	// A server that accepts but never writes — lineBanner should time
	// out and return "" rather than hang the scan loop.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c // hold open without writing
		}
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	got := lineBanner(context.Background(), "127.0.0.1", atoi(t, portStr), 100*time.Millisecond, "SMTP")
	if got != "" {
		t.Errorf("expected empty banner on silent server, got %q", got)
	}
}

func TestMySQLGreeting(t *testing.T) {
	// Hand-build a realistic v10 handshake: payload = [protover, version-string, NUL, ...]
	// We don't need the full handshake — just enough for the parser.
	version := "8.0.35-MySQL Community Server - GPL"
	payload := append([]byte{10}, version...)
	payload = append(payload, 0) // null terminator

	// 4-byte packet header: 3 bytes LE length + 1 byte seq number
	pkt := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(pkt, uint32(len(payload)))
	pkt[3] = 0 // seq
	copy(pkt[4:], payload)

	addr := startRawWrite(t, pkt)
	host, portStr, _ := net.SplitHostPort(addr)
	got := mysqlGreeting(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "MySQL: 8.0.35-MySQL Community Server - GPL" {
		t.Errorf("got %q", got)
	}
}

func TestMySQLGreeting_WrongProtocolVersion(t *testing.T) {
	// Same shape but protocol version != 10 → must return "".
	addr := startRawWrite(t, append([]byte{4, 0, 0, 0, 9}, "x"...))
	host, portStr, _ := net.SplitHostPort(addr)
	got := mysqlGreeting(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "" {
		t.Errorf("expected empty (wrong protover) got %q", got)
	}
}

func TestTLSHTTPSFingerprint(t *testing.T) {
	// Self-signed cert with a recognisable CN so we can assert the CN
	// makes it into the result string.
	cert, key := selfSignedCert(t, "router.example.com")
	tlsCert, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "TestAppliance/1.0")
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	defer srv.Close()

	host, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	got := tlsHTTPSFingerprint(context.Background(), host, atoi(t, portStr), 2*time.Second)

	if !strings.Contains(got, "HTTPS:") {
		t.Errorf("expected HTTPS: prefix, got %q", got)
	}
	if !strings.Contains(got, "CN=router.example.com") {
		t.Errorf("expected CN in result, got %q", got)
	}
	if !strings.Contains(got, "Server=TestAppliance/1.0") {
		t.Errorf("expected Server header in result, got %q", got)
	}
}

func TestRedisInfo_Version(t *testing.T) {
	resp := "$80\r\n# Server\r\nredis_version:7.2.4\r\nredis_mode:standalone\r\n"
	addr := startRequestResponse(t, []byte(resp))
	host, portStr, _ := net.SplitHostPort(addr)
	got := redisInfo(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "Redis: 7.2.4" {
		t.Errorf("got %q", got)
	}
}

func TestRedisInfo_NoAuth(t *testing.T) {
	addr := startRequestResponse(t, []byte("-NOAUTH Authentication required.\r\n"))
	host, portStr, _ := net.SplitHostPort(addr)
	got := redisInfo(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "Redis (auth required)" {
		t.Errorf("got %q", got)
	}
}

func TestMemcachedVersion(t *testing.T) {
	addr := startRequestResponse(t, []byte("VERSION 1.6.21\r\n"))
	host, portStr, _ := net.SplitHostPort(addr)
	got := memcachedVersion(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "Memcached: 1.6.21" {
		t.Errorf("got %q", got)
	}
}

func TestMemcachedVersion_NotMemcached(t *testing.T) {
	addr := startRequestResponse(t, []byte("ERROR\r\n"))
	host, portStr, _ := net.SplitHostPort(addr)
	got := memcachedVersion(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "" {
		t.Errorf("expected empty for non-memcached reply, got %q", got)
	}
}

func TestPostgresProbe_Identified(t *testing.T) {
	for _, reply := range []byte{'S', 'N'} {
		addr := startRequestResponse(t, []byte{reply})
		host, portStr, _ := net.SplitHostPort(addr)
		got := postgresProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
		if got != "PostgreSQL" {
			t.Errorf("reply %q: got %q", reply, got)
		}
	}
}

func TestPostgresProbe_NotPostgres(t *testing.T) {
	addr := startRequestResponse(t, []byte{'X'})
	host, portStr, _ := net.SplitHostPort(addr)
	got := postgresProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "" {
		t.Errorf("expected empty for non-postgres reply, got %q", got)
	}
}

func TestVNCBanner_Identified(t *testing.T) {
	addr := startLineGreeting(t, "RFB 003.008\n")
	host, portStr, _ := net.SplitHostPort(addr)
	got := vncBanner(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "VNC: RFB 003.008" {
		t.Errorf("got %q", got)
	}
}

func TestVNCBanner_NotVNC(t *testing.T) {
	// A non-RFB greeting on the port must not be labelled VNC.
	addr := startLineGreeting(t, "220 some-ftp-server\r\n")
	host, portStr, _ := net.SplitHostPort(addr)
	got := vncBanner(context.Background(), host, atoi(t, portStr), 500*time.Millisecond)
	if got != "" {
		t.Errorf("got %q, want empty for non-RFB greeting", got)
	}
}

func TestRDPProbe_Identified(t *testing.T) {
	// Any TPKT-framed reply (first byte 0x03) is RDP.
	addr := startRequestResponse(t, []byte{0x03, 0x00, 0x00, 0x13, 0xd0})
	host, portStr, _ := net.SplitHostPort(addr)
	if got := rdpProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "RDP" {
		t.Errorf("got %q", got)
	}
}

func TestRDPProbe_NotRDP(t *testing.T) {
	addr := startRequestResponse(t, []byte{0x16, 0x03, 0x01}) // looks like TLS, not TPKT
	host, portStr, _ := net.SplitHostPort(addr)
	if got := rdpProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMSSQLPrelogin_Identified(t *testing.T) {
	// TDS response packet type 0x04.
	addr := startRequestResponse(t, []byte{0x04, 0x01, 0x00, 0x2b})
	host, portStr, _ := net.SplitHostPort(addr)
	if got := mssqlPrelogin(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "MSSQL" {
		t.Errorf("got %q", got)
	}
}

func TestMSSQLPrelogin_NotMSSQL(t *testing.T) {
	addr := startRequestResponse(t, []byte{0x00, 0x01})
	host, portStr, _ := net.SplitHostPort(addr)
	if got := mssqlPrelogin(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestMongoProbe_Identified(t *testing.T) {
	// 16-byte reply header with opCode 1 (OP_REPLY) at offset 12.
	resp := make([]byte, 36)
	resp[12] = 0x01 // opcode 1, little-endian
	addr := startRequestResponse(t, resp)
	host, portStr, _ := net.SplitHostPort(addr)
	if got := mongoProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "MongoDB" {
		t.Errorf("got %q", got)
	}
}

func TestMongoProbe_NotMongo(t *testing.T) {
	resp := make([]byte, 16) // opcode 0 at offset 12
	addr := startRequestResponse(t, resp)
	host, portStr, _ := net.SplitHostPort(addr)
	if got := mongoProbe(context.Background(), host, atoi(t, portStr), 500*time.Millisecond); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- helpers ---

// startRequestResponse accepts one connection, consumes the client's
// request bytes, then writes the canned response and closes. Suited to
// request/response protocols (Redis, Memcached, Postgres) where the client
// speaks first.
func startRequestResponse(t *testing.T, response []byte) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 256)
			_, _ = c.Read(buf)
			_, _ = c.Write(response)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func startLineGreeting(t *testing.T, line string) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(line))
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func startRawWrite(t *testing.T, blob []byte) (addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write(blob)
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	var v int
	for _, r := range s {
		v = v*10 + int(r-'0')
	}
	return v
}

func selfSignedCert(t *testing.T, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  false,
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{cn, "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pemBlock("CERTIFICATE", der)
	keyPEM = pemBlock("PRIVATE KEY", keyDER)
	return
}

func pemBlock(typ string, der []byte) []byte {
	// Tiny inline PEM encoder so we don't add a new test-time dep.
	const lineLen = 64
	b64 := base64Encode(der)
	out := "-----BEGIN " + typ + "-----\n"
	for i := 0; i < len(b64); i += lineLen {
		end := i + lineLen
		if end > len(b64) {
			end = len(b64)
		}
		out += b64[i:end] + "\n"
	}
	out += "-----END " + typ + "-----\n"
	return []byte(out)
}

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
