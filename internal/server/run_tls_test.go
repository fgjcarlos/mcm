package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestBuildHTTPTLSConfigDisabledReturnsNil(t *testing.T) {
	cfg := config.HTTPTLSConfig{Enabled: false, MinVersion: "1.2"}
	tlsConfig, err := buildHTTPTLSConfig(cfg)
	if err != nil {
		t.Fatalf("buildHTTPTLSConfig returned error: %v", err)
	}
	if tlsConfig != nil {
		t.Fatalf("tlsConfig = %#v, want nil when disabled", tlsConfig)
	}
}

func TestBuildHTTPTLSConfigLoadsServerCertAndMinVersion(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, _, _ := writeTestServerCerts(t, dir, "127.0.0.1")

	tlsConfig, err := buildHTTPTLSConfig(config.HTTPTLSConfig{
		Enabled:    true,
		CertFile:   serverCert,
		KeyFile:    serverKey,
		MinVersion: "1.3",
	})
	if err != nil {
		t.Fatalf("buildHTTPTLSConfig returned error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("tlsConfig is nil when TLS enabled")
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %d, want %d", tlsConfig.MinVersion, tls.VersionTLS13)
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("Certificates length = %d, want 1", len(tlsConfig.Certificates))
	}
	if tlsConfig.ClientAuth != tls.NoClientCert {
		t.Fatalf("ClientAuth = %d, want NoClientCert when no client CA", tlsConfig.ClientAuth)
	}
}

func TestBuildHTTPTLSConfigEnablesMTLSWhenClientCAProvided(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, clientCA, _ := writeTestServerCerts(t, dir, "127.0.0.1")

	tlsConfig, err := buildHTTPTLSConfig(config.HTTPTLSConfig{
		Enabled:           true,
		CertFile:          serverCert,
		KeyFile:           serverKey,
		MinVersion:        "1.2",
		ClientCAFile:      clientCA,
		RequireClientCert: true,
	})
	if err != nil {
		t.Fatalf("buildHTTPTLSConfig returned error: %v", err)
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %d, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if tlsConfig.ClientCAs == nil {
		t.Fatal("ClientCAs pool was not built")
	}
}

func TestBuildHTTPTLSConfigRejectsRequireClientCertWithoutCA(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, _, _ := writeTestServerCerts(t, dir, "127.0.0.1")

	_, err := buildHTTPTLSConfig(config.HTTPTLSConfig{
		Enabled:           true,
		CertFile:          serverCert,
		KeyFile:           serverKey,
		MinVersion:        "1.2",
		RequireClientCert: true,
	})
	if err == nil {
		t.Fatal("expected error when require_client_cert is true without client_ca_file")
	}
	if !strings.Contains(err.Error(), "client_ca_file") {
		t.Fatalf("error message = %v, want mention of client_ca_file", err)
	}
}

func TestRunServesHTTPSWithHSTS(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, _, _ := writeTestServerCerts(t, dir, "127.0.0.1")
	cfg := newRunTestConfig(t, dir)
	cfg.HTTP.TLS = config.HTTPTLSConfig{
		Enabled:    true,
		CertFile:   serverCert,
		KeyFile:    serverKey,
		MinVersion: "1.2",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, cfg)
	}()
	waitForHTTPSReady(t, addr)

	client := newInsecureHTTPSClient()
	resp, err := client.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("HTTPS GET /healthz returned error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got == "" {
		t.Fatal("Strict-Transport-Security header missing on TLS response")
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunMTLSRejectsUnauthenticatedClient(t *testing.T) {
	dir := t.TempDir()
	serverCert, serverKey, clientCA, clientCAPriv := writeTestServerCerts(t, dir, "127.0.0.1")
	cfg := newRunTestConfig(t, dir)
	cfg.HTTP.TLS = config.HTTPTLSConfig{
		Enabled:           true,
		CertFile:          serverCert,
		KeyFile:           serverKey,
		MinVersion:        "1.2",
		ClientCAFile:      clientCA,
		RequireClientCert: true,
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port)
	runErr := make(chan error, 1)
	go func() {
		runErr <- Run(ctx, cfg)
	}()
	waitForHTTPSReady(t, addr)

	// Client without a certificate should be rejected by the server.
	clientNoCert := newInsecureHTTPSClient()
	if _, err := clientNoCert.Get("https://" + addr + "/healthz"); err == nil {
		t.Fatal("expected TLS handshake error for client without certificate")
	}

	// Client presenting a cert signed by the trusted CA should succeed.
	clientCert := issueTestClientCert(t, clientCA, clientCAPriv)
	clientWithCert := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
				Certificates:       []tls.Certificate{clientCert},
			},
		},
	}
	resp, err := clientWithCert.Get("https://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("authenticated mTLS GET returned error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

// newRunTestConfig returns a minimum-viable Config that satisfies validation and
// avoids real broker connectivity.
func newRunTestConfig(t *testing.T, dir string) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.HTTP.BindAddress = "127.0.0.1"
	cfg.HTTP.Port = pickFreePort(t)
	cfg.Database.Path = filepath.Join(dir, "mcm.db")
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	// Point Mosquitto at a closed local port so StartBrokerMonitor stays in its retry loop without dialing real infra.
	cfg.Mosquitto.Host = "127.0.0.1"
	cfg.Mosquitto.Port = 1
	return cfg
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port returned error: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForHTTPSReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("HTTPS server did not become ready at %s", addr)
}

func newInsecureHTTPSClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

// writeTestServerCerts generates a self-signed server cert (covering host) plus
// a separate self-signed client CA. It writes them to dir and returns the paths
// to the server cert/key, the client CA, and the client CA private key so tests
// can issue mTLS client certificates.
func writeTestServerCerts(t *testing.T, dir string, host string) (serverCert string, serverKey string, clientCA string, clientCAKey *ecdsa.PrivateKey) {
	t.Helper()

	serverPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mcm-test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(host)},
		DNSNames:     []string{host},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, serverTemplate, &serverPriv.PublicKey, serverPriv)
	if err != nil {
		t.Fatalf("create server cert: %v", err)
	}

	serverCert = filepath.Join(dir, "server.crt")
	serverKey = filepath.Join(dir, "server.key")
	writePEM(t, serverCert, "CERTIFICATE", serverDER)
	serverKeyDER, err := x509.MarshalECPrivateKey(serverPriv)
	if err != nil {
		t.Fatalf("marshal server key: %v", err)
	}
	writePEM(t, serverKey, "EC PRIVATE KEY", serverKeyDER)

	clientCAKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "mcm-test-client-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &clientCAKey.PublicKey, clientCAKey)
	if err != nil {
		t.Fatalf("create client CA: %v", err)
	}
	clientCA = filepath.Join(dir, "client-ca.crt")
	writePEM(t, clientCA, "CERTIFICATE", caDER)

	return serverCert, serverKey, clientCA, clientCAKey
}

// issueTestClientCert issues a client certificate signed by the CA at clientCAPath using clientCAKey.
func issueTestClientCert(t *testing.T, clientCAPath string, clientCAKey *ecdsa.PrivateKey) tls.Certificate {
	t.Helper()

	caPEM, err := os.ReadFile(clientCAPath)
	if err != nil {
		t.Fatalf("read client CA: %v", err)
	}
	block, _ := pem.Decode(caPEM)
	if block == nil {
		t.Fatal("client CA PEM had no decodable block")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse client CA: %v", err)
	}

	clientPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mcm-test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &clientPriv.PublicKey, clientCAKey)
	if err != nil {
		t.Fatalf("sign client cert: %v", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  clientPriv,
	}
	return cert
}

func writePEM(t *testing.T, path string, blockType string, der []byte) {
	t.Helper()
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
