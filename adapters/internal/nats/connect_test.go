package nats

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateTestCerts creates a self-signed CA and a leaf cert signed by it.
// Returns paths to ca.crt, tls.crt, tls.key written under dir.
func generateTestCerts(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()

	// CA key + cert
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caPath = filepath.Join(dir, "ca.crt")
	caFile, err := os.Create(caPath)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(caFile, &pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	caFile.Close()

	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)

	// Leaf key + cert
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "test-leaf",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(24 * time.Hour),
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)

	certPath = filepath.Join(dir, "tls.crt")
	certFile, err := os.Create(certPath)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	certFile.Close()

	keyPath = filepath.Join(dir, "tls.key")
	keyFile, err := os.Create(keyPath)
	require.NoError(t, err)
	leafKeyDER, err := x509.MarshalECPrivateKey(leafKey)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: leafKeyDER}))
	keyFile.Close()

	return caPath, certPath, keyPath
}

func TestBuildTLSConfig_Success(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath := generateTestCerts(t, dir)

	cfg, err := buildTLSConfig(certPath, keyPath, caPath)
	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS13), cfg.MinVersion)
	assert.Len(t, cfg.Certificates, 1)
	assert.NotNil(t, cfg.RootCAs)
}

func TestBuildTLSConfig_MissingCAFile(t *testing.T) {
	dir := t.TempDir()
	_, certPath, keyPath := generateTestCerts(t, dir)

	_, err := buildTLSConfig(certPath, keyPath, filepath.Join(dir, "nonexistent-ca.crt"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading CA cert")
}

func TestBuildTLSConfig_InvalidCAFile(t *testing.T) {
	dir := t.TempDir()
	_, certPath, keyPath := generateTestCerts(t, dir)

	// Write a non-PEM file as CA
	invalidCA := filepath.Join(dir, "invalid-ca.crt")
	require.NoError(t, os.WriteFile(invalidCA, []byte("not valid PEM data"), 0o600))

	_, err := buildTLSConfig(certPath, keyPath, invalidCA)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing CA cert")
}

func TestBuildTLSConfig_InvalidKeyPair(t *testing.T) {
	dir := t.TempDir()
	caPath, _, _ := generateTestCerts(t, dir)

	_, err := buildTLSConfig(filepath.Join(dir, "nonexistent.crt"), filepath.Join(dir, "nonexistent.key"), caPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "loading client key pair")
}

func TestConnect_NoTLS_AttemptsPlain(t *testing.T) {
	// Call Connect with empty TLS paths to an invalid URL — exercises the no-TLS
	// branch. nats.Connect with MaxReconnects(-1) would retry forever, so we use
	// a port that immediately refuses to keep the test fast.
	_, err := Connect("nats://127.0.0.1:1", "test-no-tls", "", "", "")
	assert.Error(t, err)
}
