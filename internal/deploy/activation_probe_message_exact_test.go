package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// causeAlice is the cause-and-remedy sentence MCM produces for an
// identity whose cleartext password it does not know. The tests below
// compare the deployment messages against it byte for byte: the message
// is the operator-visible contract of the issue #339 fix.
const causeAlice = `MCM does not know the cleartext password of user "alice" because it only lives in the process memory and is lost on restart; reset that user's password and apply again immediately`

// TestApply_LaterUserGrantProbeNamesTheSkippedIdentity pins the deploy
// path of the skip rule (issue #340): the identity MCM skipped because
// its cleartext password is unknown reaches the deployment message even
// when a later identity is the probe. The skipped identity is what the
// operator has to fix, so the message must name it, the cause and the
// remedy.
func TestApply_LaterUserGrantProbeNamesTheSkippedIdentity(t *testing.T) {
	t.Parallel()

	aliceHash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	bobHash, err := mosquitto.HashPassword("bob-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	deployCfg, _, _ := enabledDeployCfg(t)
	verifier := &recordingVerifier{}
	svc := nativeProbeService(
		&fakeACLStore{rules: []acl.Rule{
			{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
			{ID: "2", Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "actuators/#", Permission: acl.PermissionReadWrite},
		}},
		&fakeMQTTUserLister{users: []storage.MQTTUser{
			{Username: "alice", PasswordHash: aliceHash},
			{Username: "bob", PasswordHash: bobHash},
		}},
		deployCfg,
		"",
		"",
		verifier,
		// Only bob's cleartext password survived the restart of MCM.
		map[string]string{"bob": "bob-pass"},
	)

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
	if calls[0].Username != "bob" {
		t.Errorf("verifier username = %q, want %q (the later identity is the probe)", calls[0].Username, "bob")
	}

	want := "activation probe: user grant; " + causeAlice
	if deployment.Message != want {
		t.Errorf("message =\n%q\nwant\n%q", deployment.Message, want)
	}
}

// TestApply_PatternOnlyPolicyNamesTheSkippedIdentity pins the second
// probe phase: a policy that only carries a readwrite pattern grant and
// an identity whose cleartext password MCM does not know reaches the
// operational fallback, and the message still names the skipped identity,
// the cause and the remedy.
func TestApply_PatternOnlyPolicyNamesTheSkippedIdentity(t *testing.T) {
	t.Parallel()

	aliceHash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	deployCfg, _, passwdPath := enabledDeployCfg(t)
	// The managed service account is on disk with a matching hash, so the
	// render of the service credentials stays stable between the preview
	// and the apply.
	writeServicePasswd(t, passwdPath, serviceFixtureUser, serviceFixturePass)
	verifier := &recordingVerifier{}
	svc := nativeProbeService(
		&fakeACLStore{rules: []acl.Rule{
			{ID: "1", Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionReadWrite},
		}},
		&fakeMQTTUserLister{users: []storage.MQTTUser{
			{Username: "alice", PasswordHash: aliceHash},
		}},
		deployCfg,
		serviceFixtureUser,
		serviceFixturePass,
		verifier,
		// The restart of MCM lost the cleartext password of alice.
		nil,
	)

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

	want := "activation probe: " + string(probeKindServiceGrant) + "; " + causeAlice +
		"; this probe did not prove the client policy semantics of user \"alice\""
	if deployment.Message != want {
		t.Errorf("message =\n%q\nwant\n%q", deployment.Message, want)
	}
}

// TestApply_DeploymentMessagesAreExact pins the two operator-visible
// messages of the issue #339 scenarios byte for byte. The frontend
// renders the message verbatim, so a wording change is a contract change.
func TestApply_DeploymentMessagesAreExact(t *testing.T) {
	t.Parallel()

	t.Run("operational fallback", func(t *testing.T) {
		t.Parallel()

		verifier := &recordingVerifier{}
		svc, _, _ := restartScenario(t, serviceFixtureUser, serviceFixturePass, verifier)

		deployment, err := applyPreview(t, svc, context.Background(), "operator")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		want := "activation probe: " + string(probeKindServiceGrant) + "; " + causeAlice +
			"; this probe did not prove the client policy semantics of user \"alice\""
		if deployment.Message != want {
			t.Errorf("message =\n%q\nwant\n%q", deployment.Message, want)
		}
		if strings.ContainsAny(deployment.Message, "\n\r") {
			t.Errorf("message = %q, want a single line", deployment.Message)
		}
	})

	t.Run("no probe at all", func(t *testing.T) {
		t.Parallel()

		verifier := &recordingVerifier{}
		svc, _, _ := restartScenario(t, "", "", verifier)

		deployment, err := applyPreview(t, svc, context.Background(), "operator")
		if err != nil {
			t.Fatalf("Apply returned error: %v", err)
		}
		want := "no activation probe available (need a user readwrite grant, a pattern readwrite grant or the configured managed service account); " + causeAlice
		if deployment.Message != want {
			t.Errorf("message =\n%q\nwant\n%q", deployment.Message, want)
		}
		if strings.ContainsAny(deployment.Message, "\n\r") {
			t.Errorf("message = %q, want a single line", deployment.Message)
		}
	})
}

// TestApply_NarrowerGrantIdentityIsNotNamedAsALostPassword pins the
// other side of the two facts the probe carries: an identity that owns
// only narrower user-scoped grants is skipped because it has no
// readwrite grant, not because its cleartext password is lost. The
// deployment message must not name such an identity as a lost password.
func TestApply_NarrowerGrantIdentityIsNotNamedAsALostPassword(t *testing.T) {
	t.Parallel()

	aliceHash, err := mosquitto.HashPassword("alice-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	bobHash, err := mosquitto.HashPassword("bob-pass", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	deployCfg, _, _ := enabledDeployCfg(t)
	verifier := &recordingVerifier{}
	svc := nativeProbeService(
		&fakeACLStore{rules: []acl.Rule{
			{ID: "1", Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
			{ID: "2", Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "actuators/#", Permission: acl.PermissionReadWrite},
		}},
		&fakeMQTTUserLister{users: []storage.MQTTUser{
			{Username: "alice", PasswordHash: aliceHash},
			{Username: "bob", PasswordHash: bobHash},
		}},
		deployCfg,
		"",
		"",
		verifier,
		// Only bob's cleartext password survived the restart of MCM.
		map[string]string{"bob": "bob-pass"},
	)

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
	if calls[0].Username != "bob" {
		t.Errorf("verifier username = %q, want %q (the identity with a readwrite grant is the probe)", calls[0].Username, "bob")
	}

	want := "activation probe: user grant"
	if deployment.Message != want {
		t.Errorf("message =\n%q\nwant\n%q", deployment.Message, want)
	}
	if strings.Contains(deployment.Message, "alice") {
		t.Errorf("message = %q, want it not to name an identity that owns no readwrite grant as a lost password", deployment.Message)
	}
}

// TestBrokerConfigActivation_RefusesWithTheCauseAndTheRemedy pins the
// broker-configuration surface: the managed service account is not a test
// identity for that surface, so the surface keeps its previous refusal
// when the operational fallback is the only available probe and MCM
// skipped an identity whose cleartext password it does not know. The
// refusal names the identity, the cause and the remedy, and it keeps the
// "MQTT verification unavailable" wording the end-to-end script greps
// for.
func TestBrokerConfigActivation_RefusesWithTheCauseAndTheRemedy(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
		"user " + serviceFixtureUser,
		"topic read #",
		"topic readwrite " + serviceHealthcheckTopic,
		"",
	}, "\n"))
	writeFile(t, passwdPath, "alice:$2a$10$hash\n"+serviceFixtureUser+":$2a$10$hash\n")

	svc := nativeProbeService(&fakeACLStore{}, &fakeMQTTUserLister{}, deployCfg,
		serviceFixtureUser, serviceFixturePass, diagnostics.VerifierFunc(okVerifier), nil)

	available, reason := svc.verificationAvailable(context.Background())
	if available {
		t.Fatalf("the broker-config preflight must refuse when the operational fallback is the only probe (reason: %q)", reason)
	}
	want := "MQTT verification unavailable: no managed test identity with a known password and an ACL grant; " + causeAlice
	if reason != want {
		t.Errorf("reason =\n%q\nwant\n%q", reason, want)
	}
}
