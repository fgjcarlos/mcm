package server

import (
	"net/http"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestNewHTTPServerAppliesDefensiveLimits(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.HTTP.BindAddress = "127.0.0.1"
	cfg.HTTP.Port = 8080

	server := newHTTPServer(cfg, http.NotFoundHandler(), nil)

	if server.Addr != "127.0.0.1:8080" {
		t.Fatalf("Addr = %q, want %q", server.Addr, "127.0.0.1:8080")
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if server.ReadTimeout != httpReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, httpReadTimeout)
	}
	if server.WriteTimeout != httpWriteTimeout {
		t.Fatalf("WriteTimeout = %s, want %s", server.WriteTimeout, httpWriteTimeout)
	}
	if server.IdleTimeout != httpIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, httpIdleTimeout)
	}
	if server.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, httpMaxHeaderBytes)
	}
}
