package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/metrics"
)

type requestContextKey string

const (
	// RequestIDHeader is the canonical HTTP header used to read/write the request ID.
	RequestIDHeader = "X-Request-ID"

	requestIDContextKey requestContextKey = "request_id"
	requestIDByteLen                      = 12
)

// RequestIDFromContext returns the request ID associated with ctx, or "" when missing.
func RequestIDFromContext(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDContextKey).(string); ok {
		return value
	}
	return ""
}

// withRequestLogging wraps next so every request is annotated with a request ID, a
// structured access log line is emitted at info level, and Prometheus HTTP metrics
// (mcm_http_requests_total, mcm_http_request_duration_seconds) are updated. Labels
// use the route path pattern set by ServeMux ("unmatched" for unmatched paths) to
// keep label cardinality bounded — never the raw URL with IDs.
func withRequestLogging(next http.Handler, logger *slog.Logger, reg *metrics.Registry, trustedProxies []*net.IPNet) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := normalizeRequestID(r.Header.Get(RequestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set(RequestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
		inner := r.WithContext(ctx)

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(recorder, inner)
		duration := time.Since(start)

		route := routeLabel(inner)

		logger.LogAttrs(ctx, slog.LevelInfo, "http_request",
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("route", route),
			slog.Int("status", recorder.status),
			slog.Duration("duration", duration),
			slog.String("remote_addr", clientIP(r, trustedProxies)),
		)

		if reg != nil {
			reg.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(recorder.status)).Inc()
			reg.HTTPRequestDuration.WithLabelValues(r.Method, route).Observe(duration.Seconds())
		}
	})
}

func routeLabel(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}
	prefix := r.Method + " "
	if strings.HasPrefix(r.Pattern, prefix) {
		return strings.TrimPrefix(r.Pattern, prefix)
	}
	return r.Pattern
}

func newRequestID() string {
	var b [requestIDByteLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read on Linux only fails when the kernel can't be reached; fall
		// back to a deterministic value rather than crashing the request path.
		return "request-id-unavailable"
	}
	return hex.EncodeToString(b[:])
}

// normalizeRequestID trims whitespace, caps the length to a safe size, and drops
// any characters that aren't ASCII printable so callers can't smuggle CR/LF into
// the response header echoed back at them.
func normalizeRequestID(value string) string {
	const maxLen = 128
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value) && len(out) < maxLen; i++ {
		c := value[i]
		if c <= 0x20 || c >= 0x7f {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}
