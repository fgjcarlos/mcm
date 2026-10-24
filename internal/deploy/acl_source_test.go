package deploy

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fakeACLSourceStore is a DeploymentStore that also serves the adopted ACL
// sources. The deploy service resolves the optional ACLSourceStore with a
// type assertion on the deployment store, exactly like the broker-config
// store.
type fakeACLSourceStore struct {
	*fakeDeploymentStore
	sources map[string]storage.ACLSourceRow
}

func newFakeACLSourceStore() *fakeACLSourceStore {
	return &fakeACLSourceStore{
		fakeDeploymentStore: newFakeDeploymentStore(),
		sources:             map[string]storage.ACLSourceRow{},
	}
}

func (f *fakeACLSourceStore) GetACLSource(_ context.Context, path string) (storage.ACLSourceRow, error) {
	row, ok := f.sources[path]
	if !ok {
		return storage.ACLSourceRow{}, storage.ErrACLSourceNotFound
	}
	return row, nil
}

// aclSourceDeployCfg returns an enabled file-mode deploy configuration and
// the on-disk paths of the ACL and passwd files.
func aclSourceDeployCfg(t *testing.T) (config.DeployConfig, string, string) {
	t.Helper()
	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")
	return config.DeployConfig{
		Mode:               "file",
		ACLPath:            aclPath,
		PasswdPath:         passwdPath,
		HealthcheckTimeout: 5 * time.Second,
	}, aclPath, passwdPath
}

// aclSourceService builds a deploy service backed by the fake ACL source
// store. The managed service account is configured so the activation probe
// has a usable identity.
func aclSourceService(
	t *testing.T,
	deployCfg config.DeployConfig,
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	store *fakeACLSourceStore,
) *Service {
	t.Helper()
	return NewService(
		&fakeApplier{},
		aclStore,
		mqttStore,
		store,
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.MosquittoConfig{Host: "localhost", Port: 1883, Username: serviceFixtureUser, Password: serviceFixturePass},
		deployCfg,
		noAudit,
	)
}

// writeACLSourceServicePasswd writes the passwd file with the hashed
// service password. The deploy render reuses the on-disk hash, so the
// rendered passwd stays identical between the preview and the apply.
func writeACLSourceServicePasswd(t *testing.T, passwdPath, username, password string) {
	t.Helper()
	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	writeFile(t, passwdPath, username+":"+hash+"\n")
}

// TestRender_WithAdoptedSourceKeepsForeignContent pins that the rendered ACL
// keeps the unknown directives and the comments of the adopted source and
// still carries the managed rules.
func TestRender_WithAdoptedSourceKeepsForeignContent(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	source := "# managed by ops\nacl_file /etc/mosquitto/extra.acl\n\nuser alice\ntopic read sensors/#\n"
	store := newFakeACLSourceStore()
	store.sources[deployCfg.ACLPath] = storage.ACLSourceRow{Path: deployCfg.ACLPath, Body: source}

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	}}
	svc := aclSourceService(t, deployCfg, aclStore, &fakeMQTTUserLister{}, store)

	aclBody, _, _, err := svc.render(context.Background())
	if err != nil {
		t.Fatalf("render returned error: %v", err)
	}
	for _, want := range []string{
		"# managed by ops",
		"acl_file /etc/mosquitto/extra.acl",
		"user alice",
		"topic read sensors/#",
		"user bob",
		"user svc-mcm",
	} {
		if !strings.Contains(aclBody, want) {
			t.Errorf("the rendered ACL is missing %q:\n%s", want, aclBody)
		}
	}
}

// TestRender_WithoutAdoptedSourceMatchesManagedRenderer pins that the
// managed-only path stays byte-identical to the T1 renderer.
func TestRender_WithoutAdoptedSourceMatchesManagedRenderer(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	rules := []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
		{ID: "2", Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
	}
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)
	svc := aclSourceService(t, deployCfg, &fakeACLStore{rules: rules}, &fakeMQTTUserLister{}, newFakeACLSourceStore())

	aclBody, _, _, err := svc.render(context.Background())
	if err != nil {
		t.Fatalf("render returned error: %v", err)
	}
	managed := make([]acl.Rule, 0, len(rules)+2)
	managed = append(managed, rules...)
	managed = append(managed, serviceUserACL(serviceFixtureUser)...)
	want := mosquitto.RenderACLFile(managed)
	if aclBody != want {
		t.Fatalf("rendered ACL = %q, want %q", aclBody, want)
	}
}

// TestPreview_ReportsAtRiskEntriesForForeignContent pins that the preview
// reports the access the new render would drop.
func TestPreview_ReportsAtRiskEntriesForForeignContent(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "user alice\ntopic read sensors/#\nacl_file /etc/mosquitto/extra.acl\n")
	writeFile(t, passwdPath, "")

	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeACLSourceStore())

	result, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	want := []string{
		"unknown | acl_file /etc/mosquitto/extra.acl",
		"user alice | topic read sensors/#",
	}
	if len(result.AtRiskEntries) != len(want) {
		t.Fatalf("at-risk entries = %v, want %v", result.AtRiskEntries, want)
	}
	for i := range want {
		if result.AtRiskEntries[i] != want[i] {
			t.Errorf("at-risk entry %d = %q, want %q", i, result.AtRiskEntries[i], want[i])
		}
	}
}

// TestPreview_ReportsPreservedEntriesAndCompositionWarnings pins that the
// preview reports the preserved foreign content of the adopted source and
// the warnings the composition produced.
func TestPreview_ReportsPreservedEntriesAndCompositionWarnings(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	source := "# managed by ops\nacl_file /etc/mosquitto/extra.acl\n\nuser alice\ntopic read sensors/#\n"
	writeFile(t, aclPath, source)
	writeFile(t, passwdPath, "")

	store := newFakeACLSourceStore()
	store.sources[deployCfg.ACLPath] = storage.ACLSourceRow{Path: deployCfg.ACLPath, Body: source}

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	}}
	svc := aclSourceService(t, deployCfg, aclStore, &fakeMQTTUserLister{}, store)

	result, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(result.PreservedEntries) != 1 || result.PreservedEntries[0] != "acl_file /etc/mosquitto/extra.acl" {
		t.Errorf("preserved entries = %v, want the unknown directive", result.PreservedEntries)
	}
	if len(result.AtRiskEntries) != 0 {
		t.Errorf("at-risk entries = %v, want none: the source is composed into the render", result.AtRiskEntries)
	}
	if len(result.CompositionWarnings) == 0 {
		t.Error("the duplicated managed rule must be reported as a composition warning")
	}
	found := false
	for _, warning := range result.CompositionWarnings {
		if strings.Contains(warning, "user alice | topic read sensors/#") {
			found = true
		}
	}
	if !found {
		t.Errorf("composition warnings = %v, want the duplicated rule named", result.CompositionWarnings)
	}
}

// TestApply_RefusesUnknownDirectiveInOnDiskACL pins that an apply refuses
// when the on-disk ACL carries a directive MCM cannot interpret.
func TestApply_RefusesUnknownDirectiveInOnDiskACL(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "user alice\ntopic read sensors/#\nacl_file /etc/mosquitto/extra.acl\n")
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	// alice is a known MQTT user: her grant is reported at risk but does
	// not block. The unknown directive blocks.
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: "hash"},
	}}
	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, mqttStore, newFakeACLSourceStore())

	preview, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(preview.AtRiskEntries) != 2 {
		t.Fatalf("at-risk entries = %v, want the alice grant and the unknown directive", preview.AtRiskEntries)
	}
	_, err = svc.Apply(context.Background(), "admin", preview.RevisionID)
	if !errors.Is(err, ErrUnmanagedACLContent) {
		t.Fatalf("apply err = %v, want ErrUnmanagedACLContent", err)
	}
	var unmanaged *UnmanagedACLContentError
	if !errors.As(err, &unmanaged) {
		t.Fatalf("apply err = %v, want an error carrying the blocking entries", err)
	}
	if len(unmanaged.Entries) != 1 || unmanaged.Entries[0] != "unknown | acl_file /etc/mosquitto/extra.acl" {
		t.Errorf("blocking entries = %v, want the unknown directive", unmanaged.Entries)
	}
}

// TestApply_RefusesGrantForUnknownPrincipal pins that an apply refuses when
// the on-disk ACL grants access to a principal MCM does not know.
func TestApply_RefusesGrantForUnknownPrincipal(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "user ghost\ntopic read sensors/#\n")
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeACLSourceStore())

	preview, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	_, err = svc.Apply(context.Background(), "admin", preview.RevisionID)
	if !errors.Is(err, ErrUnmanagedACLContent) {
		t.Fatalf("apply err = %v, want ErrUnmanagedACLContent", err)
	}
	var unmanaged *UnmanagedACLContentError
	if !errors.As(err, &unmanaged) {
		t.Fatalf("apply err = %v, want an error carrying the blocking entries", err)
	}
	if len(unmanaged.Entries) != 1 || unmanaged.Entries[0] != "user ghost | topic read sensors/#" {
		t.Errorf("blocking entries = %v, want the grant of the unknown principal", unmanaged.Entries)
	}
}

// TestApply_AllowsUnmanagedContentWithoutRecognizedDirective pins that a
// file without a directive MCM recognizes is replaced as before: it grants
// nothing, so the deploy drops no access.
func TestApply_AllowsUnmanagedContentWithoutRecognizedDirective(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "old acl content\n")
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, &fakeMQTTUserLister{}, newFakeACLSourceStore())

	preview, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(preview.AtRiskEntries) != 1 {
		t.Fatalf("at-risk entries = %v, want the unmanaged line", preview.AtRiskEntries)
	}
	deployment, err := svc.Apply(context.Background(), "admin", preview.RevisionID)
	if err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("deployment status = %q, want active_verified", deployment.Status)
	}
}

// TestApply_AllowsAtRiskEntryOfKnownMQTTUser pins that an at-risk entry that
// belongs to a known MQTT user is reported but does not block the apply.
func TestApply_AllowsAtRiskEntryOfKnownMQTTUser(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	writeFile(t, aclPath, "user alice\ntopic read sensors/#\n")
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: "hash"},
	}}
	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, mqttStore, newFakeACLSourceStore())

	preview, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(preview.AtRiskEntries) != 1 || preview.AtRiskEntries[0] != "user alice | topic read sensors/#" {
		t.Fatalf("at-risk entries = %v, want the alice grant", preview.AtRiskEntries)
	}

	deployment, err := svc.Apply(context.Background(), "admin", preview.RevisionID)
	if err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("deployment status = %q, want active_verified", deployment.Status)
	}
}

// TestApply_SucceedsWhenForeignContentAdopted pins that adopting the foreign
// content as a source makes the apply land: the content is preserved.
func TestApply_SucceedsWhenForeignContentAdopted(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := aclSourceDeployCfg(t)
	onDisk := "user ghost\ntopic read sensors/#\nacl_file /etc/mosquitto/extra.acl\n"
	writeFile(t, aclPath, onDisk)
	writeFile(t, passwdPath, "")
	writeACLSourceServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	store := newFakeACLSourceStore()
	store.sources[deployCfg.ACLPath] = storage.ACLSourceRow{Path: deployCfg.ACLPath, Body: onDisk}
	svc := aclSourceService(t, deployCfg, &fakeACLStore{}, &fakeMQTTUserLister{}, store)

	preview, err := svc.Preview(context.Background(), "admin")
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(preview.AtRiskEntries) != 0 {
		t.Fatalf("at-risk entries = %v, want none: the foreign content is adopted", preview.AtRiskEntries)
	}

	deployment, err := svc.Apply(context.Background(), "admin", preview.RevisionID)
	if err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("deployment status = %q, want active_verified", deployment.Status)
	}
}
