package tlsutil_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/tlsutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientConfig_EmptyReturnsNil(t *testing.T) {
	cfg, err := tlsutil.ClientConfig(config.TLSConfig{})
	require.NoError(t, err)
	assert.Nil(t, cfg, "no TLS settings → nil tls.Config so caller falls through to plain HTTP")
}

func TestClientConfig_ServerNameOnlyStillProducesConfig(t *testing.T) {
	cfg, err := tlsutil.ClientConfig(config.TLSConfig{ServerName: "neuromancer.local"})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "neuromancer.local", cfg.ServerName)
	// MinVersion must be at least TLS 1.2 — we never want to negotiate
	// anything older even by accident.
	assert.GreaterOrEqual(t, int(cfg.MinVersion), 0x0303)
}

func TestClientConfig_LoadsCAAndClientCert(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath := writeSelfSigned(t, dir)

	cfg, err := tlsutil.ClientConfig(config.TLSConfig{
		CACertPath:     caPath,
		ClientCertPath: certPath,
		ClientKeyPath:  keyPath,
	})
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.NotNil(t, cfg.RootCAs, "CA pool must be populated")
	require.Len(t, cfg.Certificates, 1, "client keypair must be loaded")
}

func TestClientConfig_HalfMTLSRejected(t *testing.T) {
	_, err := tlsutil.ClientConfig(config.TLSConfig{ClientCertPath: "/x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_cert_path and client_key_path must be set together")
}

func TestServerConfig_HalfPair(t *testing.T) {
	_, err := tlsutil.ServerConfig("/cert", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls_cert_path and tls_key_path must be set together")
}

func TestServerConfig_Empty(t *testing.T) {
	cfg, err := tlsutil.ServerConfig("", "", "")
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

// writeSelfSigned emits a minimal self-signed cert + key (used both as CA and
// as leaf for these tests, which only need the parser to accept them).
func writeSelfSigned(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	require.NoError(t, err)

	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600))
	require.NoError(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600))
	return
}
