package deploy

import (
	"context"
	"fmt"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
)

// RestartApplier adds a CONTROLLED restart to an applier that can only
// reload the broker.
//
// The file applier reloads the broker through the configured reload
// command. A reload does not restart the broker, so a change Mosquitto
// reads only at start-up — a bridge, a plugin, the dynamic security
// plugin — stayed inactive while the deploy reported it as applied.
//
// This wrapper keeps the reload path untouched and adds a separate
// restart command, so a deployment that needs a restart either performs
// a real restart or refuses the change (issue #294).
//
// The wrapper is transparent for every other capability of the wrapped
// applier, including the durable interrupted-apply recovery of issue
// #292: the recovery is delegated to the inner applier.
type RestartApplier struct {
	// Inner is the applier that writes the files and reloads the broker.
	Inner mosquitto.Applier
	// RestartCommand is the command + argv that restarts the broker.
	// Empty means the deployment cannot restart the broker.
	RestartCommand []string
	// Runner executes the restart command. Nil means mosquitto.ExecRunner{}.
	Runner mosquitto.CommandRunner
}

// NewRestartApplier builds a RestartApplier around inner.
func NewRestartApplier(inner mosquitto.Applier, restartCommand []string, runner mosquitto.CommandRunner) RestartApplier {
	return RestartApplier{Inner: inner, RestartCommand: restartCommand, Runner: runner}
}

// Apply delegates the transactional write and reload to the inner applier.
func (a RestartApplier) Apply(ctx context.Context, req mosquitto.ApplyRequest) error {
	return a.Inner.Apply(ctx, req)
}

// ReloadBrokerOnly delegates the reload to the inner applier.
func (a RestartApplier) ReloadBrokerOnly() error {
	return a.Inner.ReloadBrokerOnly()
}

// RecoverInterruptedApply delegates the startup recovery of an
// interrupted apply to the inner applier. An applier without a recovery
// journal reports that nothing was recovered.
func (a RestartApplier) RecoverInterruptedApply() (bool, error) {
	recoverer, ok := a.Inner.(mosquitto.InterruptedApplyRecoverer)
	if !ok {
		return false, nil
	}
	return recoverer.RecoverInterruptedApply()
}

// RestartBroker restarts the broker through the configured restart
// command.
//
// Without a restart command the wrapper reports
// mosquitto.ErrRestartNotSupported, exactly like an applier that cannot
// bounce the broker: the deploy service then refuses a change that needs
// a restart instead of writing the file and reloading.
func (a RestartApplier) RestartBroker(ctx context.Context) error {
	if len(a.RestartCommand) == 0 {
		return mosquitto.ErrRestartNotSupported
	}
	runner := a.Runner
	if runner == nil {
		runner = mosquitto.ExecRunner{}
	}
	name := a.RestartCommand[0]
	args := a.RestartCommand[1:]
	if _, err := runner.Run(ctx, name, args...); err != nil {
		return fmt.Errorf("restart command %q: %w", name, err)
	}
	return nil
}

// CanRestart reports whether the deployment can really restart the
// broker. It is false without a restart command, even when the inner
// applier could reload.
func (a RestartApplier) CanRestart() bool {
	return len(a.RestartCommand) > 0
}
