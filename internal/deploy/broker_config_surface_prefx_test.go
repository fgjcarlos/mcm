package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
)

// The broker-configuration surface had a published refusal before the
// activation-probe feature: it refused the whole apply when MCM could
// not authenticate as an identity that owns a user-scoped grant, because
// the cleartext password of that identity only lived in the process
// memory of MCM and a restart of MCM lost it. The probe selection now
// skips such an identity instead of aborting the whole selection (issue
// #340), so the surface has to restore that refusal itself. The tests
// below pin the pre-feature contract of the surface, measured on the code
// before the feature: the refusal wording, the refusal condition and the
// policies that the surface accepted through the operational fallback.

// brokerConfigSurfaceService builds the deploy service of the
// broker-configuration surface with the managed service account
// configured, so the operational fallback is available in every scenario
// below. The password lookup is the given one: an empty lookup models an
// MCM that restarted and lost the cleartext passwords.
func brokerConfigSurfaceService(deployCfg config.DeployConfig, lookup *fakePasswordLookup) *Service {
	return NewService(
		&fakeApplier{},
		&fakeACLStore{},
		&fakeMQTTUserLister{},
		newFakeDeploymentStore(),
		diagnostics.VerifierFunc(okVerifier),
		lookup,
		config.MosquittoConfig{
			Host:     "localhost",
			Port:     1883,
			Username: serviceFixtureUser,
			Password: serviceFixturePass,
		},
		deployCfg,
		noAudit,
	)
}

// TestBrokerConfigSurface_ReadOnlyIdentityWithLostCleartextRefuses pins
// the pre-feature refusal of the broker-configuration surface: the
// password lookup of an identity that owns a user-scoped grant ran
// BEFORE the check for a readwrite grant, so an identity with a narrower
// grant and a lost cleartext password refused the whole apply. The
// surface refuses, keeps the "MQTT verification unavailable" wording and
// names the identity, the cause and the remedy.
func TestBrokerConfigSurface_ReadOnlyIdentityWithLostCleartextRefuses(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, strings.Join([]string{
		"user alice",
		"topic read sensors/#",
		"",
		"user " + serviceFixtureUser,
		"topic read #",
		"topic readwrite " + serviceHealthcheckTopic,
		"",
	}, "\n"))
	writeFile(t, passwdPath, "alice:$2a$10$hash\n"+serviceFixtureUser+":$2a$10$hash\n")

	// The lookup is empty: the restart of MCM lost the cleartext password
	// of alice.
	svc := brokerConfigSurfaceService(deployCfg, newFakePasswordLookup())

	ok, ran, message := svc.verifyActivation(context.Background())
	if ok || ran {
		t.Fatalf("the broker-config surface must refuse a policy whose only grant owner has a lost cleartext password (ok=%v, ran=%v, message=%q)", ok, ran, message)
	}
	if !strings.Contains(message, "MQTT verification unavailable") {
		t.Errorf("message = %q, want the %q wording", message, "MQTT verification unavailable")
	}
	if !strings.Contains(message, causeAlice) {
		t.Errorf("message = %q, want it to name the identity, the cause and the remedy", message)
	}
}

// TestBrokerConfigSurface_LostReadwriteIdentityRefuses pins the second
// pre-feature refusal: an identity that owns a readwrite grant with a
// lost cleartext password aborted the whole selection, so the surface
// refused even when another identity was a verifiable probe. The surface
// refuses and keeps the "MQTT verification unavailable" wording.
func TestBrokerConfigSurface_LostReadwriteIdentityRefuses(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, strings.Join([]string{
		"user alice",
		"topic readwrite sensors/#",
		"",
		"user bob",
		"topic readwrite actuators/#",
		"",
		"user " + serviceFixtureUser,
		"topic read #",
		"topic readwrite " + serviceHealthcheckTopic,
		"",
	}, "\n"))
	writeFile(t, passwdPath, "alice:$2a$10$hash\nbob:$2a$10$hash\n"+serviceFixtureUser+":$2a$10$hash\n")

	lookup := newFakePasswordLookup()
	// Only bob's cleartext password survived the restart of MCM, so bob
	// is a verifiable probe.
	lookup.remember("bob", "bob-pass")
	svc := brokerConfigSurfaceService(deployCfg, lookup)

	ok, ran, message := svc.verifyActivation(context.Background())
	if ok || ran {
		t.Fatalf("the broker-config surface must refuse when an identity with a grant has a lost cleartext password (ok=%v, ran=%v, message=%q)", ok, ran, message)
	}
	if !strings.Contains(message, "MQTT verification unavailable") {
		t.Errorf("message = %q, want the %q wording", message, "MQTT verification unavailable")
	}
	if !strings.Contains(message, causeAlice) {
		t.Errorf("message = %q, want it to name the identity, the cause and the remedy", message)
	}
}

// TestBrokerConfigSurface_PatternOnlyPolicyIsAccepted pins the third
// pre-feature row: a pattern grant is not a user-scoped grant, so it
// never aborted the selection and the surface accepted the policy
// through the operational fallback. The surface must keep accepting it.
func TestBrokerConfigSurface_PatternOnlyPolicyIsAccepted(t *testing.T) {
	t.Parallel()

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, strings.Join([]string{
		"pattern readwrite clients/%u/state",
		"",
		"user " + serviceFixtureUser,
		"topic read #",
		"topic readwrite " + serviceHealthcheckTopic,
		"",
	}, "\n"))
	writeFile(t, passwdPath, "alice:$2a$10$hash\n"+serviceFixtureUser+":$2a$10$hash\n")

	// The lookup is empty: alice has no known cleartext password, but no
	// user-scoped grant is affected, so the surface accepts the policy.
	svc := brokerConfigSurfaceService(deployCfg, newFakePasswordLookup())

	ok, ran, message := svc.verifyActivation(context.Background())
	if !ok || !ran {
		t.Fatalf("the broker-config surface must accept a pattern-only policy through the operational fallback (ok=%v, ran=%v, message=%q)", ok, ran, message)
	}
	if message != "" {
		t.Errorf("message = %q, want no reason for an accepted policy", message)
	}
}
