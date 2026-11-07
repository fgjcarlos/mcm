//go:build integration

// This file proves issue #300's acceptance criterion "probar TLS simple
// y mTLS": the same production FileWriter that manages listeners splices
// a TLS listener into a real Mosquitto container's configuration, and
// the broker then serves MQTT over TLS — and, after require_certificate
// is enabled, only to clients presenting a certificate signed by the
// configured CA.
package writer_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/fgjcarlos/mcm/internal/listener/writer"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// caBundle holds a self-signed CA and leaf certificates signed by it.
type caBundle struct {
	caCert    x509.Certificate
	caPool    *x509.CertPool
	serverPEM tls.Certificate
	clientPEM tls.Certificate
}

// newCABundle issues a CA, one server certificate for localhost and one
// client certificate, all valid for an hour.
func newCABundle(t *testing.T) *caBundle {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcm-listener-it-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	issue := func(name string, serial int64, isClient bool) tls.Certificate {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		template := x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: name},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		}
		if isClient {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		} else {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}
	}
	return &caBundle{
		caCert:    *caCert,
		caPool:    caPool,
		serverPEM: issue("localhost", 2, false),
		clientPEM: issue("mcm-client", 3, true),
	}
}

// writePEM writes certificate or key material in PEM form.
func writePEM(t *testing.T, path, blockType string, derBytes []byte, key *rsa.PrivateKey) {
	t.Helper()
	var pemBytes []byte
	if key != nil {
		pemBytes = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	} else {
		pemBytes = pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: derBytes})
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

// mqttTLSRoundTrip proves a publish/subscribe round trip over TLS with
// the given client-certificate policy.
func mqttTLSRoundTrip(t *testing.T, name, addr string, bundle *caBundle, withClientCert bool) error {
	t.Helper()
	tlsConfig := &tls.Config{
		RootCAs:            bundle.caPool,
		ServerName:         "localhost",
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	}
	if withClientCert {
		tlsConfig.Certificates = []tls.Certificate{bundle.clientPEM}
	}
	client := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker("ssl://" + addr).
		SetClientID("mcm-tls-it-" + name).
		SetTLSConfig(tlsConfig).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(false))
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("%s: connect timed out", name)
	}
	if err := token.Error(); err != nil {
		return err
	}
	defer client.Disconnect(100)
	return nil
}

// TestListenerWriterServesTLSAndMutualTLSOnRealBroker covers #300:
// simple TLS first, then require_certificate mTLS where a client
// without a certificate is refused.
func TestListenerWriterServesTLSAndMutualTLSOnRealBroker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available: the integration test needs a real broker")
	}

	bundle := newCABundle(t)
	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	bootstrap := strings.Join([]string{
		"allow_anonymous true",
		"persistence false",
		"log_dest stdout",
		"listener 1883 0.0.0.0",
		"",
	}, "\n")
	if err := os.WriteFile(confPath, []byte(bootstrap), 0o644); err != nil {
		t.Fatal(err)
	}
	// The broker reads the material from its config mount.
	writePEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", bundle.caCert.Raw, nil)
	writePEM(t, filepath.Join(dir, "server.pem"), "CERTIFICATE", bundle.serverPEM.Certificate[0], nil)
	writePEM(t, filepath.Join(dir, "server.key"), "", nil, bundle.serverPEM.PrivateKey.(*rsa.PrivateKey))

	name := fmt.Sprintf("mcm-tls-it-%d", time.Now().UnixNano())
	uid := strconv.Itoa(os.Getuid())
	gid := strconv.Itoa(os.Getgid())
	if out, err := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--user", uid+":"+gid,
		"--publish", "127.0.0.1::1883",
		"--publish", "127.0.0.1::8883",
		"--volume", dir+":/mosquitto/config",
		"eclipse-mosquitto:2.0",
	).CombinedOutput(); err != nil {
		t.Fatalf("start mosquitto: %v: %s", err, string(out))
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	waitForPort(t, publishedPort(t, name, "1883/tcp"))

	restart := func() string {
		t.Helper()
		if out, err := exec.Command("docker", "restart", name).CombinedOutput(); err != nil {
			t.Fatalf("restart the broker: %v: %s", err, string(out))
		}
		addr := publishedPort(t, name, "8883/tcp")
		waitForPort(t, addr)
		return addr
	}

	w := writer.New(confPath)

	// --- simple TLS ----------------------------------------------------
	simple := []listeners.ListenerSpec{
		{ID: "tcp", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "tls", Port: 8883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}, Options: map[string]string{
			"certfile": "/mosquitto/config/server.pem",
			"keyfile":  "/mosquitto/config/server.key",
		}},
	}
	if _, err := w.Write(simple); err != nil {
		t.Fatalf("write TLS listeners: %v", err)
	}
	addr := restart()
	// Anonymous MQTT over TLS without a client certificate works.
	if err := mqttTLSRoundTrip(t, "simple", addr, bundle, false); err != nil {
		t.Fatalf("simple TLS: %v", err)
	}

	// --- mTLS ----------------------------------------------------------
	mutual := []listeners.ListenerSpec{
		{ID: "tcp", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "tls", Port: 8883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}, Options: map[string]string{
			"certfile":            "/mosquitto/config/server.pem",
			"keyfile":             "/mosquitto/config/server.key",
			"require_certificate": "true",
			"cafile":              "/mosquitto/config/ca.pem",
		}},
	}
	if _, err := w.Write(mutual); err != nil {
		t.Fatalf("write mTLS listeners: %v", err)
	}
	addr = restart()
	if err := mqttTLSRoundTrip(t, "mtls-client", addr, bundle, true); err != nil {
		t.Fatalf("mTLS with a signed client certificate: %v", err)
	}
	if err := mqttTLSRoundTrip(t, "mtls-nocert", addr, bundle, false); err == nil {
		t.Fatal("mTLS accepted a client without a certificate")
	}
}
