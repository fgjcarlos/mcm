package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
)

// TestBrokerConfigSurface_PreFeatureMatrix pins the accept/refuse decision
// of the broker-configuration surface against the contract it had before the
// activation-probe feature (origin/main): the surface refuses when MCM
// cannot authenticate as an identity that owns a user-scoped grant, and it
// accepts through the operational fallback otherwise.
//
// The rows are the pre-feature contract, not the current implementation:
// this test passes on the pre-feature code as well, so a change that moves
// the surface away from that contract fails here instead of passing
// unnoticed.
func TestBrokerConfigSurface_PreFeatureMatrix(t *testing.T) {
	t.Parallel()

	const serviceBlock = "user svc-mcm\ntopic read #\ntopic readwrite mcm/healthcheck\n"
	cases := []struct {
		name       string
		acl        string
		passwd     string
		known      map[string]string
		service    bool
		wantAccept bool
	}{
		{
			name:    "read-only identity with a lost cleartext refuses",
			acl:     "user alice\ntopic read sensors/#\n\n" + serviceBlock,
			passwd:  "alice:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			service: true,
		},
		{
			name:    "readwrite identity with a lost cleartext refuses",
			acl:     "user alice\ntopic readwrite sensors/#\n\n" + serviceBlock,
			passwd:  "alice:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			service: true,
		},
		{
			name:    "lost readwrite identity beside a verifiable one refuses",
			acl:     "user alice\ntopic readwrite sensors/#\n\nuser bob\ntopic readwrite actuators/#\n\n" + serviceBlock,
			passwd:  "alice:$2a$10$h\nbob:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			known:   map[string]string{"bob": "bob-pass"},
			service: true,
		},
		{
			name:       "pattern-only policy is accepted through the fallback",
			acl:        "pattern readwrite clients/%u/state\n\n" + serviceBlock,
			passwd:     "alice:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			service:    true,
			wantAccept: true,
		},
		{
			name:       "deny-only policy is accepted through the fallback",
			acl:        "user alice\ntopic deny sensors/#\n\n" + serviceBlock,
			passwd:     "alice:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			service:    true,
			wantAccept: true,
		},
		{
			name:       "narrower grant with a known password is accepted through the fallback",
			acl:        "user alice\ntopic read sensors/#\n\n" + serviceBlock,
			passwd:     "alice:$2a$10$h\nsvc-mcm:$2a$10$h\n",
			known:      map[string]string{"alice": "alice-pass"},
			service:    true,
			wantAccept: true,
		},
		{
			name:       "a grant for a user that is not in the password file is accepted",
			acl:        "user carol\ntopic readwrite sensors/#\n\n" + serviceBlock,
			passwd:     "svc-mcm:$2a$10$h\n",
			service:    true,
			wantAccept: true,
		},
		{
			name:    "an empty policy without the service grant is refused",
			acl:     "",
			passwd:  "svc-mcm:$2a$10$h\n",
			service: false,
		},
		{
			name:    "a lost identity without a configured service account is refused",
			acl:     "user alice\ntopic readwrite sensors/#\n",
			passwd:  "alice:$2a$10$h\n",
			service: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			aclPath := filepath.Join(dir, "acl")
			passwdPath := filepath.Join(dir, "passwd")
			if err := os.WriteFile(aclPath, []byte(c.acl), 0o600); err != nil {
				t.Fatalf("write acl: %v", err)
			}
			if err := os.WriteFile(passwdPath, []byte(c.passwd), 0o600); err != nil {
				t.Fatalf("write passwd: %v", err)
			}
			lookup := newFakePasswordLookup()
			for username, password := range c.known {
				lookup.remember(username, password)
			}
			mosquittoCfg := config.MosquittoConfig{Host: "localhost", Port: 1883}
			if c.service {
				mosquittoCfg.Username = serviceFixtureUser
				mosquittoCfg.Password = serviceFixturePass
			}
			svc := NewService(
				&fakeApplier{},
				&fakeACLStore{},
				&fakeMQTTUserLister{},
				newFakeDeploymentStore(),
				diagnostics.VerifierFunc(okVerifier),
				lookup,
				mosquittoCfg,
				config.DeployConfig{Mode: "file", ACLPath: aclPath, PasswdPath: passwdPath, HealthcheckTimeout: 5 * time.Second},
				noAudit,
			)
			available, reason := svc.verificationAvailable(context.Background())
			if available != c.wantAccept {
				t.Fatalf("available = %v, want %v (reason %q)", available, c.wantAccept, reason)
			}
			if !c.wantAccept && !strings.Contains(reason, "MQTT verification unavailable") {
				t.Errorf("reason = %q, want the %q wording", reason, "MQTT verification unavailable")
			}
		})
	}
}
