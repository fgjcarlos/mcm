package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fgjcarlos/mcm/internal/backoff"
)

// Agent orchestrates periodic MQTT broker health checks and sends the results
// to the MCM server as heartbeats.
type Agent struct {
	probe     *MQTTProbe
	heartbeat *HeartbeatClient
	interval  time.Duration
	backoff   *backoff.Backoff
	logger    *slog.Logger
}

// New creates an Agent from the given config. It constructs the MQTT probe and
// heartbeat client but does not start any background activity.
func New(cfg AgentConfig, version string, logger *slog.Logger) (*Agent, error) {
	timeout, err := time.ParseDuration(cfg.Heartbeat.Timeout)
	if err != nil {
		return nil, fmt.Errorf("agent: parse heartbeat timeout: %w", err)
	}

	interval, err := time.ParseDuration(cfg.Heartbeat.Interval)
	if err != nil {
		return nil, fmt.Errorf("agent: parse heartbeat interval: %w", err)
	}

	probe := NewMQTTProbe(cfg.Mosquitto.Host, cfg.Mosquitto.Port, timeout)
	hbClient := NewHeartbeatClient(cfg, version, logger)

	// Backoff: base 5s, cap 5 minutes, ±25% jitter.
	bo := backoff.New(5*time.Second, 5*time.Minute, 0.25)

	return &Agent{
		probe:     probe,
		heartbeat: hbClient,
		interval:  interval,
		backoff:   bo,
		logger:    logger,
	}, nil
}

// Run starts the agent loop. It authenticates (if credentials are configured),
// runs an immediate probe+heartbeat, then repeats on each ticker tick until ctx
// is cancelled.
//
// In-flight requests are given up to 5 seconds to complete after cancellation
// before Run returns.
func (a *Agent) Run(ctx context.Context) error {
	// Authenticate at startup if credentials are available (no static token).
	if err := a.heartbeat.Authenticate(ctx); err != nil {
		// Not fatal — a static token may already be set, or credentials may
		// simply not be configured. Log and continue.
		a.logger.Debug("agent: initial authenticate skipped or failed", "err", err)
	}

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// Channel to signal when an in-flight cycle is complete.
	cycleDone := make(chan struct{}, 1)

	// Run the first cycle immediately without waiting for the first tick.
	go func() {
		a.cycle(ctx)
		select {
		case cycleDone <- struct{}{}:
		default:
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// Wait for the in-flight cycle to finish, up to 5 seconds.
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			select {
			case <-cycleDone:
			case <-shutdownCtx.Done():
				a.logger.Warn("agent: in-flight cycle did not finish within shutdown timeout")
			}
			return nil

		case <-ticker.C:
			go func() {
				a.cycle(ctx)
				select {
				case cycleDone <- struct{}{}:
				default:
				}
			}()
		}
	}
}

// cycle runs one probe + heartbeat iteration. On failure it waits for the
// backoff delay before returning; on success it resets the backoff.
func (a *Agent) cycle(ctx context.Context) {
	result := a.probe.Check(ctx)

	a.logger.Info("agent: probe complete",
		"status", result.Status,
		"latency", result.Latency,
		"message", result.Message,
	)

	if err := a.heartbeat.Send(ctx, result); err != nil {
		a.logger.Error("agent: heartbeat failed", "err", err)

		delay := a.backoff.Next()
		a.logger.Info("agent: backing off", "delay", delay)

		select {
		case <-time.After(delay):
		case <-ctx.Done():
		}
		return
	}

	a.backoff.Reset()
	a.logger.Info("agent: heartbeat sent successfully")
}
