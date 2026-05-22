// Package logging builds the application's structured slog.Logger from
// config.LoggingConfig.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/fgjcarlos/mcm/internal/config"
)

// New returns a slog.Logger configured from cfg, writing to w (defaulting to os.Stderr).
// Returns an error when the level or format is unrecognized; config.Validate already
// catches these at startup, but New defends in case the logger is built from a
// programmatically constructed config (e.g. tests).
func New(cfg config.LoggingConfig, w io.Writer) (*slog.Logger, error) {
	if w == nil {
		w = os.Stderr
	}
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	handler, err := buildHandler(cfg.Format, w, &slog.HandlerOptions{Level: level})
	if err != nil {
		return nil, err
	}
	return slog.New(handler), nil
}

// Discard returns a no-op slog.Logger suitable for tests.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q", value)
	}
}

func buildHandler(format string, w io.Writer, opts *slog.HandlerOptions) (slog.Handler, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json", "":
		return slog.NewJSONHandler(w, opts), nil
	case "text":
		return slog.NewTextHandler(w, opts), nil
	default:
		return nil, fmt.Errorf("unsupported log format %q", format)
	}
}
