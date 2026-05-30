package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HeartbeatClient sends periodic health reports to the MCM server.
// It handles token-based authentication and automatic re-authentication
// when the server returns 401.
type HeartbeatClient struct {
	serverURL  string
	siteID     string
	siteName   string
	version    string
	httpClient *http.Client
	token      string
	mu         sync.Mutex
	username   string // for re-auth
	password   string // for re-auth
	logger     *slog.Logger
}

// heartbeatPayload is the JSON body sent to the heartbeat endpoint.
type heartbeatPayload struct {
	SiteID  string `json:"site_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// loginPayload is the JSON body sent to the auth/login endpoint.
type loginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is the JSON response from the auth/login endpoint.
type loginResponse struct {
	Token string `json:"token"`
}

// NewHeartbeatClient creates a new client from the given config. If a static
// token is configured it is used immediately; otherwise credentials are stored
// for later use by Authenticate.
func NewHeartbeatClient(cfg AgentConfig, version string, logger *slog.Logger) *HeartbeatClient {
	timeout := 10 * time.Second
	if t, err := time.ParseDuration(cfg.Heartbeat.Timeout); err == nil && t > 0 {
		timeout = t
	}

	return &HeartbeatClient{
		serverURL: cfg.Server.URL,
		siteID:    cfg.Site.ID,
		siteName:  cfg.Site.Name,
		version:   version,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		token:    cfg.Server.Token,
		username: cfg.Server.Username,
		password: cfg.Server.Password,
		logger:   logger,
	}
}

// Send posts a heartbeat to the MCM server. On a 401 response it attempts to
// re-authenticate (if credentials are available) and retries exactly once.
// Any other non-2xx status is returned as an error.
func (c *HeartbeatClient) Send(ctx context.Context, result ProbeResult) error {
	err := c.sendOnce(ctx, result)
	if err == nil {
		return nil
	}

	// Only retry on 401; anything else propagates immediately.
	var httpErr *httpStatusError
	if !isHTTPStatus(err, &httpErr) || httpErr.statusCode != http.StatusUnauthorized {
		return err
	}

	// Static-only token: cannot re-auth.
	c.mu.Lock()
	hasCredentials := c.username != "" && c.password != ""
	c.mu.Unlock()

	if !hasCredentials {
		return fmt.Errorf("heartbeat: server returned 401 and no credentials available for re-authentication")
	}

	c.logger.Info("heartbeat: 401 received, re-authenticating")
	if authErr := c.authenticate(ctx); authErr != nil {
		return fmt.Errorf("heartbeat: re-authentication failed: %w", authErr)
	}

	return c.sendOnce(ctx, result)
}

// Authenticate obtains a token from the MCM auth endpoint using the configured
// username and password. It is safe to call concurrently.
//
// This is exported so the Agent can call it at startup. If a static token is
// configured without credentials, re-auth is not possible and this returns an
// error.
func (c *HeartbeatClient) Authenticate(ctx context.Context) error {
	return c.authenticate(ctx)
}

// authenticate is the internal implementation shared by Authenticate and Send.
func (c *HeartbeatClient) authenticate(ctx context.Context) error {
	c.mu.Lock()
	username := c.username
	password := c.password
	c.mu.Unlock()

	if username == "" || password == "" {
		return fmt.Errorf("authenticate: no username/password configured; cannot obtain token")
	}

	payload := loginPayload{Username: username, Password: password}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("authenticate: marshal login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.serverURL+"/api/v1/auth/login",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("authenticate: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authenticate: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("authenticate: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authenticate: server returned %d: %s", resp.StatusCode, respBody)
	}

	var loginResp loginResponse
	if err := json.Unmarshal(respBody, &loginResp); err != nil {
		return fmt.Errorf("authenticate: parse response: %w", err)
	}
	if loginResp.Token == "" {
		return fmt.Errorf("authenticate: server returned empty token")
	}

	c.mu.Lock()
	c.token = loginResp.Token
	c.mu.Unlock()

	c.logger.Info("heartbeat: authentication successful")
	return nil
}

// sendOnce performs a single POST to the heartbeat endpoint.
func (c *HeartbeatClient) sendOnce(ctx context.Context, result ProbeResult) error {
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()

	payload := heartbeatPayload{
		SiteID:  c.siteID,
		Name:    c.siteName,
		Version: c.version,
		Status:  string(result.Status),
		Message: result.Message,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("heartbeat: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.serverURL+"/api/v1/edge/heartbeat",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("heartbeat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("heartbeat: request failed: %w", err)
	}
	defer resp.Body.Close()

	// Drain body so the connection can be reused.
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &httpStatusError{
			statusCode: resp.StatusCode,
			body:       string(respBody),
		}
	}

	c.logger.Info("heartbeat: sent successfully", "status", resp.StatusCode)
	return nil
}

// httpStatusError represents a non-2xx HTTP response.
type httpStatusError struct {
	statusCode int
	body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("heartbeat: server returned %d: %s", e.statusCode, e.body)
}

// isHTTPStatus checks whether err is an *httpStatusError and stores the pointer.
func isHTTPStatus(err error, out **httpStatusError) bool {
	var he *httpStatusError
	if err == nil {
		return false
	}
	// Direct type assertion since we control this error type.
	he, ok := err.(*httpStatusError)
	if ok {
		*out = he
	}
	return ok
}
