package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// denyProbeService builds a deploy service with an optional managed
// service account, so the deny-only fallback can be exercised.
func denyProbeService(
	aclStore acl.Store,
	mqttStore MQTTUserLister,
	deployCfg config.DeployConfig,
	serviceUser string,
	servicePassword string,
	verifier ActiveVerifier,
	passwords map[string]string,
) (*Service, *fakeApplier) {
	lookup := newFakePasswordLookup()
	for username, password := range passwords {
		lookup.remember(username, password)
	}
	applier := &fakeApplier{}
	svc := NewService(
		applier,
		aclStore,
		mqttStore,
		newFakeDeploymentStore(),
		verifier,
		lookup,
		config.MosquittoConfig{Host: "localhost", Port: 1883, Username: serviceUser, Password: servicePassword},
		deployCfg,
		noAudit,
	)
	return svc, applier
}

// TestPickVerificationProbe_SkipsDenyRules pins that a deny rule is never
// selected as the positive activation probe: a deny is not a grant.
func TestPickVerificationProbe_SkipsDenyRules(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"topic readwrite cmd/#",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a user-scoped grant")
	}
	if probe.username != "alice" {
		t.Errorf("probe username = %q, want %q", probe.username, "alice")
	}
	if probe.allowedTopic != "cmd/mcm-verify" {
		// The verifier publishes to one concrete topic, so the wildcard
		// level of the grant is replaced by the fixed probe segment.
		t.Errorf("probe topic = %q, want %q (the concretized grant, never the deny)", probe.allowedTopic, "cmd/mcm-verify")
	}
	if probe.kind != probeKindUserGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindUserGrant)
	}
	if probe.deniedTopic != "sensors/#" {
		t.Errorf("probe denied topic = %q, want %q", probe.deniedTopic, "sensors/#")
	}
}

// TestPickVerificationProbe_DenyOnlyPolicyUsesServiceFallback pins that a
// policy with only deny rules falls back to the managed service
// healthcheck grant: the probe uses the configured service account and the
// generated healthcheck grant.
func TestPickVerificationProbe_DenyOnlyPolicyUsesServiceFallback(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
		"user svc-mcm",
		"topic read #",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\nsvc-mcm:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a deny-only policy")
	}
	if probe.username != serviceFixtureUser {
		t.Errorf("probe username = %q, want %q", probe.username, serviceFixtureUser)
	}
	if probe.password != serviceFixturePass {
		t.Errorf("probe password = %q, want the configured service password", probe.password)
	}
	if probe.allowedTopic != "mcm/healthcheck" {
		t.Errorf("probe topic = %q, want %q", probe.allowedTopic, "mcm/healthcheck")
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestPickVerificationProbe_ServiceFallbackNeedsTheGeneratedHealthcheckGrant
// pins that the fallback probe is the generated healthcheck grant and not
// an arbitrary rule that grants the service account another topic: a
// candidate rule listed before the healthcheck grant must not be selected.
func TestPickVerificationProbe_ServiceFallbackNeedsTheGeneratedHealthcheckGrant(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
		"user svc-mcm",
		"topic readwrite custom/probe",
		"topic read #",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\nsvc-mcm:$2a$10$hash\n"

	probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody)
	if !ok {
		t.Fatal("pickVerificationProbe found no probe for a deny-only policy")
	}
	if probe.allowedTopic != "mcm/healthcheck" {
		t.Errorf("probe topic = %q, want %q (the generated healthcheck grant)", probe.allowedTopic, "mcm/healthcheck")
	}
	if probe.kind != probeKindServiceGrant {
		t.Errorf("probe kind = %q, want %q", probe.kind, probeKindServiceGrant)
	}
}

// TestPickVerificationProbe_ServiceFallbackNeedsTheHealthcheckGrant pins
// that a service account without the generated healthcheck grant yields no
// fallback probe: the deploy must fail instead of probing an arbitrary
// grant.
func TestPickVerificationProbe_ServiceFallbackNeedsTheHealthcheckGrant(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
		"user svc-mcm",
		"topic read #",
		"topic readwrite custom/probe",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\nsvc-mcm:$2a$10$hash\n"

	if probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody); ok {
		t.Fatalf("expected no probe without the generated healthcheck grant, got %+v", probe)
	}
}

// TestPickVerificationProbe_DenyOnlyPolicyWithoutServiceAccountHasNoProbe
// pins that a deny-only policy without a configured service account yields
// no probe: the deploy must fail safe instead of probing a deny.
func TestPickVerificationProbe_DenyOnlyPolicyWithoutServiceAccountHasNoProbe(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), map[string]string{"alice": "alice-pass"})

	aclBody := strings.Join([]string{
		"user alice",
		"topic deny sensors/#",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\n"

	if probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody); ok {
		t.Fatalf("expected no probe for a deny-only policy without a service account, got %+v", probe)
	}
}

// TestPickVerificationProbe_MissingCredentialsHaveNoProbe pins that a
// grant whose identity has no known cleartext password yields no probe and
// no fallback: the verifier must never invent credentials.
func TestPickVerificationProbe_MissingCredentialsHaveNoProbe(t *testing.T) {
	t.Parallel()

	deployCfg, _, _ := enabledDeployCfg(t)
	svc, _ := denyProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), nil)

	aclBody := strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
		"user svc-mcm",
		"topic readwrite mcm/healthcheck",
		"",
	}, "\n")
	passwdBody := "alice:$2a$10$hash\nsvc-mcm:$2a$10$hash\n"

	if probe, ok := svc.pickVerificationProbe(context.Background(), aclBody, passwdBody); ok {
		t.Fatalf("expected no probe without a known password, got %+v", probe)
	}
}

// writeDenyServicePasswd seeds the on-disk passwd file with the managed
// service account, the way the broker bootstrap does. The deploy render
// reuses a matching hash, so the preview and the apply render the same
// bytes.
func writeDenyServicePasswd(t *testing.T, passwdPath, username, password string) {
	t.Helper()
	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	writeFile(t, passwdPath, username+":"+hash+"\n")
}

// TestApply_DenyOnlyPolicyUsesTheOperationalFallback pins the full deploy
// path for a deny-only policy: the deployment activates through the
// labelled operational fallback and the status message says so.
func TestApply_DenyOnlyPolicyUsesTheOperationalFallback(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl content")
	writeDenyServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	hash, err := mosquitto.HashPassword("verify-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{Principal: "verify-user", TopicFilter: "sensors/#", Permission: acl.PermissionDeny},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "verify-user", PasswordHash: hash},
	}}
	svc, applier := denyProbeService(aclStore, mqttStore, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(okVerifier), map[string]string{"verify-user": "verify-pass"})

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("deployment status = %q, want active_verified; message = %q", deployment.Status, deployment.Message)
	}
	if !strings.Contains(deployment.Message, "operational fallback") {
		t.Errorf("deployment message = %q, want the operational fallback label", deployment.Message)
	}

	// The rendered ACL keeps the deny rule: the fallback probe never
	// removes a rule from the policy.
	if applier.callCount() == 0 {
		t.Fatal("applier was never called")
	}
	rendered := applier.callAt(0).aclBody
	if !strings.Contains(rendered, "topic deny sensors/#") {
		t.Errorf("rendered acl = %q, want the deny rule", rendered)
	}
	if !strings.Contains(rendered, "topic readwrite mcm/healthcheck") {
		t.Errorf("rendered acl = %q, want the generated healthcheck grant", rendered)
	}
}

// TestApply_DenyOnlyPolicyWithoutServiceAccountRollsBack pins that a
// deny-only policy without a configured service account fails safe: there
// is no probe, so the deploy rolls back.
func TestApply_DenyOnlyPolicyWithoutServiceAccountRollsBack(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl content")
	writeFile(t, passwdPath, "old passwd content")

	hash, err := mosquitto.HashPassword("verify-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{Principal: "verify-user", TopicFilter: "sensors/#", Permission: acl.PermissionDeny},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "verify-user", PasswordHash: hash},
	}}
	svc, _ := denyProbeService(aclStore, mqttStore, deployCfg, "", "",
		diagnostics.VerifierFunc(okVerifier), map[string]string{"verify-user": "verify-pass"})

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "rolled_back" {
		t.Fatalf("deployment status = %q, want rolled_back; message = %q", deployment.Status, deployment.Message)
	}
	if !strings.Contains(deployment.Message, "no activation probe") {
		t.Errorf("deployment message = %q, want the no-probe reason", deployment.Message)
	}
}

// TestApply_VerifierFailureStillRollsBack pins that a failing verifier
// still rolls the deploy back, even when the probe is the operational
// fallback.
func TestApply_VerifierFailureStillRollsBack(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl content")
	writeDenyServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)

	hash, err := mosquitto.HashPassword("verify-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{Principal: "verify-user", TopicFilter: "sensors/#", Permission: acl.PermissionDeny},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "verify-user", PasswordHash: hash},
	}}
	svc, _ := denyProbeService(aclStore, mqttStore, deployCfg, serviceFixtureUser, serviceFixturePass,
		diagnostics.VerifierFunc(failVerifier), map[string]string{"verify-user": "verify-pass"})

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "rolled_back" {
		t.Fatalf("deployment status = %q, want rolled_back; message = %q", deployment.Status, deployment.Message)
	}
	if !strings.Contains(deployment.Message, "verification failed") {
		t.Errorf("deployment message = %q, want the verification failure reason", deployment.Message)
	}
}
