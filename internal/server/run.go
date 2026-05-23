package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/fgjcarlos/mcm/frontend"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/logging"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// Run initializes persistence and serves the HTTP API.
func Run(ctx context.Context, cfg config.Config) error {
	logger, err := logging.New(cfg.Logging, os.Stderr)
	if err != nil {
		return fmt.Errorf("build logger: %w", err)
	}
	slog.SetDefault(logger)

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		return err
	}
	defer store.Close()

	app, err := New(cfg, store, logger)
	if err != nil {
		return err
	}
	if dist, fErr := frontend.DistFS(); fErr == nil {
		app.frontendFS = dist
	}
	if err := app.BootstrapAdmin(ctx, cfg); err != nil {
		return err
	}
	go app.StartBrokerMonitor(ctx, cfg.Mosquitto)

	handler := withRequestLogging(app.Handler(), logger, app.metrics)
	tlsConfig, err := buildHTTPTLSConfig(cfg.HTTP.TLS)
	if err != nil {
		return err
	}
	if tlsConfig != nil {
		handler = withHSTS(handler)
	}

	server := &http.Server{
		Addr:      fmt.Sprintf("%s:%d", cfg.HTTP.BindAddress, cfg.HTTP.Port),
		Handler:   handler,
		TLSConfig: tlsConfig,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	if tlsConfig != nil {
		// Cert/key already loaded into TLSConfig; ListenAndServeTLS ignores its file arguments when set.
		if err := server.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen and serve tls: %w", err)
		}
		return nil
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}
	return nil
}

// buildHTTPTLSConfig returns a tls.Config when TLS is enabled, or nil otherwise.
// The configuration is validated by config.Validate before reaching this point;
// any IO error here aborts startup so misconfiguration fails loudly.
func buildHTTPTLSConfig(cfg config.HTTPTLSConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load http tls cert/key: %w", err)
	}

	minVersion, err := parseTLSVersion(cfg.MinVersion)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   minVersion,
	}

	if cfg.ClientCAFile != "" {
		caPEM, err := os.ReadFile(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read http tls client_ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("http tls client_ca_file %q contains no PEM certificates", cfg.ClientCAFile)
		}
		tlsConfig.ClientCAs = pool
		if cfg.RequireClientCert {
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		} else {
			tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
		}
	} else if cfg.RequireClientCert {
		return nil, fmt.Errorf("http.tls.require_client_cert is true but http.tls.client_ca_file is empty")
	}

	return tlsConfig, nil
}

func parseTLSVersion(value string) (uint16, error) {
	switch value {
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unsupported http.tls.min_version %q (use \"1.2\" or \"1.3\")", value)
	}
}

// withHSTS adds Strict-Transport-Security to every TLS response.
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}
