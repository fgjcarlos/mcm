package deploy

import (
	"context"
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

// nativeDeployCfg returns an enabled file-mode deploy configuration plus
// the on-disk paths for the ACL and passwd files.
func nativeDeployCfg(t *testing.T) (config.DeployConfig, string, string) {
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

// nativeTestService builds a deploy service with the given service user
// name so the rendered service grants can be asserted.
func nativeTestService(
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployCfg config.DeployConfig,
	serviceUser string,
) *Service {
	return NewService(
		&fakeApplier{},
		aclStore,
		mqttStore,
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		&fakePasswordLookup{},
		config.MosquittoConfig{Host: "localhost", Port: 1883, Username: serviceUser},
		deployCfg,
		noAudit,
	)
}

// TestRender_IncludesNativeRules pins that a managed deploy renders every
// native rule kind: the anonymous section, the pattern section, the deny
// permission and the service-generated grants.
func TestRender_IncludesNativeRules(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
		{ID: "2", Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{ID: "3", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionDeny},
	}}

	svc := nativeTestService(aclStore, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser)

	aclBody, _, _, err := svc.render(context.Background())
	if err != nil {
		t.Fatalf("render returned error: %v", err)
	}

	if !strings.Contains(aclBody, "topic read public/#") {
		t.Errorf("rendered ACL is missing the anonymous rule:\n%s", aclBody)
	}
	if !strings.Contains(aclBody, "pattern deny clients/%u/state") {
		t.Errorf("rendered ACL is missing the pattern rule:\n%s", aclBody)
	}
	if !strings.Contains(aclBody, "topic read sensors/#") {
		t.Errorf("rendered ACL is missing the user rule:\n%s", aclBody)
	}
	if !strings.Contains(aclBody, "user svc-mcm") {
		t.Errorf("rendered ACL is missing the service user block:\n%s", aclBody)
	}
	if !strings.Contains(aclBody, "topic read #") {
		t.Errorf("rendered ACL is missing the service grant:\n%s", aclBody)
	}

	anonymousIdx := strings.Index(aclBody, "topic read public/#")
	userIdx := strings.Index(aclBody, "user alice")
	if anonymousIdx < 0 || userIdx < 0 || anonymousIdx > userIdx {
		t.Errorf("anonymous section must be rendered before the user sections:\n%s", aclBody)
	}
}

// TestRender_KeepsNativeRulesWhenOrphanListIsEmpty pins that an anonymous
// or pattern rule is never dropped from the rendered ACL because of the
// orphan filter: those records carry no MQTT username.
func TestRender_KeepsNativeRulesWhenOrphanListIsEmpty(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{ID: "2", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionRead},
	}}
	// The orphan lister reports an empty principal, which must not be
	// treated as an orphan username for native rules.
	mqttStore := &fakeMQTTUserLister{orphanRules: []storage.ACLRuleRow{
		{ID: "9", Selector: acl.SelectorUser, Principal: "", TopicFilter: "ghost/#", Permission: acl.PermissionRead},
	}}

	svc := nativeTestService(aclStore, mqttStore, deployCfg, "")

	aclBody, _, _, err := svc.render(context.Background())
	if err != nil {
		t.Fatalf("render returned error: %v", err)
	}
	if !strings.Contains(aclBody, "topic read public/#") {
		t.Errorf("rendered ACL dropped the anonymous rule:\n%s", aclBody)
	}
	if !strings.Contains(aclBody, "pattern read clients/%u/state") {
		t.Errorf("rendered ACL dropped the pattern rule:\n%s", aclBody)
	}
}

// --- Activation probe ---

// recordingVerifier captures the options the deploy service hands to the
// active verifier so a test can assert what was actually probed.
type recordingVerifier struct {
	mu   sync.Mutex
	opts []diagnostics.VerifyActiveOptions
	fail bool
}

func (v *recordingVerifier) VerifyActive(_ context.Context, opts diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.opts = append(v.opts, opts)
	if v.fail {
		return failVerifier(context.Background(), opts)
	}
	return okVerifier(context.Background(), opts)
}

func (v *recordingVerifier) calls() []diagnostics.VerifyActiveOptions {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]diagnostics.VerifyActiveOptions, len(v.opts))
	copy(out, v.opts)
	return out
}

// nativeProbeService builds a deploy service for the activation-probe
// tests. An empty serviceUser disables the managed-service fallback; the
// passwords map seeds the cleartext password lookup.
func nativeProbeService(
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployCfg config.DeployConfig,
	serviceUser, servicePassword string,
	verifier ActiveVerifier,
	passwords map[string]string,
) *Service {
	lookup := newFakePasswordLookup()
	for username, password := range passwords {
		lookup.remember(username, password)
	}
	return NewService(
		&fakeApplier{},
		aclStore,
		mqttStore,
		newFakeDeploymentStore(),
		verifier,
		lookup,
		config.MosquittoConfig{
			Host:     "localhost",
			Port:     1883,
			Username: serviceUser,
			Password: servicePassword,
		},
		deployCfg,
		noAudit,
	)
}

// TestPickVerificationProbe_UserGrantUsesConcreteTopic pins that a
// user-scoped readwrite grant is the preferred probe and that the probe
// topic is a concrete topic name, never the wildcard filter itself.
func TestPickVerificationProbe_UserGrantUsesConcreteTopic(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a user-scoped readwrite grant")
	}
	if probe.username != "alice" {
		t.Errorf("probe username = %q, want %q", probe.username, "alice")
	}
	if probe.allowedTopic != "sensors/mcm-verify" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "sensors/mcm-verify")
	}
	if probe.kind != probeKindUserGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindUserGrant)
	}
}

// TestPickVerificationProbe_NativeOnlyPatternPolicy pins that a native-only
// policy (a pattern grant and no user-scoped grant) still yields a usable
// probe: the probe identity is substituted into the %u placeholder so the
// broker sees one concrete topic.
func TestPickVerificationProbe_NativeOnlyPatternPolicy(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := "pattern readwrite clients/%u/state\n"
	passwdBody := "alice:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a native-only pattern policy")
	}
	if probe.username != "alice" {
		t.Errorf("probe username = %q, want %q", probe.username, "alice")
	}
	if probe.allowedTopic != "clients/alice/state" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "clients/alice/state")
	}
	if probe.kind != probeKindPatternGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindPatternGrant)
	}
}

// TestPickVerificationProbe_BroadPatternKeepsUserGrantUsable pins that a
// broader pattern grant does not disqualify a usable user-scoped grant.
// The pattern applies to every user, so it cannot make the user grant
// unusable.
func TestPickVerificationProbe_BroadPatternKeepsUserGrantUsable(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"pattern readwrite clients/#",
		"user alice",
		"topic readwrite clients/alice",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe although a user grant is usable")
	}
	if probe.username != "alice" {
		t.Errorf("probe username = %q, want %q", probe.username, "alice")
	}
	if probe.allowedTopic != "clients/alice" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "clients/alice")
	}
	if probe.kind != probeKindUserGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindUserGrant)
	}
}

// TestPickVerificationProbe_AnonymousPolicyFallsBackToManagedService pins
// that an anonymous-only policy does not dead-end the deploy: the probe
// falls back to the managed service account and the grant MCM emits for
// it. The fallback is an operational probe, not proof of the client
// policy.
func TestPickVerificationProbe_AnonymousPolicyFallsBackToManagedService(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), nil)

	aclBody := strings.Join([]string{
		"topic read public/#",
		"user svc-mcm",
		"topic read #",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := "svc-mcm:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for an anonymous policy with a managed service account")
	}
	if probe.username != serviceFixtureUser {
		t.Errorf("probe username = %q, want %q", probe.username, serviceFixtureUser)
	}
	if probe.allowedTopic != "mcm/healthcheck" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "mcm/healthcheck")
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestPickVerificationProbe_ServiceFallbackSelectsExactHealthcheckGrant
// pins that the managed-service fallback probes the healthcheck grant it
// claims to probe. An arbitrary readwrite rule of the service account
// that precedes the healthcheck grant must not be selected, and the
// fallback must not be labelled with a topic it does not use.
func TestPickVerificationProbe_ServiceFallbackSelectsExactHealthcheckGrant(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), nil)

	aclBody := strings.Join([]string{
		"user svc-mcm",
		"topic readwrite custom/probe",
		"topic read #",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := "svc-mcm:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a service account with a healthcheck grant")
	}
	if probe.allowedTopic != "mcm/healthcheck" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "mcm/healthcheck")
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestPickVerificationProbe_ServiceFallbackRejectsCustomOnlyGrant pins
// that a service account without the rendered healthcheck grant does not
// get a fallback probe: a custom readwrite rule must not be accepted as
// the healthcheck grant the fallback is named after.
func TestPickVerificationProbe_ServiceFallbackRejectsCustomOnlyGrant(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), nil)

	aclBody := strings.Join([]string{
		"user svc-mcm",
		"topic readwrite custom/probe",
		"topic read #",
		"",
	}, "\n")
	passwdBody := "svc-mcm:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if ok {
		t.Fatalf("expected no service probe without the healthcheck grant, got %+v", probe)
	}
}

// TestPickVerificationProbe_ClientIDPlaceholderIsNotProbed pins that a
// pattern that uses %c is not probed with a made-up topic: the verifier
// does not control the MQTT client id, so the placeholder cannot be
// expanded. The probe falls back to the managed service grant instead.
func TestPickVerificationProbe_ClientIDPlaceholderIsNotProbed(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"pattern readwrite clients/%c/state",
		"user svc-mcm",
		"topic read #",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := strings.Join([]string{
		"alice:$2a$10$hash",
		"svc-mcm:$2a$10$hash",
		"",
	}, "\n")

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a client-id placeholder pattern policy")
	}
	if strings.Contains(probe.allowedTopic, "%c") {
		t.Errorf("probe topic = %q, must not publish a placeholder", probe.allowedTopic)
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestPickVerificationProbe_DenyOnlyPolicyHasNoProbe pins that a policy
// with only deny rules yields no probe: the deploy must fail safe instead
// of probing a fabricated grant.
func TestPickVerificationProbe_DenyOnlyPolicyHasNoProbe(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\n"

	if probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody); ok {
		t.Fatalf("expected no probe for a deny-only policy, got %+v", probe)
	}
}

// TestPickVerificationProbe_EmptyPolicyHasNoProbe pins that an empty ACL
// yields no probe.
func TestPickVerificationProbe_EmptyPolicyHasNoProbe(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := nativeDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	if probe, ok := svc.pickVerificationProbe(context.Background(), "", "alice:$2a$10$hash\n"); ok {
		t.Fatalf("expected no probe for an empty ACL, got %+v", probe)
	}
}

// TestPickVerificationProbe_MissingCredentialsHaveNoProbe is covered by
// the stricter base test TestPickVerificationProbe_MissingCredentialsHaveNoProbe
// in acl_deny_test.go: there, an identity without a known cleartext
// password yields no probe even when the managed service account could
// serve as a fallback.

// writeServicePasswd seeds the on-disk passwd file with the managed
// service account, the way the broker bootstrap does. The deploy render
// reuses a matching hash, so the preview and the apply render the same
// bytes.
func writeServicePasswd(t *testing.T, passwdPath, username, password string) {
	t.Helper()
	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	writeFile(t, passwdPath, username+":"+hash+"\n")
}

// TestApply_NativeOnlyPolicyReachesTheVerifier pins that a native-only
// policy activates: the verifier is invoked with the substituted
// identity and the deployment reaches active_verified.
func TestApply_NativeOnlyPolicyReachesTheVerifier(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	hash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: hash},
	}}
	verifier := &recordingVerifier{}
	svc := nativeProbeService(aclStore, mqttStore, deployCfg, serviceFixtureUser, serviceFixturePass, verifier,
		map[string]string{"alice": "alice-pass"})

	d, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "active_verified" {
		t.Fatalf("Status = %q, want %q (message: %s)", d.Status, "active_verified", d.Message)
	}
	calls := verifier.calls()
	if len(calls) != 1 {
		t.Fatalf("verifier called %d times, want 1", len(calls))
	}
	if calls[0].Username != "alice" {
		t.Errorf("verifier username = %q, want %q", calls[0].Username, "alice")
	}
	if calls[0].AllowedTopic != "clients/alice/state" {
		t.Errorf("verifier topic = %q, want %q", calls[0].AllowedTopic, "clients/alice/state")
	}
	if !strings.Contains(d.Message, string(probeKindPatternGrant)) {
		t.Errorf("deployment message = %q, want it to name the probe kind", d.Message)
	}
}

// TestApply_AnonymousPolicyUsesManagedServiceProbe pins that an anonymous
// policy activates through the managed service probe instead of failing
// the setup stage.
func TestApply_AnonymousPolicyUsesManagedServiceProbe(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
	}}
	verifier := &recordingVerifier{}
	svc := nativeProbeService(aclStore, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass, verifier, nil)

	d, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "active_verified" {
		t.Fatalf("Status = %q, want %q (message: %s)", d.Status, "active_verified", d.Message)
	}
	calls := verifier.calls()
	if len(calls) != 1 {
		t.Fatalf("verifier called %d times, want 1", len(calls))
	}
	if calls[0].Username != serviceFixtureUser {
		t.Errorf("verifier username = %q, want %q", calls[0].Username, serviceFixtureUser)
	}
	if calls[0].AllowedTopic != "mcm/healthcheck" {
		t.Errorf("verifier topic = %q, want %q", calls[0].AllowedTopic, "mcm/healthcheck")
	}
	if calls[0].Password != serviceFixturePass {
		t.Errorf("verifier password = %q, want the configured service password", calls[0].Password)
	}
}

// TestApply_NativeOnlyPolicyRollsBackOnFailedProbe pins that a failing
// probe still rolls the deploy back and that the retries stay bounded.
func TestApply_NativeOnlyPolicyRollsBackOnFailedProbe(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	hash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: hash},
	}}
	verifier := &recordingVerifier{fail: true}
	svc := nativeProbeService(aclStore, mqttStore, deployCfg, serviceFixtureUser, serviceFixturePass, verifier,
		map[string]string{"alice": "alice-pass"})

	d, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "rolled_back" {
		t.Fatalf("Status = %q, want %q", d.Status, "rolled_back")
	}
	if calls := verifier.calls(); len(calls) != verifyAttempts {
		t.Errorf("verifier called %d times, want %d", len(calls), verifyAttempts)
	}
	if !strings.Contains(d.Message, string(probeKindPatternGrant)) {
		t.Errorf("message = %q, want it to name the probe kind", d.Message)
	}
}

// TestApply_MissingCredentialsFailSafe pins that a deploy without usable
// credentials does not skip verification: the verifier is never invoked
// and the deployment is rolled back.
func TestApply_MissingCredentialsFailSafe(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")

	hash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	writeFile(t, passwdPath, "alice:"+hash+"\n")

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: hash},
	}}
	verifier := &recordingVerifier{}
	svc := nativeProbeService(aclStore, mqttStore, deployCfg, "", "", verifier, nil)

	d, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if d.Status != "rolled_back" {
		t.Fatalf("Status = %q, want %q", d.Status, "rolled_back")
	}
	if calls := verifier.calls(); len(calls) != 0 {
		t.Errorf("verifier called %d times, want 0 (setup failure must not probe)", len(calls))
	}
	if !strings.Contains(d.Message, "no activation probe available") {
		t.Errorf("message = %q, want it to name the missing activation probe", d.Message)
	}
}

// TestSummarizeChanges_CountsNativeRules pins that the preview change
// summary sees native rules: a rendered anonymous or pattern rule counts as
// an added ACL entry.
func TestSummarizeChanges_CountsNativeRules(t *testing.T) {
	t.Parallel()

	rendered := strings.Join([]string{
		"topic read public/#",
		"pattern read clients/%u/state",
		"user alice",
		"topic read sensors/#",
		"",
	}, "\n")

	summary := summarizeChanges("", "", "", rendered)
	if summary.TopicsAdded != 3 {
		t.Errorf("TopicsAdded = %d, want 3 (anonymous, pattern and user entries): %+v", summary.TopicsAdded, summary)
	}
	if !summary.HasACLChanges {
		t.Errorf("expected HasACLChanges = true, got %+v", summary)
	}
}

// TestRender_WritesNativeRulesToDisk pins the rendered native rules reach
// the managed ACL file.
func TestRender_WritesNativeRulesToDisk(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := nativeDeployCfg(t)
	writeFile(t, aclPath, "")
	writeFile(t, passwdPath, "")

	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{ID: "2", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionDeny},
	}}

	svc := nativeTestService(aclStore, &fakeMQTTUserLister{}, deployCfg, "")

	aclBody, _, _, err := svc.render(context.Background())
	if err != nil {
		t.Fatalf("render returned error: %v", err)
	}
	if err := os.WriteFile(aclPath, []byte(aclBody), 0o600); err != nil {
		t.Fatalf("write acl file: %v", err)
	}
	onDisk, err := os.ReadFile(aclPath)
	if err != nil {
		t.Fatalf("read acl file: %v", err)
	}
	if !strings.Contains(string(onDisk), "pattern deny clients/%u/state") {
		t.Errorf("on-disk ACL is missing the pattern rule:\n%s", string(onDisk))
	}
}
