package tlsutil_test

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
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/tlsutil"
)

// writePEM writes a PEM-encoded block to path with mode 0o600.
func writePEM(t *testing.T, path string, blockType string, der []byte) {
	t.Helper()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// generateCA creates a self-signed CA certificate, writes it to dir, and
// returns the file path and private key.
func generateCA(t *testing.T, dir string) (caFile string, caKey *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlsutil-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	caFile = filepath.Join(dir, "ca.crt")
	writePEM(t, caFile, "CERTIFICATE", der)
	return caFile, key
}

// generateClientCert creates a client certificate signed by caKey/caCert,
// writes cert and key files to dir, and returns their paths.
func generateClientCert(t *testing.T, dir string, caFile string, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read CA: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "tlsutil-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create client cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}

	certFile = filepath.Join(dir, "client.crt")
	keyFile = filepath.Join(dir, "client.key")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func mosqCfg(host string, tlsCfg config.MosquittoTLSConfig) config.MosquittoConfig {
	return config.MosquittoConfig{Host: host, Port: 8883, TLS: tlsCfg}
}

func TestBuildMosquittoTLSConfigPopulatesRootCAsAndClientCert(t *testing.T) {
	dir := t.TempDir()
	caFile, caKey := generateCA(t, dir)
	certFile, keyFile := generateClientCert(t, dir, caFile, caKey)

	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:            true,
		CACertFile:         caFile,
		ClientCertFile:     certFile,
		ClientKeyFile:      keyFile,
		InsecureSkipVerify: false,
	})

	tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("BuildMosquittoTLSConfig returned nil config")
	}
	if tlsConfig.RootCAs == nil {
		t.Fatal("RootCAs is nil — CA cert was not loaded")
	}
	if len(tlsConfig.Certificates) == 0 {
		t.Fatal("Certificates is empty — client cert/key were not loaded")
	}
	if tlsConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify is true, want false")
	}
	if tlsConfig.ServerName != "broker.example.com" {
		t.Fatalf("ServerName = %q, want %q", tlsConfig.ServerName, "broker.example.com")
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want TLS 1.2", tlsConfig.MinVersion)
	}
}

func TestBuildMosquittoTLSConfigIPHostClearsServerName(t *testing.T) {
	dir := t.TempDir()
	caFile, caKey := generateCA(t, dir)
	certFile, keyFile := generateClientCert(t, dir, caFile, caKey)

	cfg := mosqCfg("192.168.1.10", config.MosquittoTLSConfig{
		Enabled:        true,
		CACertFile:     caFile,
		ClientCertFile: certFile,
		ClientKeyFile:  keyFile,
	})

	tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if tlsConfig.ServerName != "" {
		t.Fatalf("ServerName = %q, want empty for IP host", tlsConfig.ServerName)
	}
}

func TestBuildMosquittoTLSConfigCACertOnly(t *testing.T) {
	dir := t.TempDir()
	caFile, _ := generateCA(t, dir)

	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:    true,
		CACertFile: caFile,
	})

	tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if tlsConfig.RootCAs == nil {
		t.Fatal("RootCAs is nil")
	}
	if len(tlsConfig.Certificates) != 0 {
		t.Fatalf("Certificates = %d, want 0 when no client cert provided", len(tlsConfig.Certificates))
	}
}

func TestBuildMosquittoTLSConfigRejectsMissingCAFile(t *testing.T) {
	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:    true,
		CACertFile: "/nonexistent/ca.crt",
	})

	_, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for missing CA cert file")
	}
	if !strings.Contains(err.Error(), "ca_cert_file") {
		t.Fatalf("error = %q, want mention of ca_cert_file", err)
	}
}

func TestBuildMosquittoTLSConfigRejectsMismatchedClientCertKey(t *testing.T) {
	dir := t.TempDir()
	caFile, _ := generateCA(t, dir)

	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:        true,
		CACertFile:     caFile,
		ClientCertFile: "client.crt",
		// ClientKeyFile intentionally missing
	})

	_, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error when only one of client_cert_file / client_key_file is set")
	}
	if !strings.Contains(err.Error(), "client_cert_file") || !strings.Contains(err.Error(), "client_key_file") {
		t.Fatalf("error = %q, want mention of both client_cert_file and client_key_file", err)
	}
}

func TestBuildMosquittoTLSConfigInsecureSkipVerify(t *testing.T) {
	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:            true,
		InsecureSkipVerify: true,
	})

	tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify is false, want true")
	}
}

func TestBuildMosquittoTLSConfigInvalidCAPEM(t *testing.T) {
	dir := t.TempDir()
	badCA := filepath.Join(dir, "bad-ca.crt")
	if err := os.WriteFile(badCA, []byte("not a valid PEM"), 0o600); err != nil {
		t.Fatalf("write bad CA: %v", err)
	}

	cfg := mosqCfg("broker.example.com", config.MosquittoTLSConfig{
		Enabled:    true,
		CACertFile: badCA,
	})

	_, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid PEM content")
	}
	if !strings.Contains(err.Error(), "ca_cert_file") {
		t.Fatalf("error = %q, want mention of ca_cert_file", err)
	}
}

// TestBuildMosquittoTLSConfigServerNameFromIPv6 ensures IPv6 hosts are treated
// the same as IPv4 — ServerName should be cleared.
func TestBuildMosquittoTLSConfigServerNameFromIPv6(t *testing.T) {
	cfg := mosqCfg("::1", config.MosquittoTLSConfig{
		Enabled:            true,
		InsecureSkipVerify: true,
	})

	tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if tlsConfig.ServerName != "" {
		t.Fatalf("ServerName = %q, want empty for IPv6 host", tlsConfig.ServerName)
	}
}
