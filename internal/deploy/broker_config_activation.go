package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// ErrBrokerRestartUnavailable reports that the configured adapter cannot
// restart the broker. A change that needs a restart is refused instead of
// being written and left inactive behind a reload.
var ErrBrokerRestartUnavailable = errors.New("broker restart capability is unavailable")

// ErrBrokerVerificationFailed reports that the broker did not serve the
// expected access policy. It is distinct from "verification unavailable":
// the probe ran and failed, so the apply must not be reported as active.
var ErrBrokerVerificationFailed = errors.New("broker verification failed")

// classifyReload returns the worst reload_kind across the parsed conf and
// whether any restart-required directive has at least one occurrence.
func classifyReload(f *conf.File, cat ConfCatalog) (string, bool) {
	worst := "reload"
	requiresRestart := false
	for _, it := range f.Items {
		if it.Kind != conf.ItemDirective && it.Kind != conf.ItemBlockOpen {
			continue
		}
		spec, ok := cat.Lookup(it.Key)
		if !ok {
			continue
		}
		switch spec.ReloadKind {
		case "restart":
			worst = "restart"
			requiresRestart = true
		case "both":
			if worst != "restart" {
				worst = "both"
			}
		}
	}
	return worst, requiresRestart
}

// ErrSensitiveBrokerConfig reports a configuration with inline credentials.
// The graphical editor and the diffs would expose them, so MCM refuses the
// revision instead of showing the secret.
var ErrSensitiveBrokerConfig = errors.New("inline credentials are not supported by the graphical editor; externalize them before importing configuration")

// hasInlineCredentials reports whether the configuration carries a
// credential in a directive instead of referencing an external file.
func hasInlineCredentials(parsed *conf.File) bool {
	for _, item := range parsed.Items {
		if item.Kind != conf.ItemDirective {
			continue
		}
		key := item.Key
		if (strings.Contains(key, "password") && key != "password_file") || strings.Contains(key, "secret") || strings.Contains(key, "token") || key == "bridge_psk" {
			return true
		}
	}
	return false
}

// checkBrokerConfigIncludes refuses a revision whose include tree no longer
// matches the snapshot the preview was taken against.
func (s *Service) checkBrokerConfigIncludes(parsed *conf.File, serialized string) error {
	_, err := s.resolveBrokerConfigIncludes(parsed, serialized)
	return err
}

// resolveBrokerConfigIncludes verifies the snapshot and returns the same
// resolved tree for validation, so preview and apply judge directives in
// included files consistently.
func (s *Service) resolveBrokerConfigIncludes(parsed *conf.File, serialized string) (*conf.File, error) {
	var expected conf.IncludeSnapshot
	if err := json.Unmarshal([]byte(serialized), &expected); err != nil {
		return nil, ErrRevisionMismatch
	}
	resolved, actual, err := conf.NewIncludeResolver(filepath.Dir(s.confPath)).Resolve(parsed)
	if err != nil || !reflect.DeepEqual(expected, actual) {
		return nil, ErrRevisionMismatch
	}
	return resolved, nil
}

// verificationPrerequisites checks everything an activation probe needs
// before it can run: a configured verifier, readable managed files and a
// usable probe identity. It returns the file bodies so the caller does not
// read them twice.
func (s *Service) verificationPrerequisites(ctx context.Context) (aclBody, passwdBody string, available bool, reason string) {
	if s.verifier == nil {
		return "", "", false, "MQTT verification unavailable: no verifier is configured"
	}
	aclBody, err := s.readFile(s.deployCfg.ACLPath)
	if err != nil {
		return "", "", false, "MQTT verification unavailable: the managed ACL file is unreadable"
	}
	passwdBody, err = s.readFile(s.deployCfg.PasswdPath)
	if err != nil {
		return "", "", false, "MQTT verification unavailable: the managed password file is unreadable"
	}
	probe, ok := s.pickVerificationProbe(ctx, aclBody, passwdBody)
	// This surface refused the whole apply before the activation-probe
	// feature when MCM could not authenticate as an identity that owns a
	// user-scoped grant: the cleartext password of that identity only
	// lived in the process memory of MCM, so a restart of MCM lost it and
	// the policy of that identity could never be verified here. The probe
	// selection now skips such an identity instead of aborting (issue
	// #340), so the surface restores that refusal itself. The reason
	// names the identity, the cause and the remedy, so the operator knows
	// what to fix; the "MQTT verification unavailable" wording stays,
	// because a CI script greps for it.
	if probe.unverifiedGrantUser != "" {
		return "", "", false, "MQTT verification unavailable: no managed test identity with a known password and an ACL grant; " +
			unknownCleartextPasswordCause(probe.unverifiedGrantUser)
	}
	if !ok {
		return "", "", false, "MQTT verification unavailable: no managed test identity with a known password and an ACL grant"
	}
	return aclBody, passwdBody, true, ""
}

// verificationAvailable is the preflight of the broker-configuration apply.
// It reports whether the activation of a change could be observed at all.
// A change that can never be verified is refused before anything is
// written, because the operator would have no way to trust it.
func (s *Service) verificationAvailable(ctx context.Context) (bool, string) {
	_, _, available, reason := s.verificationPrerequisites(ctx)
	return available, reason
}

// verifyActivation runs the positive+negative MQTT probe when the deployment
// has a usable subject. It reports whether the probe ran at all so an
// unverifiable activation is never presented as verified.
func (s *Service) verifyActivation(ctx context.Context) (ok bool, ran bool, message string) {
	aclBody, passwdBody, available, reason := s.verificationPrerequisites(ctx)
	if !available {
		return false, false, reason
	}
	result, _, _ := s.runVerifier(ctx, aclBody, passwdBody)
	if strings.HasPrefix(result.Message, "no activation probe available") {
		return false, false, "MQTT verification unavailable: no managed test identity with a known password and an ACL grant"
	}
	if !result.OK {
		return false, true, "MQTT verification failed: " + result.Message
	}
	return true, true, ""
}

// activateBrokerConfig writes the reviewed revision, signals the broker and
// reports what the operator can actually trust: a verified activation, a
// pending activation with its reason, or a rollback that restored and
// re-verified the previous state.
func (s *Service) activateBrokerConfig(ctx context.Context, actor string, dep *storage.Deployment, before, after, kind string, requiresRestart bool) (storage.Deployment, error) {
	info, err := os.Stat(s.confPath)
	if err != nil {
		return storage.Deployment{}, err
	}
	perm := info.Mode().Perm() // Never broaden an existing file's permissions.
	if err := mosquitto.AtomicWriteConf(s.confPath, after, perm); err != nil {
		if statusErr := s.updateDeploymentStatus(dep.ID, "failed", "configuration write failed"); statusErr != nil {
			return storage.Deployment{}, errors.Join(err, statusErr)
		}
		return storage.Deployment{}, err
	}

	// Mosquitto reads a bridge and a plugin at start-up: a reload does not
	// create the connection and does not load the plugin, so a change that
	// needs a restart needs a restart. Every other change uses the reload
	// signal.
	needsRestart := requiresRestart || kindNeedsRestart(kind)
	restarted := false
	if needsRestart {
		restarted, err = s.restartBroker(ctx)
		if err != nil {
			return s.recoverBrokerConfig(ctx, actor, dep, before, perm, kind, needsRestart, fmt.Errorf("restart broker after broker-config apply: %w", err))
		}
	}
	if !restarted {
		if err := s.applier.ReloadBrokerOnly(); err != nil {
			return s.recoverBrokerConfig(ctx, actor, dep, before, perm, kind, needsRestart, fmt.Errorf("reload broker after broker-config apply: %w", err))
		}
	}

	// A change that was written but not restarted is not active yet: the
	// broker still runs the previous configuration. The deployment says so
	// instead of claiming a verified activation.
	if needsRestart && !restarted {
		message := "the change is written to the configuration, but the broker was only reloaded; restart it to activate it"
		if err := s.updateDeploymentStatus(dep.ID, "pending_activation", message); err != nil {
			return storage.Deployment{}, fmt.Errorf("update broker-config deployment to pending_activation: %w", err)
		}
		s.emitAudit(ctx, actor, "deployment.pending_activation", dep.ID, "success")
		return s.getDeployment(dep.ID)
	}

	if kind != "broker_config" {
		// The bridges, the plugins and the dynamic-security database are
		// not observable through the MQTT probe: nothing MCM can query
		// tells it that the bridge is connected or that the plugin is
		// loaded. The change is written and the broker was signalled, but
		// no verified reload or restart backs the claim, so the
		// deployment must not say "active_verified" (issue #293, S21).
		// The dynamic-security surface upgrades the deployment to
		// "active_verified" when the control channel confirms the
		// applied state.
		message := "the change was written and the broker was signalled, but MCM cannot verify this surface, so the change is not declared active"
		if err := s.updateDeploymentStatus(dep.ID, "pending_activation", message); err != nil {
			return storage.Deployment{}, fmt.Errorf("update broker-config deployment to pending_activation: %w", err)
		}
		s.emitAudit(ctx, actor, "deployment.pending_activation", dep.ID, "success")
		return s.getDeployment(dep.ID)
	}

	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok, ran, message := s.verifyActivation(verifyCtx)
	if !ran {
		// Honest state: the files were written and the broker was
		// signalled, but MQTT behavior could not be observed. This must
		// never be presented as a verified activation, and it is not a
		// failure either: the operator decides what to do with a change
		// MCM could not observe.
		if err := s.updateDeploymentStatus(dep.ID, "pending_activation", message); err != nil {
			return storage.Deployment{}, fmt.Errorf("update broker-config deployment to pending_activation: %w", err)
		}
		s.emitAudit(ctx, actor, "deployment.pending_activation", dep.ID, "success")
		return s.getDeployment(dep.ID)
	}
	if !ok {
		return s.recoverBrokerConfig(ctx, actor, dep, before, perm, kind, needsRestart, errors.New(message))
	}

	if err := s.updateDeploymentStatus(dep.ID, "active_verified", "MQTT allowed/denied access verified after activation; not an assertion about every directive"); err != nil {
		return storage.Deployment{}, fmt.Errorf("update broker-config deployment to active_verified: %w", err)
	}
	s.emitAudit(ctx, actor, "deployment.active_verified", dep.ID, "success")
	return s.getDeployment(dep.ID)
}

// recoverBrokerConfig restores the previous bytes, signals the broker again
// and reports whether the restored state could be confirmed. The recovery
// budget is detached from the request so a cancelled client cannot leave the
// broker with a half-restored configuration.
func (s *Service) recoverBrokerConfig(ctx context.Context, actor string, dep *storage.Deployment, before string, perm os.FileMode, kind string, needsRestart bool, cause error) (storage.Deployment, error) {
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	restoreErr := mosquitto.AtomicWriteConf(s.confPath, before, perm)
	message := "Configuration activation failed; previous bytes restored and the broker signalled again."
	if restoreErr == nil {
		restarted, restartErr := s.restartBroker(rollbackCtx)
		if restartErr != nil {
			restoreErr = restartErr
		} else if !restarted {
			restoreErr = s.applier.ReloadBrokerOnly()
		}
		if restoreErr == nil && !needsRestart {
			time.Sleep(reloadSettleDelay)
			ok, ran, detail := s.verifyActivation(rollbackCtx)
			switch {
			case ran && !ok:
				restoreErr = errors.New(detail)
			case !ran:
				message += " MQTT verification was unavailable, so the restored state is not confirmed active."
			default:
				message += " MQTT access verified after recovery."
			}
		}
	}
	status := "rolled_back"
	if restoreErr != nil {
		status = "rollback_failed"
		message = "Configuration activation and recovery failed; operator intervention required."
	}
	if err := s.updateDeploymentStatus(dep.ID, status, message); err != nil {
		return storage.Deployment{}, fmt.Errorf("%w: %v", ErrDeploymentStateIndeterminate, err)
	}
	s.emitAudit(rollbackCtx, actor, "deployment."+status, dep.ID, "failure")
	return s.getDeployment(dep.ID)
}
