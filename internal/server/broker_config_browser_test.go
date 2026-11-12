package server

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/frontend"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// Opt-in: this owns a unique disposable container and directory. It never
// touches the developer's Compose stack, existing brokers, or named volumes.
func TestBrokerConfigurationBrowserJourney(t *testing.T) {
	if os.Getenv("MCM_E2E_UI") != "1" {
		t.Skip("set MCM_E2E_UI=1 to run the isolated Chromium/Mosquitto journey")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		// Only stdout carries the answer (the container id, the published
		// port, the broker version). The streams are kept apart on
		// purpose: Docker writes the image pull progress to stderr, and
		// a merged output would prepend it to the container id, so every
		// later command would address a container that does not exist.
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
	// The broker log lives in its own mount: MCM ingests it the way an
	// operator's deployment does (mosquitto.log_path), and the connection
	// test of the onboarding journey (#353) observes the created account
	// through it. Keeping it out of the config directory leaves the
	// configuration files MCM manages untouched by the broker's writes.
	logDirectory := t.TempDir()
	const servicePassword = "isolated-mcm-service-password"
	hash, err := mosquitto.HashPassword(servicePassword, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	original := "# existing broker, seeded before MCM starts\nuser root\nlistener 1883\nallow_anonymous false\npassword_file /mosquitto/config/passwd\nacl_file /mosquitto/config/acl\npersistence false\nconnection_messages true\nlog_dest stdout\nlog_dest file /mosquitto/log/mosquitto.log\n"
	for file, body := range map[string]string{"mosquitto.conf": original, "passwd": "mcm:" + hash + "\n", "acl": "user mcm\ntopic readwrite #\n"} {
		if err := os.WriteFile(filepath.Join(directory, file), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The broker refuses to load world-readable credential files, and MCM
	// must keep read/write access to them.
	for _, file := range []string{"passwd", "acl"} {
		if err := os.Chmod(filepath.Join(directory, file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("mcm-305-ui-%d", time.Now().UnixNano())
	// The broker runs with the same uid as this process: the stock image
	// otherwise chowns the mounted directory to its own user, which would
	// take MCM's access away (the production path is tracked in #294).
	container := run("run", "--detach", "--rm", "--name", name, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--publish", "127.0.0.1::1883", "--mount", "type=bind,src="+directory+",dst=/mosquitto/config", "--mount", "type=bind,src="+logDirectory+",dst=/mosquitto/log", "eclipse-mosquitto:2.0", "mosquitto", "-c", "/mosquitto/config/mosquitto.conf")
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "40", container).CombinedOutput()
			t.Logf("isolated broker logs:\n%s", logs)
		}
		if output, err := exec.Command("docker", "rm", "--force", container).CombinedOutput(); err != nil {
			t.Errorf("clean up owned container: %v %s", err, output)
		}
		// The broker container runs as root and may replace the mounted
		// files, so restore ownership before the temporary directory is
		// removed.
		for _, owned := range []string{directory, logDirectory} {
			_ = filepath.Walk(owned, func(path string, info os.FileInfo, err error) error {
				if err == nil {
					_ = os.Chmod(path, 0o700)
				}
				return nil
			})
		}
	})
	published := run("port", container, "1883/tcp")
	_, portString, err := net.SplitHostPort(published)
	if err != nil {
		t.Fatalf("published port %q: %v", published, err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	brokerCfg := config.MosquittoConfig{Host: "127.0.0.1", Port: port, Username: "mcm", Password: servicePassword, LogPath: filepath.Join(logDirectory, "mosquitto.log")}
	deadline := time.Now().Add(20 * time.Second)
	for {
		probeCtx, probeCancel := context.WithTimeout(ctx, time.Second)
		result := diagnostics.CheckMQTTConnectivity(probeCtx, brokerCfg)
		probeCancel()
		if result.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(result.Message)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Logf("existing broker: %s", run("exec", container, "mosquitto", "-h"))
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	// The default test app uses a frozen clock; TOTP enrollment and proof
	// expiry must follow the real wall clock in this journey.
	app.now = time.Now
	seedAdminUserWithRole(t, store, "admin", "ui-test-password", auth.RoleAdmin)
	app.cfg.Mosquitto, app.mosquitto = brokerCfg, brokerCfg
	deployCfg := config.DeployConfig{Mode: "docker", ACLPath: filepath.Join(directory, "acl"), PasswdPath: filepath.Join(directory, "passwd"), ContainerName: name, HealthcheckTimeout: 5 * time.Second}
	applier := &mosquitto.DockerApplier{ACLPath: deployCfg.ACLPath, PasswdPath: deployCfg.PasswdPath, ContainerName: name, Runner: mosquitto.ExecRunner{}}
	audit := func(ctx context.Context, actor, action, resource, id, result string, metadata []byte) {
		if len(metadata) == 0 {
			metadata = []byte("{}")
		}
		if _, err := store.RecordAuditEvent(ctx, storage.CreateAuditEventParams{OccurredAt: time.Now().UTC(), Actor: actor, Action: action, ResourceType: resource, ResourceID: id, Result: result, Metadata: metadata}); err != nil {
			t.Errorf("record deployment audit: %v", err)
		}
	}
	cat := loadTestCatalog(t)
	svc := deploy.NewService(applier, store.ACLStore(), store, store, diagnostics.VerifierFunc(diagnostics.VerifyActive), app, brokerCfg, deployCfg, audit).WithBrokerConfig(filepath.Join(directory, "mosquitto.conf"), cat)
	app.deploySvc, app.brokerCfgSvc, app.brokerCfgCatalog = svc, svc, cat
	app.frontendFS, err = frontend.DistFS()
	if err != nil {
		t.Fatal(err)
	}
	// Surface the deploy service's own error so a journey failure names the
	// cause instead of the generic handler message.
	probeSvc := svc
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	// The broker monitor and the log poller run as in production (run.go):
	// the connection test of the journey proves a real client's connection
	// and operation only through what they observe on the broker. They stop
	// before the server closes, so no event outlives the journey.
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	var monitors sync.WaitGroup
	monitors.Add(2)
	go func() { defer monitors.Done(); app.StartBrokerMonitor(monitorCtx, brokerCfg) }()
	go func() { defer monitors.Done(); app.StartBrokerLogPoller(monitorCtx) }()
	defer func() { stopMonitor(); monitors.Wait() }()
	if _, err := probeSvc.Preview(ctx, "admin"); err != nil {
		t.Fatalf("deploy preview unavailable in the fixture: %v", err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "frontend/e2e/configuration-journey.mjs"))
	// The journey runs a real MQTT client from the broker image (docker exec)
	// with the created credentials; the service account proves the denial
	// independently of the UI.
	command.Env = append(os.Environ(), "MCM_UI_URL="+server.URL, "MCM_E2E_BROKER_CONTAINER="+name, "MCM_E2E_SERVICE_PASSWORD="+servicePassword, "PLAYWRIGHT_BROWSERS_PATH="+filepath.Join(root, "frontend/node_modules/.cache/ms-playwright"))
	if browserPath := os.Getenv("PLAYWRIGHT_CHROMIUM_PATH"); browserPath != "" {
		command.Env = append(command.Env, "PLAYWRIGHT_CHROMIUM_PATH="+browserPath)
	}
	output, err := command.CombinedOutput()
	t.Logf("browser journey:\n%s", output)
	// The broker's own record of the test client: the line the connection
	// stage was derived from (it names the account, never the password).
	if brokerLog, readErr := os.ReadFile(brokerCfg.LogPath); readErr == nil {
		for _, line := range strings.Split(string(brokerLog), "\n") {
			if strings.Contains(line, "New client connected") && strings.Contains(line, "u'probe'") {
				t.Logf("broker log: %s", line)
			}
		}
	}
	if err != nil {
		t.Fatalf("browser journey: %v", err)
	}
	// All modifications after startup came through the UI. Recovery must restore
	// the original root bytes, not just a stored row or a successful HTTP status.
	restored, err := os.ReadFile(filepath.Join(directory, "mosquitto.conf"))
	if err != nil || string(restored) != original {
		t.Fatalf("root not recovered: %q %v", restored, err)
	}
}
