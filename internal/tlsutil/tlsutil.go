// Package tlsutil provides shared TLS configuration helpers for MCM's MQTT
// connectivity. It is used by both the long-running broker monitor
// (internal/server) and the one-shot diagnostics path (internal/diagnostics)
// so that both call sites load CA and client certificates identically.
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/fgjcarlos/mcm/internal/config"
)

// BuildMosquittoTLSConfig constructs a *tls.Config for an outbound MQTT
// connection to Mosquitto from the provided MosquittoConfig.
//
//   - ServerName is set to cfg.Host unless Host parses as an IP address, in
//     which case it is left empty so Go's TLS stack can use IP-SANs.
//   - RootCAs is populated from cfg.TLS.CACertFile when non-empty.
//   - Certificates is populated from cfg.TLS.ClientCertFile + ClientKeyFile
//     when both are provided (mTLS / client-certificate authentication).
//   - InsecureSkipVerify is forwarded as-is from cfg.TLS.InsecureSkipVerify.
//
// The minimum TLS version is always TLS 1.2.
func BuildMosquittoTLSConfig(cfg config.MosquittoConfig) (*tls.Config, error) {
	serverName := strings.TrimSpace(cfg.Host)
	if net.ParseIP(serverName) != nil {
		serverName = ""
	}

	tlsConfig := &tls.Config{ //nolint:gosec // User-controlled option for local/self-signed brokers; InsecureSkipVerify is forwarded intentionally.
		MinVersion:         tls.VersionTLS12,
		ServerName:         serverName,
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
	}

	if strings.TrimSpace(cfg.TLS.CACertFile) != "" {
		caPEM, err := os.ReadFile(cfg.TLS.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read mosquitto.tls.ca_cert_file %q: %w", cfg.TLS.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("mosquitto.tls.ca_cert_file %q does not contain a valid PEM CA certificate", cfg.TLS.CACertFile)
		}
		tlsConfig.RootCAs = pool
	}

	clientCertFile := strings.TrimSpace(cfg.TLS.ClientCertFile)
	clientKeyFile := strings.TrimSpace(cfg.TLS.ClientKeyFile)
	if clientCertFile != "" || clientKeyFile != "" {
		if clientCertFile == "" || clientKeyFile == "" {
			return nil, fmt.Errorf("mosquitto.tls.client_cert_file and mosquitto.tls.client_key_file must both be set for client certificate authentication")
		}
		cert, err := tls.LoadX509KeyPair(clientCertFile, clientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load Mosquitto client certificate/key pair: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
