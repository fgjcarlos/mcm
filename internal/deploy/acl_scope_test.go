package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// scopedDeploymentStore is a fake deployment store that also owns the
// registry of the per-listener ACL files and the listener specs, so the
// scoped ACL behaviour can be exercised end to end.
type scopedDeploymentStore struct {
	*fakeDeploymentStore
	mu      sync.Mutex
	managed map[string]string
	specs   []storage.ListenerSpecRow
}

func newScopedDeploymentStore() *scopedDeploymentStore {
	return &scopedDeploymentStore{
		fakeDeploymentStore: newFakeDeploymentStore(),
		managed:             map[string]string{},
	}
}

func (s *scopedDeploymentStore) ListManagedACLFiles(_ context.Context) ([]storage.ManagedACLFileRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]storage.ManagedACLFileRow, 0, len(s.managed))
	for path, listenerID := range s.managed {
		out = append(out, storage.ManagedACLFileRow{Path: path, ListenerID: listenerID})
	}
	return out, nil
}

func (s *scopedDeploymentStore) RecordManagedACLFile(_ context.Context, path, listenerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.managed[path] = listenerID
	return nil
}

func (s *scopedDeploymentStore) DeleteManagedACLFile(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.managed[path]; !ok {
		return storage.ErrManagedACLFileNotFound
	}
	delete(s.managed, path)
	return nil
}

func (s *scopedDeploymentStore) ListListenerSpecs(_ context.Context) ([]storage.ListenerSpecRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.specs, nil
}

// setListener registers a listener spec that declares its own ACL file.
func (s *scopedDeploymentStore) setListener(id, aclFile string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.specs = append(s.specs, storage.ListenerSpecRow{
		ID:      id,
		Port:    8883,
		Bind:    "0.0.0.0",
		Options: map[string]string{"acl_file": aclFile},
	})
}

// managedPaths returns the registered per-listener ACL files.
func (s *scopedDeploymentStore) managedPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.managed))
	for path := range s.managed {
		out = append(out, path)
	}
	return out
}

// newScopedService builds a deploy service whose ACL store holds the given
// rules.
//
// The service is seeded with a managed MQTT user that owns a readwrite
// grant, so the deploy verifier finds a probe identity and the apply can
// reach the verified state.
func newScopedService(t *testing.T, rules []acl.Rule) (*Service, *scopedDeploymentStore, config.DeployConfig) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DeployConfig{
		Mode:               "file",
		ACLPath:            filepath.Join(dir, "acl"),
		PasswdPath:         filepath.Join(dir, "passwd"),
		HealthcheckTimeout: 5 * time.Second,
	}
	hash, err := mosquitto.HashPassword("verify-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	store := acl.NewMemoryStore()
	seeded := append([]acl.Rule{{
		Principal:   "verify-user",
		TopicFilter: "verify/topic",
		Permission:  acl.PermissionReadWrite,
	}}, rules...)
	for _, rule := range seeded {
		if _, err := store.CreateRule(context.Background(), rule); err != nil {
			t.Fatalf("CreateRule() error = %v", err)
		}
	}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "verify-user", PasswordHash: hash},
	}}
	pwLookup := newFakePasswordLookup()
	pwLookup.remember("verify-user", "verify-pass")
	deployStore := newScopedDeploymentStore()
	svc := NewService(
		&fakeApplier{},
		store,
		mqttStore,
		deployStore,
		diagnostics.VerifierFunc(okVerifier),
		pwLookup,
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		cfg,
		noAudit,
	)
	return svc, deployStore, cfg
}

// TestRenderScopedRuleGoesToTheListenerFile pins that a listener-scoped
// rule is rendered into the file of that listener and not into the
// broker-wide file.
func TestRenderScopedRuleGoesToTheListenerFile(t *testing.T) {
	t.Parallel()

	svc, _, cfg := newScopedService(t, []acl.Rule{
		{Principal: "alice", TopicFilter: "global/#", Permission: acl.PermissionRead},
		{Scope: acl.ScopeListener, ListenerID: "tls", Principal: "bob", TopicFilter: "tls/#", Permission: acl.PermissionRead},
	})

	rendered, err := svc.renderConfig(context.Background())
	if err != nil {
		t.Fatalf("renderConfig() error = %v", err)
	}
	if !strings.Contains(rendered.ACLBody, "topic read global/#") {
		t.Fatalf("the broker-wide file is missing the global rule:\n%s", rendered.ACLBody)
	}
	if strings.Contains(rendered.ACLBody, "tls/#") {
		t.Fatalf("the broker-wide file carries the listener rule:\n%s", rendered.ACLBody)
	}
	if len(rendered.ACLFiles) != 2 {
		t.Fatalf("renderConfig() produced %d acl files, want 2: %#v", len(rendered.ACLFiles), rendered.ACLFiles)
	}
	listenerFile := rendered.ACLFiles[1]
	if listenerFile.ListenerID != "tls" {
		t.Fatalf("the second file belongs to %q, want %q", listenerFile.ListenerID, "tls")
	}
	wantPath := filepath.Join(filepath.Dir(cfg.ACLPath), "acl.tls")
	if listenerFile.Path != wantPath {
		t.Fatalf("the listener file path = %q, want %q", listenerFile.Path, wantPath)
	}
	if !strings.Contains(listenerFile.Body, "topic read tls/#") {
		t.Fatalf("the listener file is missing its rule:\n%s", listenerFile.Body)
	}
}

// TestPreviewReportsTheListenerFile pins that the preview shows the
// per-listener file and the change it carries.
func TestPreviewReportsTheListenerFile(t *testing.T) {
	t.Parallel()

	svc, _, _ := newScopedService(t, []acl.Rule{
		{Scope: acl.ScopeListener, ListenerID: "tls", Principal: "bob", TopicFilter: "tls/#", Permission: acl.PermissionRead},
	})

	preview, err := svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.ListenerACLFiles) != 1 {
		t.Fatalf("the preview reported %d listener files, want 1", len(preview.ListenerACLFiles))
	}
	file := preview.ListenerACLFiles[0]
	if file.ListenerID != "tls" {
		t.Fatalf("the previewed file belongs to %q, want %q", file.ListenerID, "tls")
	}
	if file.Diff == "" {
		t.Fatal("the previewed listener file carries no diff")
	}
	if !preview.HasChanges {
		t.Fatal("the preview reports no changes although the listener file is new")
	}
}

// TestApplyRefusesUnwiredListenerScope pins that an apply whose scoped
// rules would never reach the broker is refused.
//
// Mosquitto only applies the ACL file of a listener when the listener
// declares it, so an apply that ignored this would report success while
// the broker kept serving the broker-wide access.
func TestApplyRefusesUnwiredListenerScope(t *testing.T) {
	t.Parallel()

	svc, _, _ := newScopedService(t, []acl.Rule{
		{Scope: acl.ScopeListener, ListenerID: "tls", Principal: "bob", TopicFilter: "tls/#", Permission: acl.PermissionRead},
	})

	preview, err := svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.ScopeIssues) == 0 {
		t.Fatal("the preview reports no scope issue although the listener declares no acl_file")
	}

	_, err = svc.Apply(context.Background(), "alice", preview.RevisionID)
	if !errors.Is(err, ErrACLScopeNotWired) {
		t.Fatalf("Apply() error = %v, want it to refuse the unwired scope", err)
	}
}

// TestApplyWritesTheListenerFile pins that a wired listener scope writes
// its own file in the same transaction as the broker-wide one.
func TestApplyWritesTheListenerFile(t *testing.T) {
	t.Parallel()

	svc, deployStore, cfg := newScopedService(t, []acl.Rule{
		{Principal: "alice", TopicFilter: "global/#", Permission: acl.PermissionRead},
		{Scope: acl.ScopeListener, ListenerID: "tls", Principal: "bob", TopicFilter: "tls/#", Permission: acl.PermissionRead},
	})
	listenerPath := filepath.Join(filepath.Dir(cfg.ACLPath), "acl.tls")
	deployStore.setListener("tls", listenerPath)

	preview, err := svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.ScopeIssues) != 0 {
		t.Fatalf("the preview reports scope issues for a wired listener: %v", preview.ScopeIssues)
	}

	if _, err := svc.Apply(context.Background(), "alice", preview.RevisionID); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	applier := svc.applier.(*fakeApplier)
	call := applier.callAt(0)
	if len(call.aclFiles) != 1 {
		t.Fatalf("the apply wrote %d listener files, want 1", len(call.aclFiles))
	}
	if call.aclFiles[0].Path != listenerPath {
		t.Fatalf("the apply wrote %q, want %q", call.aclFiles[0].Path, listenerPath)
	}
	if !strings.Contains(call.aclFiles[0].Body, "topic read tls/#") {
		t.Fatalf("the written listener file is missing its rule:\n%s", call.aclFiles[0].Body)
	}

	paths := deployStore.managedPaths()
	if len(paths) != 1 || paths[0] != listenerPath {
		t.Fatalf("the registry holds %v, want [%q]", paths, listenerPath)
	}
}

// TestApplyRemovesTheListenerFileOfARemovedScope pins that the file of a
// listener whose scoped rules are gone is deleted.
//
// The listener would otherwise keep serving the access the operator
// removed, because Mosquitto keeps using the file the listener declares.
func TestApplyRemovesTheListenerFileOfARemovedScope(t *testing.T) {
	t.Parallel()

	svc, deployStore, cfg := newScopedService(t, []acl.Rule{
		{Scope: acl.ScopeListener, ListenerID: "tls", Principal: "bob", TopicFilter: "tls/#", Permission: acl.PermissionRead},
	})
	listenerPath := filepath.Join(filepath.Dir(cfg.ACLPath), "acl.tls")
	deployStore.setListener("tls", listenerPath)
	writeFile(t, listenerPath, "user bob\ntopic read tls/#\n")

	preview, err := svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if _, err := svc.Apply(context.Background(), "alice", preview.RevisionID); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// The operator removes the scoped rule.
	store := svc.aclStore
	rules, err := store.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules() error = %v", err)
	}
	removed := 0
	for _, rule := range rules {
		if !rule.IsListenerScoped() {
			continue
		}
		if err := store.DeleteRule(context.Background(), rule.ID); err != nil {
			t.Fatalf("DeleteRule() error = %v", err)
		}
		removed++
	}
	if removed != 1 {
		t.Fatalf("removed %d scoped rules, want 1", removed)
	}

	preview, err = svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if len(preview.RemovedACLFiles) != 1 || preview.RemovedACLFiles[0] != listenerPath {
		t.Fatalf("the preview reports the removed files %v, want [%q]", preview.RemovedACLFiles, listenerPath)
	}

	if _, err := svc.Apply(context.Background(), "alice", preview.RevisionID); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	applier := svc.applier.(*fakeApplier)
	call := applier.callAt(1)
	if len(call.removedFiles) != 1 || call.removedFiles[0].Path != listenerPath {
		t.Fatalf("the apply removed %v, want [%q]", call.removedFiles, listenerPath)
	}
	if paths := deployStore.managedPaths(); len(paths) != 0 {
		t.Fatalf("the registry still holds %v, want it empty", paths)
	}
}

// TestApplyRestoresTheRemovedListenerFileOnRollback pins that a failed
// apply restores the file of a listener whose scope was removed.
func TestApplyRestoresTheRemovedListenerFileOnRollback(t *testing.T) {
	t.Parallel()

	svc, deployStore, cfg := newScopedService(t, nil)
	listenerPath := filepath.Join(filepath.Dir(cfg.ACLPath), "acl.tls")
	deployStore.setListener("tls", listenerPath)
	deployStore.managed[listenerPath] = "tls"
	writeFile(t, listenerPath, "user bob\ntopic read tls/#\n")

	failing := &fakeApplier{failAll: true}
	svc.applier = failing

	preview, err := svc.Preview(context.Background(), "alice")
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if _, err := svc.Apply(context.Background(), "alice", preview.RevisionID); err == nil {
		t.Fatal("Apply() succeeded although the applier fails")
	}

	if _, statErr := os.Stat(listenerPath); statErr != nil {
		t.Fatalf("the listener file is gone after the rollback: %v", statErr)
	}
}
