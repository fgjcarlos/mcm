package deploy

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fileWritingApplier writes the bodies of an apply to the managed files,
// so a test can assert what the broker would serve after the apply. The
// rollback request carries the snapshot as the body, so the second call
// restores the previous content.
type fileWritingApplier struct {
	aclPath    string
	passwdPath string

	mu    sync.Mutex
	calls int
}

// Apply writes the ACL and passwd bodies to the managed files.
func (a *fileWritingApplier) Apply(_ context.Context, req mosquitto.ApplyRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if err := os.WriteFile(a.aclPath, []byte(req.ACLBody), 0o600); err != nil {
		return err
	}
	return os.WriteFile(a.passwdPath, []byte(req.PasswdBody), 0o600)
}

// ReloadBrokerOnly signals the broker without writing any file.
func (a *fileWritingApplier) ReloadBrokerOnly() error { return nil }

// callCount returns how many times the applier was called.
func (a *fileWritingApplier) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// restartScenario builds the deploy service of an MCM that restarted: the
// managed user owns a readwrite grant, but the cleartext password of that
// user is gone because it only lived in the process memory.
//
// The managed service account is configured and present in the rendered
// password file, and it owns the generated readwrite healthcheck grant, so
// the operational fallback is available.
func restartScenario(t *testing.T, serviceUser, servicePassword string, verifier ActiveVerifier) (*Service, string, string) {
	t.Helper()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "")
	if serviceUser != "" && servicePassword != "" {
		writeServicePasswd(t, passwdPath, serviceUser, servicePassword)
	} else {
		writeFile(t, passwdPath, "")
	}

	hash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: hash},
	}}
	// The password lookup is empty: the process restarted and lost the
	// cleartext password of the user.
	svc := nativeProbeService(aclStore, mqttStore, deployCfg, serviceUser, servicePassword, verifier, nil)
	return svc, aclPath, passwdPath
}

// TestPickVerificationProbe_UnknownCleartextPasswordDoesNotDiscardLaterCandidates
// pins the skip rule (issue #340): an identity whose cleartext password is
// unknown is skipped, so the valid candidates that come after it are still
// considered. The selection must not end with the first identity that owns
// a grant.
func TestPickVerificationProbe_UnknownCleartextPasswordDoesNotDiscardLaterCandidates(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"bob": "bob-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
		"user bob",
		"topic readwrite actuators/#",
		"",
	}, "\n")
	passwdBody := strings.Join([]string{
		"alice:$2a$10$hash",
		"bob:$2a$10$hash",
		"",
	}, "\n")

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe although bob has a known cleartext password")
	}
	if probe.username != "bob" {
		t.Errorf("probe username = %q, want %q (alice must be skipped, not discard bob)", probe.username, "bob")
	}
	if probe.kind != probeKindUserGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindUserGrant)
	}
}

// TestApply_AfterRestartVerifiesWithTheManagedServiceOperationalFallback
// pins the outcome of an apply after a restart of MCM: the deployment
// activates through the managed service account (the labelled operational
// fallback) instead of rolling back, and the message names the cause (the
// lost cleartext password of the user) and the remedy.
func TestApply_AfterRestartVerifiesWithTheManagedServiceOperationalFallback(t *testing.T) {
	t.Parallel()

	verifier := &recordingVerifier{}
	svc, _, _ := restartScenario(t, serviceFixtureUser, serviceFixturePass, verifier)

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("Status = %q, want %q (message: %s)", deployment.Status, "active_verified", deployment.Message)
	}

	calls := verifier.calls()
	if len(calls) != 1 {
		t.Fatalf("verifier called %d times, want 1", len(calls))
	}
	if calls[0].Username != serviceFixtureUser {
		t.Errorf("verifier username = %q, want %q (the managed service account)", calls[0].Username, serviceFixtureUser)
	}
	if calls[0].AllowedTopic != serviceHealthcheckTopic {
		t.Errorf("verifier topic = %q, want %q (the generated healthcheck grant)", calls[0].AllowedTopic, serviceHealthcheckTopic)
	}

	if !strings.Contains(deployment.Message, string(probeKindServiceGrant)) {
		t.Errorf("message = %q, want it to name the operational fallback", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "alice") {
		t.Errorf("message = %q, want it to name the user whose cleartext password is unknown", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "lost on restart") {
		t.Errorf("message = %q, want it to name the cause (the cleartext password is lost on restart)", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "reset that user's password and apply again immediately") {
		t.Errorf("message = %q, want it to name the remedy", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "client policy semantics") {
		t.Errorf("message = %q, want it to state that the client policy semantics were not proven", deployment.Message)
	}
}

// TestApply_WithoutAnyProbeKeepsThePrefixAndNamesTheCauseAndTheRemedy
// pins the diagnostic of an apply that cannot build any probe: the message
// keeps the prefix the broker-config activation classifies, and it names
// the cause and the remedy.
func TestApply_WithoutAnyProbeKeepsThePrefixAndNamesTheCauseAndTheRemedy(t *testing.T) {
	t.Parallel()

	verifier := &recordingVerifier{}
	svc, _, _ := restartScenario(t, "", "", verifier)

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "rolled_back" {
		t.Fatalf("Status = %q, want %q (message: %s)", deployment.Status, "rolled_back", deployment.Message)
	}
	if calls := verifier.calls(); len(calls) != 0 {
		t.Errorf("verifier called %d times, want 0 (a setup failure must not probe)", len(calls))
	}
	if !strings.HasPrefix(deployment.Message, "no activation probe available") {
		t.Errorf("message = %q, want it to keep the %q prefix", deployment.Message, "no activation probe available")
	}
	if !strings.Contains(deployment.Message, "alice") {
		t.Errorf("message = %q, want it to name the user whose cleartext password is unknown", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "lost on restart") {
		t.Errorf("message = %q, want it to name the cause (the cleartext password is lost on restart)", deployment.Message)
	}
	if !strings.Contains(deployment.Message, "reset that user's password and apply again immediately") {
		t.Errorf("message = %q, want it to name the remedy", deployment.Message)
	}
}

// TestApply_RejectedApplyLeavesTheBrokerFilesUntouched pins that an apply
// that cannot build any probe leaves the broker files as they were: the
// apply writes the rendered content and the rollback restores the previous
// bytes.
func TestApply_RejectedApplyLeavesTheBrokerFilesUntouched(t *testing.T) {
	t.Parallel()

	const (
		previousACL    = "old acl content"
		previousPasswd = "olduser:legacy-hash\n"
	)

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, previousACL)
	writeFile(t, passwdPath, previousPasswd)

	hash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "alice", PasswordHash: hash},
	}}
	applier := &fileWritingApplier{aclPath: aclPath, passwdPath: passwdPath}
	svc := NewService(
		applier,
		aclStore,
		mqttStore,
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		newFakePasswordLookup(),
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		deployCfg,
		noAudit,
	)

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "rolled_back" {
		t.Fatalf("Status = %q, want %q (message: %s)", deployment.Status, "rolled_back", deployment.Message)
	}
	if applier.callCount() < 2 {
		t.Errorf("applier called %d times, want at least 2 (the apply and its rollback)", applier.callCount())
	}

	onDiskACL, err := os.ReadFile(aclPath)
	if err != nil {
		t.Fatalf("read acl file: %v", err)
	}
	if string(onDiskACL) != previousACL {
		t.Errorf("acl file = %q, want the previous content %q", string(onDiskACL), previousACL)
	}
	onDiskPasswd, err := os.ReadFile(passwdPath)
	if err != nil {
		t.Fatalf("read passwd file: %v", err)
	}
	if string(onDiskPasswd) != previousPasswd {
		t.Errorf("passwd file = %q, want the previous content %q", string(onDiskPasswd), previousPasswd)
	}
}

// TestPickVerificationProbe_UserGrantWithKnownPasswordIsStillPreferred pins
// that the skip rule did not change the probe order: a user-scoped
// readwrite grant whose identity has a known cleartext password is still
// the preferred probe.
func TestPickVerificationProbe_UserGrantWithKnownPasswordIsStillPreferred(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
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
	if probe.kind != probeKindUserGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindUserGrant)
	}
}

// TestPickVerificationProbe_PatternGrantIsStillProbed pins that the skip
// rule did not change the second phase: a readwrite pattern grant is still
// probed with the identity substituted into the placeholder.
func TestPickVerificationProbe_PatternGrantIsStillProbed(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := "pattern readwrite clients/%u/state\n"
	passwdBody := "alice:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a pattern policy")
	}
	if probe.username != "alice" {
		t.Errorf("probe username = %q, want %q", probe.username, "alice")
	}
	if probe.kind != probeKindPatternGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindPatternGrant)
	}
}

// TestPickVerificationProbe_DenyOnlyPolicyStillUsesTheServiceFallback pins
// that the operational fallback is still the probe of a policy without any
// usable grant: the skip rule only skips an identity, it never removes the
// fallback.
func TestPickVerificationProbe_DenyOnlyPolicyStillUsesTheServiceFallback(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), nil)

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
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
		t.Fatal("pickVerificationProbe found no probe for a deny-only policy")
	}
	if probe.username != serviceFixtureUser {
		t.Errorf("probe username = %q, want %q", probe.username, serviceFixtureUser)
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestBrokerConfigActivation_ClassifiesAMissingProbeAsUnavailable pins the
// contract the broker-config activation relies on: a missing activation
// probe is classified as unavailable verification, not as a failed
// verification.
func TestBrokerConfigActivation_ClassifiesAMissingProbeAsUnavailable(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
	}, "\n"))
	writeFile(t, passwdPath, "alice:$2a$10$hash\n")

	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), nil)

	ok, ran, message := svc.verifyActivation(context.Background())
	if ok {
		t.Error("the activation must not be reported as verified without a probe")
	}
	if ran {
		t.Error("the probe must not be reported as run without a probe identity")
	}
	if !strings.Contains(message, "MQTT verification unavailable") {
		t.Errorf("message = %q, want the unavailable classification", message)
	}
}
