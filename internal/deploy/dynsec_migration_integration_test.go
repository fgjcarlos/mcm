//go:build integration

// Package deploy_test contains the integration test of the migration
// from the file backend to the dynamic security backend.
//
// The test runs against a real Mosquitto broker, because only the broker
// can prove that the plugin takes the access control over and that the
// migrated clients and roles are the ones the broker enforces.
//
// The broker is started by the test itself. It uses the `mosquitto`
// binary when MOSQUITTO_BIN points at one, and the eclipse-mosquitto
// container image otherwise. The test is skipped when neither is
// available, or when the broker does not ship the dynamic security
// plugin.
package deploy

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
)

// adminUser is the account the broker starts with.
const (
	adminUser     = "mcm-admin"
	adminPassword = "mcm-admin-password"
)

// pluginModule is the path of the plugin inside the container image.
const pluginModule = "/usr/lib/mosquitto_dynamic_security.so"

// broker is a running Mosquitto instance.
type broker struct {
	addr      string
	container string
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	// dir is the directory the configuration of the broker lives in.
	dir string
	// t is the test the broker belongs to.
	t *testing.T
}

// stop ends the broker.
func (b *broker) stop(t *testing.T) {
	t.Helper()
	if b.container != "" {
		if err := exec.Command("docker", "rm", "--force", b.container).Run(); err != nil {
			t.Logf("remove the broker container: %v", err)
		}
		return
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.cmd != nil {
		_ = b.cmd.Wait()
	}
}

// restart restarts the broker so that it reads the configuration again.
//
// A plugin is loaded at start-up, so the broker has to come back for the
// dynamic security plugin to take over.
func (b *broker) restart(t *testing.T) {
	t.Helper()
	if b.container != "" {
		if err := exec.Command("docker", "restart", b.container).Run(); err != nil {
			t.Fatalf("restart the broker: %v", err)
		}
	} else {
		if err := exec.Command("kill", "-HUP", fmt.Sprint(b.cmd.Process.Pid)).Run(); err != nil {
			t.Fatalf("signal the broker: %v", err)
		}
	}
	waitForBroker(t, b.addr)
}

// freePort returns a port that is free at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer listener.Close() //nolint:errcheck
	return listener.Addr().(*net.TCPAddr).Port
}

// waitForBroker waits until the broker accepts connections.
func waitForBroker(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the broker at %s did not accept connections", addr)
}

// startBroker starts a Mosquitto broker with the file backend.
func startBroker(t *testing.T, dir string, port int) *broker {
	t.Helper()

	// The password file of the broker holds the administration account.
	// The hash is the one the plugin produces, so the migration can move
	// the account over without changing its credential.
	hash, salt, err := dynsec.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("hash the administration password: %v", err)
	}
	passwd := fmt.Sprintf("%s:$7$101$%s$%s\n", adminUser, salt, hash)
	if err := os.WriteFile(filepath.Join(dir, "passwd"), []byte(passwd), 0o644); err != nil {
		t.Fatalf("write the password file: %v", err)
	}
	acl := "user " + adminUser + "\ntopic readwrite #\n"
	if err := os.WriteFile(filepath.Join(dir, "acl"), []byte(acl), 0o644); err != nil {
		t.Fatalf("write the ACL file: %v", err)
	}

	body := fmt.Sprintf(`listener %d 127.0.0.1
allow_anonymous false
persistence false
password_file /mosquitto/config/passwd
acl_file /mosquitto/config/acl
`, port)

	if bin := strings.TrimSpace(os.Getenv("MOSQUITTO_BIN")); bin != "" {
		return startLocalBroker(t, dir, port, body, bin)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		requireBroker(t)
		t.Skip("neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker")
	}
	return startDockerBroker(t, dir, port, body)
}

// requireBroker fails the test when no broker is available.
//
// The test skips itself when the environment has neither a mosquitto
// binary nor docker, so a developer without them is not blocked. CI sets
// MCM_REQUIRE_BROKER: there a missing broker is a broken pipeline, not a
// green run that proved nothing.
func requireBroker(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("MCM_REQUIRE_BROKER")) == "" {
		return
	}
	t.Fatal("no broker is available: set MOSQUITTO_BIN or make docker available, or unset MCM_REQUIRE_BROKER to skip")
}

// startLocalBroker starts the mosquitto binary with the given
// configuration.
func startLocalBroker(t *testing.T, dir string, port int, body string, bin string) *broker {
	t.Helper()

	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-c", confPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mosquitto: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForBroker(t, addr)
	return &broker{addr: addr, cmd: cmd, cancel: cancel, dir: dir}
}

// startDockerBroker starts the eclipse-mosquitto container with the given
// configuration.
func startDockerBroker(t *testing.T, dir string, port int, body string) *broker {
	t.Helper()

	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o777); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("relax the configuration directory: %v", err)
	}

	name := fmt.Sprintf("mcm-dynsec-migration-it-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--network", "host",
		"--volume", fmt.Sprintf("%s:/mosquitto/config", dir),
		"--volume", fmt.Sprintf("%s:/mosquitto/data", dataDir),
		"eclipse-mosquitto:2.0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start the mosquitto container: %v: %s", err, string(out))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForBroker(t, addr)
	return &broker{addr: addr, container: name, dir: dir}
}

// restartApplier restarts the broker when the deployment needs it.
type restartApplier struct {
	broker *broker
}

func (a *restartApplier) Apply(context.Context, mosquitto.ApplyRequest) error { return nil }

func (a *restartApplier) ReloadBrokerOnly() error { return nil }

func (a *restartApplier) RestartBroker(_ context.Context) error {
	a.broker.restart(a.broker.t)
	return nil
}

// TestTheMigrationMovesTheFileBackendToThePlugin proves the whole
// migration against a real broker: the operator adopts the dynamic
// security backend, the configuration loads the plugin, the broker comes
// back and the migrated clients are the ones the broker enforces.
func TestTheMigrationMovesTheFileBackendToThePlugin(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)
	b.t = t

	confPath := filepath.Join(dir, "mosquitto.conf")
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	pluginCat, err := plugins.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load plugin catalog: %v", err)
	}

	svc := NewService(
		&restartApplier{broker: b},
		nil,
		nil,
		newFakeDeploymentStore(),
		nil,
		nil,
		config.MosquittoConfig{
			Host:            "127.0.0.1",
			Port:            port,
			Username:        adminUser,
			Password:        adminPassword,
			ConfigDir:       dir,
			BrokerConfigDir: "/mosquitto/config",
		},
		config.DeployConfig{Mode: "file"},
		nil,
	)
	svc.brokerCfgStore = newFakeBrokerConfigStore()
	svc.WithBrokerConfig(confPath, cat)
	svc.WithPluginCatalog(pluginCat)
	svc.WithDynSec(func(context.Context) (dynsec.Transport, func(), error) {
		adapter := dynsec.NewAdapter(dynsec.Options{
			Address:  b.addr,
			Username: adminUser,
			Password: adminPassword,
			ClientID: fmt.Sprintf("mcm-migration-it-%d", time.Now().UnixNano()),
			Timeout:  15 * time.Second,
		})
		if err := adapter.Connect(context.Background()); err != nil {
			return nil, nil, err
		}
		return adapter, adapter.Close, nil
	})

	ctx := context.Background()

	// The broker still uses the file backend.
	before, err := svc.GetDynSec(ctx)
	if err != nil {
		t.Fatalf("GetDynSec: %v", err)
	}
	if before.Backend != AccessBackendFile {
		t.Fatalf("backend = %q, want file", before.Backend)
	}

	// The operator adopts the dynamic security backend.
	adoption, err := svc.AdoptDynSec(ctx, "admin")
	if err != nil {
		t.Fatalf("AdoptDynSec: %v", err)
	}
	if adoption.Kind != "adopt" {
		t.Errorf("kind = %q, want adopt", adoption.Kind)
	}
	if !adoption.Guard.OK {
		t.Errorf("the guard reports a violation: %v", adoption.Guard.Reasons)
	}
	if adoption.ConfigRevisionID == "" {
		t.Fatal("the adoption has no configuration revision")
	}

	// The database of the plugin is written with the management account.
	database := filepath.Join(dir, "dynamic-security.json")
	if _, err := os.Stat(database); err != nil {
		t.Fatalf("the database of the plugin was not written: %v", err)
	}

	// Applying the plan loads the plugin and restarts the broker.
	if _, err := svc.ApplyDynSec(ctx, "admin", adoption.PlanID, nil); err != nil {
		t.Fatalf("ApplyDynSec: %v", err)
	}

	// The broker loads the plugin now.
	after, err := svc.GetDynSec(ctx)
	if err != nil {
		t.Fatalf("GetDynSec: %v", err)
	}
	if after.Backend != AccessBackendDynamicSecurity {
		t.Fatalf("backend = %q, want dynamic-security", after.Backend)
	}
	if !after.Enabled {
		t.Error("the plugin is not enabled")
	}

	// The management account survives the migration and may reach the
	// control channel.
	if _, ok := after.Document.FindClient(adminUser); !ok {
		t.Fatalf("the management account is missing: %+v", after.Document.Clients)
	}
	if !after.Guard.OK {
		t.Errorf("the guard reports a violation: %v", after.Guard.Reasons)
	}

	// The broker enforces the migrated state: the administration account
	// may still publish and subscribe.
	client, err := connect(t, b.addr, adminUser, adminPassword)
	if err != nil {
		t.Fatalf("the migrated account cannot connect: %v", err)
	}
	defer client.Disconnect(250)
	token := client.Subscribe("sensors/#", 1, nil)
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("the subscription timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("the migrated account cannot subscribe: %v", err)
	}
}

// connect opens an MQTT connection to the broker.
func connect(t *testing.T, addr, username, password string) (mqtt.Client, error) {
	t.Helper()
	options := mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(fmt.Sprintf("mcm-migration-it-%d", time.Now().UnixNano())).
		SetConnectTimeout(10 * time.Second)
	if username != "" {
		options.SetUsername(username)
		options.SetPassword(password)
	}
	client := mqtt.NewClient(options)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("the connection to %s timed out", addr)
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	return client, nil
}
