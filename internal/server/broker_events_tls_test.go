package server

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

// writeMQTTClientCert issues a client cert signed by the CA at caFile / caKey,
// writes cert and key PEM files to dir, and returns their paths.
// It reuses issueTestClientCert from run_tls_test.go (same package).
func writeMQTTClientCert(t *testing.T, dir string, caFile string, caKey *ecdsa.PrivateKey) (certFile, keyFile string) {
	t.Helper()

	cert := issueTestClientCert(t, caFile, caKey)

	// Write DER-encoded certificate as PEM.
	certFile = filepath.Join(dir, "mqtt-client.crt")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write MQTT client cert: %v", err)
	}

	// Write EC private key as PEM.
	keyFile = filepath.Join(dir, "mqtt-client.key")
	privKey, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("unexpected private key type %T", cert.PrivateKey)
	}
	keyDER, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		t.Fatalf("marshal MQTT client key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write MQTT client key: %v", err)
	}

	return certFile, keyFile
}

// TestBuildMQTTClientTLSLoadsCAAndClientCerts verifies that when TLS is enabled
// with ca_cert_file, client_cert_file, and client_key_file, the MQTT client
// options carry a *tls.Config with a populated RootCAs pool and a client
// certificate — not just ServerName and InsecureSkipVerify.
func TestBuildMQTTClientTLSLoadsCAAndClientCerts(t *testing.T) {
	dir := t.TempDir()
	// writeTestServerCerts (run_tls_test.go) returns a server cert, server key,
	// a client CA cert file, and the client CA private key.
	_, _, caFile, caKey := writeTestServerCerts(t, dir, "broker.example.com")
	certFile, keyFile := writeMQTTClientCert(t, dir, caFile, caKey)

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.MosquittoConfig{
		Host: "broker.example.com",
		Port: 8883,
		TLS: config.MosquittoTLSConfig{
			Enabled:            true,
			CACertFile:         caFile,
			ClientCertFile:     certFile,
			ClientKeyFile:      keyFile,
			InsecureSkipVerify: false,
		},
	}

	client, err := buildMQTTClient(cfg, "broker.example.com:8883", app)
	if err != nil {
		t.Fatalf("buildMQTTClient returned error: %v", err)
	}
	optsReader := client.OptionsReader()
	tlsCfg := optsReader.TLSConfig()

	if tlsCfg == nil {
		t.Fatal("TLS config is nil — TLS not applied to broker monitor MQTT client")
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("RootCAs is nil — ca_cert_file was not loaded into broker monitor TLS config")
	}
	if len(tlsCfg.Certificates) == 0 {
		t.Fatal("Certificates is empty — client cert/key were not loaded into broker monitor TLS config")
	}
	if tlsCfg.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify is true, want false")
	}
	if tlsCfg.ServerName != "broker.example.com" {
		t.Fatalf("ServerName = %q, want %q", tlsCfg.ServerName, "broker.example.com")
	}
}

// TestBuildMQTTClientTLSIPHostClearsServerName ensures an IP-addressed broker
// does not populate ServerName (Go TLS uses IP-SANs instead).
func TestBuildMQTTClientTLSIPHostClearsServerName(t *testing.T) {
	dir := t.TempDir()
	_, _, caFile, caKey := writeTestServerCerts(t, dir, "127.0.0.1")
	certFile, keyFile := writeMQTTClientCert(t, dir, caFile, caKey)

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	cfg := config.MosquittoConfig{
		Host: "192.168.1.10",
		Port: 8883,
		TLS: config.MosquittoTLSConfig{
			Enabled:        true,
			CACertFile:     caFile,
			ClientCertFile: certFile,
			ClientKeyFile:  keyFile,
		},
	}

	client, err := buildMQTTClient(cfg, "192.168.1.10:8883", app)
	if err != nil {
		t.Fatalf("buildMQTTClient returned error: %v", err)
	}
	optsReader := client.OptionsReader()
	tlsCfg := optsReader.TLSConfig()

	if tlsCfg == nil {
		t.Fatal("TLS config is nil")
	}
	if tlsCfg.ServerName != "" {
		t.Fatalf("ServerName = %q, want empty for IP host", tlsCfg.ServerName)
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("RootCAs is nil for IP-addressed broker")
	}
}
