package deploy

import (
	"testing"

	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
)

// recoveringFakeApplier wraps fakeApplier and simulates an applier that
// finds (or does not find) an interrupted apply at startup (issue #292, S19).
type recoveringFakeApplier struct {
	fakeApplier
	recovered bool
	err       error
}

func (r *recoveringFakeApplier) RecoverInterruptedApply() (bool, error) {
	return r.recovered, r.err
}

// TestRecoverInterruptedApplyAtStartupAuditsRecovery: when the applier can
// recover an interrupted apply, the deploy service audits the restoration
// at startup; when there is nothing to recover, no audit event is emitted.
func TestRecoverInterruptedApplyAtStartupAuditsRecovery(t *testing.T) {
	t.Parallel()

	t.Run("recovered", func(t *testing.T) {
		audit := &recordingAudit{}
		deployCfg, _, _ := enabledDeployCfg(t)
		svc := newTestServiceWithAudit(
			&recoveringFakeApplier{fakeApplier: fakeApplier{}, recovered: true},
			&fakeACLStore{},
			&fakeMQTTUserLister{},
			newFakeDeploymentStore(),
			diagnostics.VerifierFunc(okVerifier),
			&fakePasswordLookup{},
			deployCfg,
			audit,
		)
		svc.RecoverInterruptedApply()
		found := false
		for _, call := range audit.calls {
			if call.action == "deployment.recovered" && call.result == "success" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected a deployment.recovered success audit event, got %+v", audit.calls)
		}
	})

	t.Run("nothing to recover", func(t *testing.T) {
		audit := &recordingAudit{}
		deployCfg, _, _ := enabledDeployCfg(t)
		svc := newTestServiceWithAudit(
			&recoveringFakeApplier{fakeApplier: fakeApplier{}, recovered: false},
			&fakeACLStore{},
			&fakeMQTTUserLister{},
			newFakeDeploymentStore(),
			diagnostics.VerifierFunc(okVerifier),
			&fakePasswordLookup{},
			deployCfg,
			audit,
		)
		svc.RecoverInterruptedApply()
		if got := audit.actions(); len(got) != 0 {
			t.Errorf("expected no audit events, got %v", got)
		}
	})

	t.Run("applier without recovery", func(t *testing.T) {
		audit := &recordingAudit{}
		deployCfg, _, _ := enabledDeployCfg(t)
		svc := newTestServiceWithAudit(
			&fakeApplier{},
			&fakeACLStore{},
			&fakeMQTTUserLister{},
			newFakeDeploymentStore(),
			diagnostics.VerifierFunc(okVerifier),
			&fakePasswordLookup{},
			deployCfg,
			audit,
		)
		svc.RecoverInterruptedApply()
		if got := audit.actions(); len(got) != 0 {
			t.Errorf("expected no audit events, got %v", got)
		}
	})
}

// Compile-time guard: the real appliers implement the recovery interface.
var (
	_ mosquitto.InterruptedApplyRecoverer = mosquitto.FileApplier{}
	_ mosquitto.InterruptedApplyRecoverer = mosquitto.DockerApplier{}
)
