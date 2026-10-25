package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/bridges"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

const bridgesConf = `# MCM configuration
persistence true

listener 1883 0.0.0.0
allow_anonymous true

connection upstream
address remote.example.org:1884
remote_username mcm
remote_password topsecret
topic both/# both 1
`

// bridgeService wires a deploy service with the broker-config path enabled
// and a configuration file holding the given body.
func bridgeService(t *testing.T, body string) (*Service, *fakeBrokerConfigStore, string) {
	t.Helper()
	return bridgeServiceWith(t, &fakeApplier{}, body)
}

// bridgeServiceWith wires a deploy service with the given applier.
func bridgeServiceWith(t *testing.T, applier mosquitto.Applier, body string) (*Service, *fakeBrokerConfigStore, string) {
	t.Helper()
	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	store := newFakeBrokerConfigStore()
	svc := newTestService(
		applier,
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.DeployConfig{Mode: "file"},
	)
	svc.WithBrokerConfig(confPath, cat)
	svc.brokerCfgStore = store
	return svc, store, confPath
}

// reloadOnlyApplier is an applier that cannot restart the broker.
type reloadOnlyApplier struct{}

func (reloadOnlyApplier) Apply(context.Context, mosquitto.ApplyRequest) error { return nil }
func (reloadOnlyApplier) ReloadBrokerOnly() error                             { return nil }

func TestGetBridgesRedactsSecrets(t *testing.T) {
	svc, _, _ := bridgeService(t, bridgesConf)
	result, err := svc.GetBridges(context.Background())
	if err != nil {
		t.Fatalf("GetBridges: %v", err)
	}
	if len(result.Bridges) != 1 {
		t.Fatalf("bridges = %d, want 1", len(result.Bridges))
	}
	bridge := result.Bridges[0]
	if bridge.Name != "upstream" {
		t.Errorf("name = %q, want upstream", bridge.Name)
	}
	if bridge.Address != "remote.example.org:1884" {
		t.Errorf("address = %q", bridge.Address)
	}
	if bridge.Password != "" {
		t.Errorf("the password leaked into the response: %q", bridge.Password)
	}
	if !bridge.PasswordSet {
		t.Errorf("password_set = false, want true")
	}
	if bridge.Username != "mcm" {
		t.Errorf("username = %q, want mcm", bridge.Username)
	}
}

func TestPreviewBridgesRendersTheConnection(t *testing.T) {
	svc, store, confPath := bridgeService(t, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:             "upstream",
		Address:          "remote.example.org:1884",
		RemoteClientID:   "mcm-upstream",
		Username:         "mcm",
		Password:         "topsecret",
		KeepaliveSeconds: intPtr(45),
		Topics: []bridges.TopicRule{
			{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1},
		},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	if preview.Kind != "bridges" {
		t.Errorf("kind = %q, want bridges", preview.Kind)
	}
	if preview.RevisionID == "" {
		t.Fatal("the preview has no revision id")
	}
	if !strings.Contains(preview.ConfBody, "connection upstream") {
		t.Errorf("the rendered body is missing the connection:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "keepalive_interval 45") {
		t.Errorf("the rendered body is missing the keepalive:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "listener 1883 0.0.0.0") {
		t.Errorf("the rendered body lost the listener:\n%s", preview.ConfBody)
	}
	if !preview.RequiresRestart {
		t.Error("requires_restart = false, want true: Mosquitto reads a bridge at start-up")
	}
	if preview.ReloadKind != "restart" {
		t.Errorf("reload_kind = %q, want restart", preview.ReloadKind)
	}
	if len(preview.ChangedDirectives) == 0 {
		t.Error("changed_directives is empty")
	}
	if _, ok := store.revisions[preview.RevisionID]; !ok {
		t.Error("the revision was not stored")
	}
	// The preview must not change the file on disk.
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	if string(body) != bridgesConf {
		t.Errorf("the preview changed the file on disk:\n%s", body)
	}
}

func TestPreviewBridgesRedactsTheSecret(t *testing.T) {
	svc, _, _ := bridgeService(t, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:     "upstream",
		Address:  "remote.example.org:1884",
		Username: "mcm",
		Password: "topsecret",
		Topics:   []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	if strings.Contains(preview.ConfBody, "topsecret") {
		t.Errorf("the rendered body leaks the password:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "remote_password ********") {
		t.Errorf("the rendered body does not mask the password:\n%s", preview.ConfBody)
	}
	if strings.Contains(preview.ConfDiff, "topsecret") {
		t.Errorf("the diff leaks the password:\n%s", preview.ConfDiff)
	}
}

func TestPreviewBridgesKeepsTheStoredSecret(t *testing.T) {
	svc, _, _ := bridgeService(t, bridgesConf)
	// The operator edits the address but does not resend the password:
	// the API never returns it, so the preview must carry it over.
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:     "upstream",
		Address:  "other.example.org:1884",
		Username: "mcm",
		Topics:   []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	if !strings.Contains(preview.ConfBody, "remote_password ********") {
		t.Errorf("the password was dropped:\n%s", preview.ConfBody)
	}
	if !strings.Contains(preview.ConfBody, "address other.example.org:1884") {
		t.Errorf("the address was not applied:\n%s", preview.ConfBody)
	}
}

func TestPreviewBridgesRejectsAnInvalidDocument(t *testing.T) {
	svc, _, _ := bridgeService(t, bridgesConf)
	// A bridge that connects the broker to its own listener loops every
	// message.
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:    "self",
		Address: "0.0.0.0:1883",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	_, err := svc.PreviewBridges(context.Background(), "operator", doc)
	var bridgesErr *BridgesError
	if !errors.As(err, &bridgesErr) {
		t.Fatalf("error = %v, want a BridgesError", err)
	}
	if len(bridgesErr.Issues) == 0 {
		t.Error("the error carries no issue")
	}
	if bridgesErr.Issues[0].Kind != bridges.IssueKindLoop {
		t.Errorf("kind = %q, want loop", bridgesErr.Issues[0].Kind)
	}
}

func TestPreviewBridgesRejectsADuplicateName(t *testing.T) {
	svc, _, _ := bridgeService(t, bridgesConf)
	bridge := bridges.Bridge{
		Name:    "upstream",
		Address: "remote.example.org:1884",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}
	_, err := svc.PreviewBridges(context.Background(), "operator", bridges.Document{Bridges: []bridges.Bridge{bridge, bridge}})
	var bridgesErr *BridgesError
	if !errors.As(err, &bridgesErr) {
		t.Fatalf("error = %v, want a BridgesError", err)
	}
}

func TestApplyBridgesWritesTheFileAndVerifiesTheActivation(t *testing.T) {
	applier := &fakeApplier{}
	svc, _, confPath := bridgeServiceWith(t, applier, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:     "upstream",
		Address:  "remote.example.org:1884",
		Username: "mcm",
		Password: "topsecret",
		Topics:   []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	dep, err := svc.ApplyBridges(context.Background(), "admin", preview.RevisionID, true, true)
	if err != nil {
		t.Fatalf("ApplyBridges: %v", err)
	}
	if dep.Status != "active_verified" {
		t.Errorf("status = %q, want active_verified", dep.Status)
	}
	if dep.Kind != "bridges" {
		t.Errorf("kind = %q, want bridges", dep.Kind)
	}
	// Mosquitto reads a bridge at start-up, so the apply restarts the
	// broker instead of reloading it.
	if applier.restartCount() != 1 {
		t.Errorf("restarts = %d, want 1", applier.restartCount())
	}
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	if !strings.Contains(string(body), "connection upstream") {
		t.Errorf("the file does not carry the bridge:\n%s", body)
	}
	if !strings.Contains(string(body), "remote_password topsecret") {
		t.Errorf("the file does not carry the password:\n%s", body)
	}
	if !strings.Contains(string(body), "listener 1883 0.0.0.0") {
		t.Errorf("the file lost the listener:\n%s", body)
	}
}

func TestApplyBridgesReportsPendingActivationWithoutARestart(t *testing.T) {
	// A deployment that cannot restart the broker writes the bridge and
	// says that the broker still runs the previous connection set.
	svc, _, confPath := bridgeServiceWith(t, reloadOnlyApplier{}, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:    "upstream",
		Address: "remote.example.org:1884",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	dep, err := svc.ApplyBridges(context.Background(), "admin", preview.RevisionID, true, true)
	if err != nil {
		t.Fatalf("ApplyBridges: %v", err)
	}
	if dep.Status != "pending_activation" {
		t.Errorf("status = %q, want pending_activation", dep.Status)
	}
	body, _ := os.ReadFile(confPath)
	if !strings.Contains(string(body), "connection upstream") {
		t.Errorf("the file does not carry the bridge:\n%s", body)
	}
}

func TestApplyBridgesRefusesWithoutForce(t *testing.T) {
	svc, _, confPath := bridgeService(t, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:    "upstream",
		Address: "remote.example.org:1884",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	_, err = svc.ApplyBridges(context.Background(), "admin", preview.RevisionID, false, true)
	if !errors.Is(err, ErrRequiresRestart) {
		t.Fatalf("error = %v, want ErrRequiresRestart", err)
	}
	body, _ := os.ReadFile(confPath)
	if string(body) != bridgesConf {
		t.Errorf("the refused apply changed the file:\n%s", body)
	}
}

func TestApplyBridgesRejectsAStaleRevision(t *testing.T) {
	svc, _, confPath := bridgeService(t, bridgesConf)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:    "upstream",
		Address: "remote.example.org:1884",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	// Somebody edits the file after the preview.
	if err := os.WriteFile(confPath, []byte(bridgesConf+"# touched\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	_, err = svc.ApplyBridges(context.Background(), "admin", preview.RevisionID, true, true)
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("error = %v, want ErrRevisionMismatch", err)
	}
}

func TestPreviewBridgesPreservesTheRestOfTheFile(t *testing.T) {
	body := `# header comment
listener 1883 0.0.0.0
allow_anonymous true

# an unmanaged bridge
connection legacy
address legacy.example.org:1883
topic legacy/# both 1

persistence true
`
	svc, _, _ := bridgeService(t, body)
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:    "upstream",
		Address: "remote.example.org:1884",
		Topics:  []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
	}}}
	preview, err := svc.PreviewBridges(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBridges: %v", err)
	}
	for _, want := range []string{"# header comment", "listener 1883 0.0.0.0", "persistence true"} {
		if !strings.Contains(preview.ConfBody, want) {
			t.Errorf("the rendered body lost %q:\n%s", want, preview.ConfBody)
		}
	}
	if strings.Contains(preview.ConfBody, "connection legacy") {
		t.Errorf("the replaced bridge is still present:\n%s", preview.ConfBody)
	}
}

func TestChangedConnectionDirectives(t *testing.T) {
	before, err := conf.ParseString("connection a\naddress one:1883\ntopic both/# both 1\n", "x")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	after, err := conf.ParseString("connection a\naddress two:1883\ntopic both/# both 1\n", "x")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	changed := changedConnectionDirectives(before, after)
	if len(changed) != 1 || changed[0] != "address" {
		t.Errorf("changed = %v, want [address]", changed)
	}

	same := changedConnectionDirectives(before, before)
	if len(same) != 0 {
		t.Errorf("changed = %v, want none", same)
	}
}

func TestRedactBridgeSecrets(t *testing.T) {
	body := "    remote_password hunter2\n    local_password hunter3\n    bridge_psk hunter4\n    address broker:1883\n"
	got := redactBridgeSecrets(body)
	for _, secret := range []string{"hunter2", "hunter3", "hunter4"} {
		if strings.Contains(got, secret) {
			t.Errorf("the secret %q survived:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, "address broker:1883") {
		t.Errorf("the redaction touched a harmless line:\n%s", got)
	}
}

func intPtr(v int) *int { return &v }
