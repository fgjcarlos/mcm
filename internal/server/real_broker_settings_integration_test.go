//go:build integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// This test owns one disposable Mosquitto container and a temporary config.
// It never attaches to the project Compose stack or uses named volumes.
func TestSettingsApplyEnforcesMaxConnectionsOnRealMosquitto(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer probeCancel()
	if err := exec.CommandContext(probeCtx, "docker", "info").Run(); err != nil {
		t.Skipf("Docker is unavailable: %v", err)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("docker %v: %v\n%s", args, err, stderr.String())
		}
		return strings.TrimSpace(stdout.String())
	}

	directory := t.TempDir()
	const servicePassword = "isolated-settings-service-password"
	hash, err := mosquitto.HashPassword(servicePassword, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	original := "# settings integration fixture\nuser root\nmax_connections 6\nport 1883\nallow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\npersistence false\nlog_dest stdout\n"
	for file, body := range map[string]string{
		"mosquitto.conf": original,
		"passwd":         "mcm:" + hash + "\n",
		"acl":            "user mcm\ntopic readwrite mcm/healthcheck\ntopic readwrite #\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, file), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("mcm-settings-%d", time.Now().UnixNano())
	container := ""
	t.Cleanup(func() {
		containerRef := container
		if containerRef == "" {
			containerRef = name
		}
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "80", containerRef).CombinedOutput()
			t.Logf("disposable Mosquitto logs:\n%s", logs)
		}
		if output, err := exec.Command("docker", "rm", "--force", containerRef).CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(output)), "no such container") {
			t.Errorf("remove disposable Mosquitto container: %v %s", err, output)
		}
		_ = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	container = run(
		"run", "--detach", "--name", name,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--publish", fmt.Sprintf("127.0.0.1:%d:1883", port),
		"--mount", "type=bind,src="+directory+",dst=/mosquitto/config",
		"eclipse-mosquitto:2.0", "mosquitto", "-c", "/mosquitto/config/mosquitto.conf",
	)
	published := run("port", container, "1883/tcp")
	_, portString, err := net.SplitHostPort(published)
	if err != nil {
		t.Fatalf("published port %q: %v", published, err)
	}
	publishedPort, err := strconv.Atoi(portString)
	if err != nil || publishedPort != port {
		t.Fatalf("published port %q differs from reserved port %d: %v", published, port, err)
	}

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	app.now = time.Now
	seedAdminUserWithRole(t, store, "admin", "settings-test-password", auth.RoleAdmin)
	brokerCfg := config.MosquittoConfig{Host: "127.0.0.1", Port: port, Username: "mcm", Password: servicePassword}
	app.cfg.Mosquitto, app.mosquitto = brokerCfg, brokerCfg
	deployCfg := config.DeployConfig{
		Mode: "docker", ACLPath: filepath.Join(directory, "acl"), PasswdPath: filepath.Join(directory, "passwd"),
		ContainerName: name, HealthcheckTimeout: 10 * time.Second,
	}
	applier := &mosquitto.DockerApplier{ACLPath: deployCfg.ACLPath, PasswdPath: deployCfg.PasswdPath, ContainerName: name, Runner: mosquitto.ExecRunner{}}
	audit := func(ctx context.Context, actor, action, resource, id, result string, metadata []byte) {
		if len(metadata) == 0 {
			metadata = []byte("{}")
		}
		if _, err := store.RecordAuditEvent(ctx, storage.CreateAuditEventParams{
			OccurredAt: time.Now().UTC(), Actor: actor, Action: action, ResourceType: resource,
			ResourceID: id, Result: result, Metadata: metadata,
		}); err != nil {
			t.Errorf("record broker settings audit: %v", err)
		}
	}
	catalog := loadTestCatalog(t)
	svc := deploy.NewService(applier, store.ACLStore(), store, store,
		diagnostics.VerifierFunc(diagnostics.VerifyActive), app, brokerCfg, deployCfg, audit).
		WithBrokerConfig(filepath.Join(directory, "mosquitto.conf"), catalog)
	app.deploySvc, app.brokerCfgSvc, app.brokerCfgCatalog = svc, svc, catalog
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	token := loginAs(t, app, "admin", "settings-test-password")
	doRequest := func(method, path string, payload any, proof string) *http.Response {
		t.Helper()
		var body bytes.Buffer
		if payload != nil {
			if err := json.NewEncoder(&body).Encode(payload); err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, &body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if proof != "" {
			req.Header.Set("X-MCM-MFA", proof)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	previewResponse := doRequest(http.MethodPost, "/api/v1/broker/settings/preview", settings.Document{
		Entries: []settings.Entry{{Directive: "max_connections", Values: []string{"4"}}},
	}, "")
	var preview struct {
		RevisionID        string   `json:"revision_id"`
		ReloadKind        string   `json:"reload_kind"`
		RequiresRestart   bool     `json:"requires_restart"`
		ChangedDirectives []string `json:"changed_directives"`
		ConfBody          string   `json:"conf_body"`
	}
	if previewResponse.StatusCode != http.StatusOK {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(previewResponse.Body)
		_ = previewResponse.Body.Close()
		t.Fatalf("settings preview: status %d: %s", previewResponse.StatusCode, body.String())
	}
	if err := json.NewDecoder(previewResponse.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	_ = previewResponse.Body.Close()
	t.Logf("settings preview changes=%v, rendered config:\n%s", preview.ChangedDirectives, preview.ConfBody)
	if preview.RevisionID == "" || preview.ReloadKind != "restart" || !preview.RequiresRestart {
		t.Fatalf("preview does not describe the required restart: %+v", preview)
	}
	if !strings.Contains(preview.ConfBody, "max_connections 4") {
		t.Fatalf("preview did not render max_connections=4:\n%s", preview.ConfBody)
	}

	proof := recentMFATestProof(t, app, token)
	applyResponse := doRequest(http.MethodPost, "/api/v1/broker/config/apply", map[string]any{
		"revision_id": preview.RevisionID, "force": true, "adopt": true,
	}, proof)
	if applyResponse.StatusCode != http.StatusOK {
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(applyResponse.Body)
		_ = applyResponse.Body.Close()
		t.Fatalf("settings apply: status %d: %s", applyResponse.StatusCode, body.String())
	}
	var applied struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(applyResponse.Body).Decode(&applied); err != nil {
		_ = applyResponse.Body.Close()
		t.Fatal(err)
	}
	_ = applyResponse.Body.Close()
	if applied.Status != "active_verified" {
		t.Fatalf("settings apply status = %q, want active_verified", applied.Status)
	}
	appliedConfig, err := os.ReadFile(filepath.Join(directory, "mosquitto.conf"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("applied Mosquitto config:\n%s", appliedConfig)
	if !strings.Contains(string(appliedConfig), "max_connections 4") {
		t.Fatalf("applied config does not contain max_connections=4:\n%s", appliedConfig)
	}
	publishedAfterRestart := run("port", container, "1883/tcp")
	_, portString, err = net.SplitHostPort(publishedAfterRestart)
	if err != nil {
		t.Fatalf("published port after restart %q: %v", publishedAfterRestart, err)
	}
	postRestartPort, err := strconv.Atoi(portString)
	if err != nil || postRestartPort != port {
		t.Fatalf("published port after restart %q differs from reserved port %d: %v", publishedAfterRestart, port, err)
	}

	clientOptions := func(clientID string) *mqtt.ClientOptions {
		return mqtt.NewClientOptions().
			AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).
			SetClientID(clientID).
			SetUsername("mcm").
			SetPassword(servicePassword).
			SetConnectTimeout(time.Second).
			SetKeepAlive(10 * time.Second).
			SetAutoReconnect(false).
			SetConnectRetry(false)
	}
	var first mqtt.Client
	var lastConnectError string
	readyDeadline := time.Now().Add(15 * time.Second)
	for attempt := 1; time.Now().Before(readyDeadline); attempt++ {
		candidate := mqtt.NewClient(clientOptions(fmt.Sprintf("settings-limit-first-%d", attempt)))
		connectToken := candidate.Connect()
		if !connectToken.WaitTimeout(2 * time.Second) {
			lastConnectError = "connection attempt timed out"
			candidate.Disconnect(250)
		} else if err := connectToken.Error(); err != nil {
			lastConnectError = err.Error()
			candidate.Disconnect(250)
		} else {
			first = candidate
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if first == nil {
		t.Fatalf("first MQTT client could not connect after broker restart: %s", lastConnectError)
	}
	clients := []mqtt.Client{first}
	t.Cleanup(func() {
		for _, client := range clients {
			client.Disconnect(250)
		}
	})
	for index := 2; index <= 4; index++ {
		client := mqtt.NewClient(clientOptions(fmt.Sprintf("settings-limit-client-%d", index)))
		token := client.Connect()
		if !token.WaitTimeout(10 * time.Second) {
			t.Fatalf("MQTT client %d connection did not resolve", index)
		}
		if err := token.Error(); err != nil {
			t.Fatalf("Mosquitto refused client %d before max_connections=4 was reached: %v", index, err)
		}
		clients = append(clients, client)
	}
	for index, client := range clients {
		if !client.IsConnected() {
			t.Fatalf("MQTT client %d disconnected before the fifth-client probe", index+1)
		}
	}

	fifth := mqtt.NewClient(clientOptions("settings-limit-client-5"))
	fifthToken := fifth.Connect()
	if !fifthToken.WaitTimeout(10 * time.Second) {
		t.Fatal("fifth MQTT client connection did not resolve")
	}
	if err := fifthToken.Error(); err == nil {
		fifth.Disconnect(250)
		t.Fatal("Mosquitto accepted a fifth client after max_connections was set to 4")
	}

	clients[0].Disconnect(250)
	replacement := mqtt.NewClient(clientOptions("settings-limit-replacement"))
	replacementToken := replacement.Connect()
	if !replacementToken.WaitTimeout(10 * time.Second) {
		t.Fatal("replacement MQTT client connection did not resolve after a slot was freed")
	}
	if err := replacementToken.Error(); err != nil {
		t.Fatalf("broker refused a replacement client after a slot was freed: %v", err)
	}
	clients = append(clients, replacement)
}

func TestSettingsFixtureCleansFailedContainerStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	directory := t.TempDir()
	callsPath := filepath.Join(directory, "docker-calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$T12_DOCKER_CALLS"
case "$1" in
  info) exit 0 ;;
  run) echo fixture-container-id; echo 'port is already allocated' >&2; exit 125 ;;
  rm|logs) exit 0 ;;
esac
exit 2
`
	dockerPath := filepath.Join(directory, "docker")
	if err := os.WriteFile(dockerPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable,
		"-test.run=^TestSettingsApplyEnforcesMaxConnectionsOnRealMosquitto$",
		"-test.count=1",
	)
	env := os.Environ()
	env = withTestEnv(env, "PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = withTestEnv(env, "T12_DOCKER_CALLS", callsPath)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "port is already allocated") {
		t.Fatalf("expected injected docker start failure, got %v:\n%s", err, output)
	}
	calls, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains("\n"+string(calls), "\nrm --force ") {
		t.Fatalf("failed start skipped container cleanup:\n%s", calls)
	}
}

func withTestEnv(env []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}
