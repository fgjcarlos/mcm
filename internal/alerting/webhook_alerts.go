package alerting

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

const webhookAlertQueueSize = 32

type WebhookAlert struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Severity   string         `json:"severity"`
	Source     string         `json:"source"`
	Message    string         `json:"message"`
	ObservedAt time.Time      `json:"observed_at"`
	Details    map[string]any `json:"details,omitempty"`
}

type WebhookAlerter struct {
	enabled       bool
	endpointURL   string
	signingSecret string
	timeout       time.Duration
	client        *http.Client
	queue         chan WebhookAlert
	logger        *slog.Logger
	mu            sync.Mutex
	closed        bool
	done          chan struct{}
}

func NewWebhookAlerter(cfg config.AlertingConfig, logger *slog.Logger) *WebhookAlerter {
	if logger == nil {
		logger = slog.Default()
	}
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 5 * time.Second
	}

	a := &WebhookAlerter{
		enabled:       cfg.Enabled && strings.TrimSpace(cfg.EndpointURL) != "",
		endpointURL:   strings.TrimSpace(cfg.EndpointURL),
		signingSecret: cfg.SigningSecret,
		timeout:       timeout,
		client:        &http.Client{Timeout: timeout},
		queue:         make(chan WebhookAlert, webhookAlertQueueSize),
		logger:        logger.With(slog.String("component", "webhook_alerter")),
		done:          make(chan struct{}),
	}
	if a.enabled {
		go a.run()
	}
	return a
}

func (a *WebhookAlerter) Enqueue(alert WebhookAlert) {
	if a == nil || !a.enabled {
		return
	}
	if alert.ObservedAt.IsZero() {
		alert.ObservedAt = time.Now().UTC()
	}
	if alert.ID == "" {
		alert.ID = fmt.Sprintf("%s-%d", sanitizeAlertIDPart(alert.Type), alert.ObservedAt.UnixNano())
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	select {
	case a.queue <- alert:
	default:
		a.logger.Warn("webhook alert queue full; dropping alert",
			slog.String("alert_type", alert.Type),
			slog.String("severity", alert.Severity),
		)
	}
}

func (a *WebhookAlerter) Shutdown(ctx context.Context) error {
	if a == nil || !a.enabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	if !a.closed {
		a.closed = true
		close(a.queue)
	}
	a.mu.Unlock()

	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *WebhookAlerter) run() {
	defer close(a.done)
	for alert := range a.queue {
		ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
		err := a.deliver(ctx, alert)
		cancel()
		if err != nil {
			a.logger.Error("webhook alert delivery failed",
				slog.String("alert_type", alert.Type),
				slog.String("severity", alert.Severity),
				slog.String("error", err.Error()),
			)
		}
	}
}

func (a *WebhookAlerter) deliver(ctx context.Context, alert WebhookAlert) error {
	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpointURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mcm-webhook-alerts")
	req.Header.Set("X-MCM-Event", alert.Type)
	if a.signingSecret != "" {
		req.Header.Set("X-MCM-Signature", signWebhookPayload(a.signingSecret, body))
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	return nil
}

func signWebhookPayload(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func sanitizeAlertIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "alert"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
