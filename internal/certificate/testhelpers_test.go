package certificate

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"testing"
)

// tlsListen starts a TLS listener that serves the given certificate.
func tlsListen(pair tls.Certificate) (net.Listener, error) {
	return tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	})
}

// encodeJSON renders a value as JSON for the leak assertions.
func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %T: %v", value, err)
	}
	return string(encoded)
}
