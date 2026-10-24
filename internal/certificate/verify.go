package certificate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto/certs"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// TLS verification statuses.
const (
	// TLSStatusVerified marks a listener that completed the TLS handshake.
	TLSStatusVerified = "verified"
	// TLSStatusUnverified marks a listener that could not be checked, for
	// example because MCM cannot reach the port or because the listener
	// demands a client certificate MCM does not own.
	TLSStatusUnverified = "unverified"
	// TLSStatusFailed marks a listener that rejected the new material.
	TLSStatusFailed = "failed"
)

// TLSVerification reports the outcome of one handshake check.
type TLSVerification struct {
	ListenerID string `json:"listener_id"`
	Port       int    `json:"port"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Detail     string `json:"detail"`
}

// verifyListener performs a TLS handshake against one bound listener.
//
// The check proves that the broker serves the new material. A listener that
// cannot be reached is reported as unverified instead of verified: the
// service never claims more than it observed.
func (s *Service) verifyListener(ctx context.Context, profileName string, spec storage.ListenerSpecRow) TLSVerification {
	verification := TLSVerification{ListenerID: spec.ID, Port: spec.Port}

	files, err := s.store.ActiveFiles(profileName)
	if err != nil {
		verification.Status = TLSStatusUnverified
		verification.Reason = "the active certificate material could not be read"
		verification.Detail = err.Error()
		return verification
	}

	leafPEM, err := os.ReadFile(files.CertPath)
	if err != nil {
		verification.Status = TLSStatusUnverified
		verification.Reason = "the active certificate could not be read"
		verification.Detail = err.Error()
		return verification
	}
	leaf, err := certs.ParseCertificatesPEM(string(leafPEM))
	if err != nil || len(leaf) == 0 {
		verification.Status = TLSStatusFailed
		verification.Reason = "the active certificate is not a valid PEM certificate"
		return verification
	}

	state := &verifyState{}
	tlsConfig, reason := buildVerifyConfig(files, leaf[0], state)
	if tlsConfig == nil {
		verification.Status = TLSStatusUnverified
		verification.Reason = reason
		return verification
	}

	address := net.JoinHostPort(s.brokerHost, strconv.Itoa(spec.Port))
	raw, err := s.dialListener(ctx, address)
	if err != nil {
		verification.Status = TLSStatusUnverified
		verification.Reason = "the listener port is not reachable from MCM"
		verification.Detail = err.Error()
		return verification
	}
	defer raw.Close()

	conn := tls.Client(raw, tlsConfig)
	handshakeCtx, cancel := context.WithTimeout(ctx, s.verifyTimeout)
	defer cancel()
	if err := conn.HandshakeContext(handshakeCtx); err != nil {
		if state.clientCertificateRequested || isClientCertificateRequired(err) {
			verification.Status = TLSStatusUnverified
			verification.Reason = "the listener requires a client certificate that MCM does not own"
			verification.Detail = err.Error()
			return verification
		}
		verification.Status = TLSStatusFailed
		verification.Reason = "the TLS handshake with the listener failed"
		verification.Detail = err.Error()
		return verification
	}

	// TLS 1.3 completes the handshake before the broker rejects the
	// missing client certificate, so the rejection is invisible to the
	// handshake itself. The certificate request still proves that the
	// listener expects a client certificate MCM does not own, which is
	// reported as unverified instead of a verified listener.
	if state.clientCertificateRequested {
		verification.Status = TLSStatusUnverified
		verification.Reason = "the listener requires a client certificate that MCM does not own"
		return verification
	}

	// A completed handshake only proves that the broker serves a trusted
	// certificate. The served leaf is compared with the activated revision
	// so a rotation inside one authority cannot pass while the broker keeps
	// serving the previous certificate.
	served := conn.ConnectionState().PeerCertificates
	if len(served) == 0 {
		verification.Status = TLSStatusFailed
		verification.Reason = "the broker did not present a certificate"
		return verification
	}
	expected := sha256.Sum256(leaf[0].Raw)
	actual := sha256.Sum256(served[0].Raw)
	if expected != actual {
		verification.Status = TLSStatusFailed
		verification.Reason = "the broker served a different certificate than the activated revision"
		verification.Detail = fmt.Sprintf("expected sha256:%s, served sha256:%s", hex.EncodeToString(expected[:]), hex.EncodeToString(actual[:]))
		return verification
	}

	verification.Status = TLSStatusVerified
	verification.Reason = "the broker served the new certificate"
	return verification
}

// dialListener opens a TCP connection to the listener.
//
// The broker is restarted right before the verification runs, so the port can
// still refuse connections for a short moment. Only the connection is
// retried: once the broker answers, the handshake verdict is final.
func (s *Service) dialListener(ctx context.Context, address string) (net.Conn, error) {
	deadline := time.Now().Add(s.verifyReadyWindow)
	for {
		dialer := &net.Dialer{Timeout: s.verifyTimeout}
		raw, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			return raw, nil
		}
		if !time.Now().Before(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(s.verifyRetryInterval):
		}
	}
}

// verifyState carries what one verification handshake observed.
type verifyState struct {
	// clientCertificateRequested records that the broker asked the client
	// for a certificate.
	//
	// The verification client never owns a client certificate, so the
	// request is detected from the handshake itself instead of from the
	// wording of the alert: the wording depends on the TLS version.
	clientCertificateRequested bool
}

// buildVerifyConfig builds the TLS configuration used to check a listener.
//
// The broker is reached by service name, so the certificate is verified
// against its own subject alternative names instead of the dial address.
func buildVerifyConfig(files certs.Files, leaf *x509.Certificate, state *verifyState) (*tls.Config, string) {
	pool := x509.NewCertPool()
	trusted := false
	if files.CAPath != "" {
		caPEM, err := os.ReadFile(files.CAPath)
		if err == nil && pool.AppendCertsFromPEM(caPEM) {
			trusted = true
		}
	}
	if !trusted && leaf.IsCA && leaf.CheckSignatureFrom(leaf) == nil {
		pool.AddCert(leaf)
		trusted = true
	}
	if !trusted {
		return nil, "the profile has no certificate authority to verify the chain"
	}

	serverName := ""
	if len(leaf.DNSNames) > 0 {
		serverName = leaf.DNSNames[0]
	} else if len(leaf.IPAddresses) > 0 {
		serverName = leaf.IPAddresses[0].String()
	}
	if serverName == "" {
		return nil, "the certificate has no subject alternative name to verify"
	}

	return &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			state.clientCertificateRequested = true
			return &tls.Certificate{}, nil
		},
	}, ""
}

// isClientCertificateRequired reports whether the broker refused the
// handshake because the client did not present a certificate.
func isClientCertificateRequired(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "certificate required") ||
		strings.Contains(message, "bad certificate") && strings.Contains(message, "required")
}

// randomSuffix returns a short random suffix for revision identifiers.
func randomSuffix() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "0000"
	}
	return fmt.Sprintf("%x", buffer)
}
