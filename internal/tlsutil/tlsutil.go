// Package tlsutil builds *tls.Config values from the file paths declared in
// the agent's config. It's intentionally not part of the config package —
// the config layer is pure JSON+validation, no filesystem I/O at validate
// time, and that boundary is worth preserving.
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
)

// ClientConfig builds the *tls.Config the watchdog uses to dial an HTTPS
// peer. Returns (nil, nil) when no TLS settings are populated — the caller
// will fall through to plain HTTP, which is fine for the loopback case.
//
// Whenever any field is set we pin to a project CA rather than the system
// roots; an internal mesh shouldn't trust every public CA implicitly
// (OWASP A05).
func ClientConfig(c config.TLSConfig) (*tls.Config, error) {
	if c.CACertPath == "" && c.ClientCertPath == "" && c.ClientKeyPath == "" && c.ServerName == "" {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.ServerName,
	}
	if c.CACertPath != "" {
		pool, err := loadCAPool(c.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("watchdog tls: %w", err)
		}
		cfg.RootCAs = pool
	}
	if c.ClientCertPath != "" || c.ClientKeyPath != "" {
		if c.ClientCertPath == "" || c.ClientKeyPath == "" {
			return nil, fmt.Errorf("watchdog tls: client_cert_path and client_key_path must be set together")
		}
		cert, err := tls.LoadX509KeyPair(c.ClientCertPath, c.ClientKeyPath)
		if err != nil {
			return nil, fmt.Errorf("watchdog tls: load client keypair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// ServerConfig builds the *tls.Config the health server uses for its
// incoming listener. Returns (nil, nil) when no TLS material is configured.
// When clientCA is set the listener requires mTLS — clients that fail to
// present a valid certificate are rejected before any handler runs.
func ServerConfig(certPath, keyPath, clientCAPath string) (*tls.Config, error) {
	if certPath == "" && keyPath == "" && clientCAPath == "" {
		return nil, nil
	}
	if certPath == "" || keyPath == "" {
		return nil, fmt.Errorf("health tls: tls_cert_path and tls_key_path must be set together")
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("health tls: load server keypair: %w", err)
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
	}
	if clientCAPath != "" {
		pool, err := loadCAPool(clientCAPath)
		if err != nil {
			return nil, fmt.Errorf("health tls: %w", err)
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ca %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no certificates parsed from %q", path)
	}
	return pool, nil
}
