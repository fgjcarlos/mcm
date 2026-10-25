package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fakeBrokerConfigStore keeps the broker-config revisions in memory.
type fakeBrokerConfigStore struct {
	revisions map[string]storage.BrokerConfigRevision
	adoptions []storage.BrokerConfigAdoption
	nextAdopt int64
}

func newFakeBrokerConfigStore() *fakeBrokerConfigStore {
	return &fakeBrokerConfigStore{revisions: map[string]storage.BrokerConfigRevision{}}
}

func (f *fakeBrokerConfigStore) InsertBrokerConfigRevision(_ context.Context, r *storage.BrokerConfigRevision) error {
	f.revisions[r.ID] = *r
	return nil
}

func (f *fakeBrokerConfigStore) GetBrokerConfigRevision(_ context.Context, id string) (storage.BrokerConfigRevision, error) {
	r, ok := f.revisions[id]
	if !ok {
		return storage.BrokerConfigRevision{}, storage.ErrBrokerConfigRevisionNotFound
	}
	return r, nil
}

func (f *fakeBrokerConfigStore) ConsumeBrokerConfigRevision(_ context.Context, id string, appliedAt time.Time) error {
	r, ok := f.revisions[id]
	if !ok {
		return storage.ErrBrokerConfigRevisionNotFound
	}
	if !r.AppliedAt.IsZero() {
		return storage.ErrBrokerConfigRevisionConsumed
	}
	r.AppliedAt = appliedAt
	f.revisions[id] = r
	return nil
}

func (f *fakeBrokerConfigStore) InsertBrokerConfigAdoption(_ context.Context, sourcePath, adoptedBy string) (storage.BrokerConfigAdoption, error) {
	f.nextAdopt++
	a := storage.BrokerConfigAdoption{
		ID:         f.nextAdopt,
		SourcePath: sourcePath,
		AdoptedBy:  adoptedBy,
		AdoptedAt:  time.Now().UTC(),
	}
	f.adoptions = append(f.adoptions, a)
	return a, nil
}

func (f *fakeBrokerConfigStore) LatestBrokerConfigAdoption(_ context.Context, sourcePath string) (storage.BrokerConfigAdoption, error) {
	for i := len(f.adoptions) - 1; i >= 0; i-- {
		if f.adoptions[i].SourcePath == sourcePath {
			return f.adoptions[i], nil
		}
	}
	return storage.BrokerConfigAdoption{}, nil
}

// settingsService wires a deploy service with the broker-config path
// enabled and a configuration file holding the given body.
func settingsService(t *testing.T, body string) (*Service, *fakeBrokerConfigStore, string) {
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
		&fakeApplier{},
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

const settingsConf = `# MCM configuration
persistence true
persistence_location /mosquitto/data/

log_dest stdout
log_type error
log_type warning

user root

listener 1883 0.0.0.0
`

func TestGetBrokerSettings(t *testing.T) {
	svc, _, confPath := settingsService(t, settingsConf)
	result, err := svc.GetBrokerSettings(context.Background())
	if err != nil {
		t.Fatalf("GetBrokerSettings: %v", err)
	}
	if result.Path != confPath {
		t.Errorf("path = %q, want %q", result.Path, confPath)
	}
	if result.Version != "2.0" {
		t.Errorf("version = %q, want 2.0", result.Version)
	}
	values, ok := result.Document.Values("persistence")
	if !ok || len(values) != 1 || values[0] != "true" {
		t.Errorf("persistence = %v (present=%v), want [true]", values, ok)
	}
	if len(result.Sections) != 4 {
		t.Fatalf("sections = %d, want 4", len(result.Sections))
	}
	ids := map[string]bool{}
	for _, s := range result.Sections {
		ids[s.ID] = true
		if len(s.Fields) == 0 {
			t.Errorf("section %q has no fields", s.ID)
		}
	}
	for _, want := range []string{catalog.SectionPersistence, catalog.SectionQueues, catalog.SectionSessions, catalog.SectionLogging} {
		if !ids[want] {
			t.Errorf("section %q is missing", want)
		}
	}
}

func TestPreviewBrokerSettings_ChangedDirective(t *testing.T) {
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"false"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if len(preview.ChangedDirectives) != 1 || preview.ChangedDirectives[0] != "persistence" {
		t.Errorf("changed = %v, want [persistence]", preview.ChangedDirectives)
	}
	if !preview.HasChanges {
		t.Error("has_changes = false, want true")
	}
	if !strings.Contains(preview.ConfDiff, "persistence false") {
		t.Errorf("diff does not mention the new value:\n%s", preview.ConfDiff)
	}
}

func TestPreviewBrokerSettings_ReloadOnlyChange(t *testing.T) {
	// The configuration holds "user root", which Mosquitto only reads at
	// start-up. Changing a reload-only setting must not ask for a
	// restart.
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"false"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if preview.RequiresRestart {
		t.Error("requires_restart = true, want false for a reload-only change")
	}
	if preview.ReloadKind != "reload" {
		t.Errorf("reload_kind = %q, want reload", preview.ReloadKind)
	}
}

func TestPreviewBrokerSettings_RestartChange(t *testing.T) {
	// max_connections is bound to the listener, so Mosquitto needs a
	// restart to pick it up.
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "max_connections", Values: []string{"100"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if !preview.RequiresRestart {
		t.Error("requires_restart = false, want true for a listener bound directive")
	}
	if preview.ReloadKind != "restart" {
		t.Errorf("reload_kind = %q, want restart", preview.ReloadKind)
	}
}

func TestPreviewBrokerSettings_InvalidDocument(t *testing.T) {
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "max_inflight_messages", Values: []string{"70000"}},
	}}
	_, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err == nil {
		t.Fatal("expected an error for an out-of-range value")
	}
	if !errors.Is(err, ErrSettingsInvalid) {
		t.Errorf("err = %v, want ErrSettingsInvalid", err)
	}
	var settingsErr *SettingsError
	if !errors.As(err, &settingsErr) {
		t.Fatalf("err = %v, want a *SettingsError", err)
	}
	if len(settingsErr.Issues) == 0 {
		t.Error("the settings error carries no issues")
	}
}

func TestPreviewBrokerSettings_UnknownDirective(t *testing.T) {
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "allow_anonymous", Values: []string{"true"}},
	}}
	if _, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc); !errors.Is(err, ErrSettingsInvalid) {
		t.Errorf("err = %v, want ErrSettingsInvalid", err)
	}
}

func TestPreviewBrokerSettings_RecordsChangeSet(t *testing.T) {
	svc, store, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"false"}},
		{Directive: "max_queued_messages", Values: []string{"5000"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	rev, err := store.GetBrokerConfigRevision(context.Background(), preview.RevisionID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if rev.ChangedDirectives == nil {
		t.Fatal("the revision does not record a change set")
	}
	got := strings.Split(*rev.ChangedDirectives, ",")
	if len(got) != 2 || got[0] != "max_queued_messages" || got[1] != "persistence" {
		t.Errorf("changed directives = %v, want [max_queued_messages persistence]", got)
	}
}

func TestApplyBrokerSettings_WritesTheFile(t *testing.T) {
	svc, _, confPath := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"false"}},
		{Directive: "autosave_interval", Values: []string{"60"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if _, err := svc.ApplyBrokerConfig(context.Background(), "operator", preview.RevisionID, false, true); err != nil {
		t.Fatalf("ApplyBrokerConfig: %v", err)
	}
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	got := string(body)
	if !strings.Contains(got, "persistence false") {
		t.Errorf("the file was not updated:\n%s", got)
	}
	if !strings.Contains(got, "autosave_interval 60") {
		t.Errorf("the new directive was not written:\n%s", got)
	}
	// The rest of the file must survive the apply.
	if !strings.Contains(got, "user root") || !strings.Contains(got, "listener 1883 0.0.0.0") {
		t.Errorf("the apply dropped part of the file:\n%s", got)
	}
}

func TestApplyBrokerSettings_RefusesRestartWithoutForce(t *testing.T) {
	svc, _, confPath := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "max_connections", Values: []string{"100"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if _, err := svc.ApplyBrokerConfig(context.Background(), "operator", preview.RevisionID, false, true); !errors.Is(err, ErrRequiresRestart) {
		t.Errorf("err = %v, want ErrRequiresRestart", err)
	}
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	if strings.Contains(string(body), "max_connections") {
		t.Errorf("the refused apply wrote the file:\n%s", body)
	}
}

func TestApplyBrokerSettings_UnchangedDocumentIsANoOp(t *testing.T) {
	svc, _, _ := settingsService(t, settingsConf)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"true"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	if preview.HasChanges {
		t.Error("has_changes = true, want false when nothing changed")
	}
	if len(preview.ChangedDirectives) != 0 {
		t.Errorf("changed = %v, want none", preview.ChangedDirectives)
	}
}

func TestSettings_RoundTripKeepsTheFileIntact(t *testing.T) {
	svc, _, confPath := settingsService(t, settingsConf)
	current, err := svc.GetBrokerSettings(context.Background())
	if err != nil {
		t.Fatalf("GetBrokerSettings: %v", err)
	}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", current.Document)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	rev, err := svc.brokerCfgStore.GetBrokerConfigRevision(context.Background(), preview.RevisionID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	original, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	if rev.ConfRendered != string(original) {
		t.Errorf("the preview changed the file:\n--- got ---\n%s\n--- want ---\n%s", rev.ConfRendered, original)
	}
}

func TestSettings_PreservesUnknownDirectivesAndComments(t *testing.T) {
	body := "# a comment\nfuture_option value\npersistence true\n\nlistener 1883 0.0.0.0\n"
	svc, _, _ := settingsService(t, body)
	current, err := svc.GetBrokerSettings(context.Background())
	if err != nil {
		t.Fatalf("GetBrokerSettings: %v", err)
	}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", current.Document)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	rev, err := svc.brokerCfgStore.GetBrokerConfigRevision(context.Background(), preview.RevisionID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if rev.ConfRendered != body {
		t.Errorf("the preview changed the file:\n--- got ---\n%s\n--- want ---\n%s", rev.ConfRendered, body)
	}
}

func TestSettings_WarnsAboutIncludedDuplicates(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("include_dir conf.d\npersistence true\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	incDir := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(incDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(incDir, "extra.conf"), []byte("log_type debug\n"), 0o644); err != nil {
		t.Fatalf("write include: %v", err)
	}
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	svc := newTestService(
		&fakeApplier{}, &fakeACLStore{}, &fakeMQTTUserLister{},
		newFakeDeploymentStore(), diagnostics.VerifierFunc(okVerifier), &fakePasswordLookup{}, config.DeployConfig{Mode: "file"},
	)
	svc.WithBrokerConfig(confPath, cat)
	svc.brokerCfgStore = newFakeBrokerConfigStore()

	result, err := svc.GetBrokerSettings(context.Background())
	if err != nil {
		t.Fatalf("GetBrokerSettings: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Error("warnings = none, want a warning about the included log_type")
	}
}

func TestClassifyChangedDirectives(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	kind, restart := classifyChangedDirectives([]string{"persistence"}, cat)
	if kind != "reload" || restart {
		t.Errorf("classify(persistence) = (%q, %v), want (reload, false)", kind, restart)
	}
	kind, restart = classifyChangedDirectives([]string{"persistence", "max_connections"}, cat)
	if kind != "restart" || !restart {
		t.Errorf("classify(max_connections) = (%q, %v), want (restart, true)", kind, restart)
	}
	kind, restart = classifyChangedDirectives(nil, cat)
	if kind != "reload" || restart {
		t.Errorf("classify(none) = (%q, %v), want (reload, false)", kind, restart)
	}
}

func TestChangedDirectiveNames(t *testing.T) {
	if got := changedDirectiveNames(""); got != nil {
		t.Errorf("changedDirectiveNames(\"\") = %v, want nil", got)
	}
	got := changedDirectiveNames("persistence, log_type")
	if len(got) != 2 || got[0] != "persistence" || got[1] != "log_type" {
		t.Errorf("changedDirectiveNames = %v, want [persistence log_type]", got)
	}
}

// The settings editor must not touch the include tree: the file it
// writes keeps its include_dir directive.
func TestPreviewBrokerSettings_KeepsTheIncludeTree(t *testing.T) {
	body := "include_dir /mosquitto/config/conf.d\npersistence true\n"
	svc, store, _ := settingsService(t, body)
	doc := settings.Document{Entries: []settings.Entry{
		{Directive: "persistence", Values: []string{"false"}},
	}}
	preview, err := svc.PreviewBrokerSettings(context.Background(), "operator", doc)
	if err != nil {
		t.Fatalf("PreviewBrokerSettings: %v", err)
	}
	rev, err := store.GetBrokerConfigRevision(context.Background(), preview.RevisionID)
	if err != nil {
		t.Fatalf("get revision: %v", err)
	}
	if !strings.Contains(rev.ConfRendered, "include_dir") {
		t.Errorf("the preview dropped the include tree:\n%s", rev.ConfRendered)
	}
	parsed, err := conf.ParseString(rev.ConfRendered, "mosquitto.conf")
	if err != nil {
		t.Fatalf("parse rendered: %v", err)
	}
	if len(parsed.Items) == 0 {
		t.Error("the rendered config is empty")
	}
}
