package writer

import (
	"fmt"
	"os"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// perListenerSettingsDirective is the global Mosquitto directive that turns
// the authentication and access control options into per-listener options.
//
// Mosquitto 2.0 only honours an `acl_file` declared inside a `listener`
// block when this directive is enabled. Without it the broker keeps using
// the global `acl_file` for every listener, so a listener that owns scoped
// ACL rules would silently keep the broker-wide access instead of its own.
const perListenerSettingsDirective = "per_listener_settings"

// aclFileOption is the listener option that points at the ACL file of one
// listener.
const aclFileOption = "acl_file"

// passwordFileOption is the listener option that points at the password
// file of one listener.
const passwordFileOption = "password_file"

// securityDirectives are the configuration directives Mosquitto treats as
// security settings.
//
// Mosquitto only honours `per_listener_settings` when the directive is set
// before the first security setting: with the directive after them the
// broker refuses to start with "per_listener_settings must be set before
// any other security settings."
var securityDirectives = map[string]struct{}{
	"allow_anonymous": {},
	"password_file":   {},
	"acl_file":        {},
	"psk_file":        {},
	"auth_plugin":     {},
}

// ErrPerListenerSettingsDisabled is returned when the configuration turns
// per-listener settings off while a listener declares its own ACL file.
//
// MCM refuses the change instead of writing a configuration whose scoped
// rules would never apply: the operator would see a successful apply and a
// listener that still serves the broker-wide access.
var ErrPerListenerSettingsDisabled = fmt.Errorf(
	"per_listener_settings must be true when a listener declares its own acl_file",
)

// ErrACLFileNotWritten is returned when a listener declares an ACL file MCM
// has not written yet.
//
// Mosquitto refuses to start when the ACL file of a listener is missing, so
// applying the listener configuration before the deployment would take the
// broker down. MCM refuses the change instead: the operator sees the error
// and applies the deployment first.
var ErrACLFileNotWritten = fmt.Errorf("the acl file of a listener has not been written yet")

// ErrDefaultListenerScope is returned when the listener on the default port
// owns a file of its own.
//
// Mosquitto always creates its default listener — the one on the default
// port — with the global access control once the per-listener settings are
// enabled, so a listener of the configuration on that port cannot exist at
// all. The scoped rules of such a listener would never apply.
var ErrDefaultListenerScope = fmt.Errorf(
	"the listener on the default port cannot own an acl file of its own",
)

// defaultListenerPort is the port of the listener Mosquitto creates when
// the configuration declares none.
//
// Once the per-listener settings are enabled, Mosquitto creates that
// listener — with the global authentication and access control — next to
// the listeners of the configuration. A listener of the configuration on the
// same port would collide with it and the broker would refuse to start, so
// the writer drops it.
const defaultListenerPort = 1883

// dropDefaultListenerSpec removes the listener on the default port from the
// specs the writer renders.
//
// Mosquitto creates that listener itself once the per-listener settings are
// enabled, with the global authentication and access control: the broker
// keeps serving the port and the drop does not change the access it
// offers. A listener on that port that owns a file of its own is refused
// instead: Mosquitto would ignore the file and the scoped rules would never
// apply.
func dropDefaultListenerSpec(specs []listeners.ListenerSpec) ([]listeners.ListenerSpec, error) {
	out := make([]listeners.ListenerSpec, 0, len(specs))
	for _, spec := range specs {
		if spec.Port != defaultListenerPort {
			out = append(out, spec)
			continue
		}
		if strings.TrimSpace(spec.Options[aclFileOption]) != "" {
			return nil, fmt.Errorf("%w: listener %q", ErrDefaultListenerScope, spec.ID)
		}
	}
	return out, nil
}

// specsDeclareACLFile reports whether any listener declares its own ACL
// file.
func specsDeclareACLFile(specs []listeners.ListenerSpec) bool {
	for _, spec := range specs {
		if strings.TrimSpace(spec.Options[aclFileOption]) != "" {
			return true
		}
	}
	return false
}

// inheritGlobalAccessControl copies the global authentication and access
// control settings into the listener blocks that do not declare their own.
//
// Mosquitto resolves the authentication and the access control per
// listener once the per-listener settings are enabled: a listener block
// that declares neither a password file nor an ACL file has no access
// control at all, and a client that authenticates is refused. Copying the
// global values keeps the broker-wide accounts and the broker-wide rules
// in place for the listeners that do not own a scoped ACL file, while a
// listener that declares its own ACL file keeps it.
func inheritGlobalAccessControl(specs []listeners.ListenerSpec, head string) []listeners.ListenerSpec {
	values := globalDirectiveValues(head, passwordFileOption, aclFileOption)
	if len(values) == 0 {
		return specs
	}
	out := make([]listeners.ListenerSpec, 0, len(specs))
	for _, spec := range specs {
		options := make(map[string]string, len(spec.Options)+len(values))
		for key, value := range spec.Options {
			options[key] = value
		}
		for _, key := range []string{passwordFileOption, aclFileOption} {
			if _, declared := options[key]; declared {
				continue
			}
			if value, ok := values[key]; ok && value != "" {
				options[key] = value
			}
		}
		spec.Options = options
		out = append(out, spec)
	}
	return out
}

// globalDirectiveValues returns the value of every requested global
// directive of the configuration head.
func globalDirectiveValues(head string, keys ...string) map[string]string {
	if strings.TrimSpace(head) == "" {
		return nil
	}
	parsed, err := conf.ParseString(head, "")
	if err != nil {
		return nil
	}
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[key] = struct{}{}
	}
	values := make(map[string]string, len(keys))
	for _, item := range parsed.Items {
		if item.Kind != conf.ItemDirective {
			continue
		}
		if _, tracked := wanted[item.Key]; !tracked {
			continue
		}
		if _, found := values[item.Key]; found {
			continue
		}
		if len(item.Values) == 0 {
			continue
		}
		values[item.Key] = item.Values[0]
	}
	return values
}

// ensurePerListenerSettings returns the configuration head with the
// per-listener settings directive enabled.
//
// The directive is inserted before the first security setting of the
// configuration, because Mosquitto only honours it there: a directive that
// follows `allow_anonymous`, `password_file` or `acl_file` makes the broker
// refuse to start. A configuration that sets it to false is refused as well:
// Mosquitto would ignore the listener-local `acl_file` and serve the
// broker-wide access on that listener, which is not what the operator asked
// for. A directive that is already enabled but misplaced is moved to the
// position Mosquitto requires.
func ensurePerListenerSettings(head string) (string, error) {
	if strings.TrimSpace(head) == "" {
		return "per_listener_settings true", nil
	}
	parsed, err := conf.ParseString(head, "")
	if err != nil {
		return "", fmt.Errorf("parse config for per-listener settings: %w", err)
	}
	for _, item := range parsed.Items {
		if item.Kind != conf.ItemDirective || item.Key != perListenerSettingsDirective {
			continue
		}
		if len(item.Values) == 0 {
			return "", fmt.Errorf("%w: %s has no value", ErrPerListenerSettingsDisabled, perListenerSettingsDirective)
		}
		value := strings.ToLower(strings.TrimSpace(item.Values[0]))
		switch value {
		case "true", "yes", "on", "1":
			return movePerListenerSettings(parsed)
		default:
			return "", fmt.Errorf("%w: found %s %s", ErrPerListenerSettingsDisabled, perListenerSettingsDirective, value)
		}
	}
	parsed.Items = insertPerListenerSettings(parsed.Items)
	rendered, err := parsed.Render()
	if err != nil {
		return "", fmt.Errorf("render config with per-listener settings: %w", err)
	}
	return strings.TrimRight(string(rendered), "\n"), nil
}

// movePerListenerSettings re-renders a configuration whose
// per-listener settings directive is enabled but sits behind a security
// setting. The directive is moved to the position Mosquitto requires.
func movePerListenerSettings(parsed *conf.File) (string, error) {
	items := make([]conf.Item, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		if item.Kind == conf.ItemDirective && item.Key == perListenerSettingsDirective {
			continue
		}
		items = append(items, item)
	}
	parsed.Items = insertPerListenerSettings(items)
	rendered, err := parsed.Render()
	if err != nil {
		return "", fmt.Errorf("render config with per-listener settings: %w", err)
	}
	return strings.TrimRight(string(rendered), "\n"), nil
}

// insertPerListenerSettings inserts the per-listener settings directive
// before the first security setting of the configuration.
//
// A configuration without any security setting keeps the directive at the
// end: there is nothing it has to precede.
func insertPerListenerSettings(items []conf.Item) []conf.Item {
	directive := conf.Item{
		Kind:   conf.ItemDirective,
		Key:    perListenerSettingsDirective,
		Values: []string{"true"},
		Line:   -1,
	}
	at := len(items)
	for i, item := range items {
		if item.Kind != conf.ItemDirective {
			continue
		}
		if _, security := securityDirectives[item.Key]; !security {
			continue
		}
		at = i
		break
	}
	out := make([]conf.Item, 0, len(items)+1)
	out = append(out, items[:at]...)
	out = append(out, directive)
	out = append(out, items[at:]...)
	return out
}

// resolveBrokerACLPaths rewrites the ACL file of every listener that owns
// a MCM-managed file into the path the broker reads.
//
// MCM writes the file of a listener next to the broker-wide ACL file, but
// the path MCM uses is the path in MCM's own filesystem. In a container
// deployment the shared volume is mounted under a different path in each
// container ("/var/lib/mosquitto-config/acl" for MCM, "/mosquitto/config/acl"
// for the broker), so the broker reads the file under its own path.
//
// The broker-wide ACL file of the configuration head names the path the
// broker uses, so the file of a listener is resolved against the same
// directory. A file MCM does not manage (an operator-provided path) keeps
// the value it was given.
func resolveBrokerACLPaths(specs []listeners.ListenerSpec, head string) []listeners.ListenerSpec {
	values := globalDirectiveValues(head, aclFileOption)
	globalACLFile := strings.TrimSpace(values[aclFileOption])
	if globalACLFile == "" {
		return specs
	}
	out := make([]listeners.ListenerSpec, 0, len(specs))
	for _, spec := range specs {
		declared := strings.TrimSpace(spec.Options[aclFileOption])
		brokerPath, managed := mosquitto.BrokerACLPath(declared, globalACLFile, spec.ID)
		if managed {
			options := make(map[string]string, len(spec.Options))
			for key, value := range spec.Options {
				options[key] = value
			}
			options[aclFileOption] = brokerPath
			spec.Options = options
		}
		out = append(out, spec)
	}
	return out
}

// requireWrittenACLFiles refuses a listener whose MCM-managed ACL file MCM
// has not written yet.
//
// Mosquitto refuses to start when the ACL file of a listener is missing, so
// a configuration that declares a file that is not there takes the broker
// down. The file is written by the deploy, so the operator has to apply the
// deployment before the listener configuration.
//
// A file MCM does not manage (an operator-provided path) is the operator's
// business and is not checked.
func requireWrittenACLFiles(specs []listeners.ListenerSpec, head string) error {
	values := globalDirectiveValues(head, aclFileOption)
	globalACLFile := strings.TrimSpace(values[aclFileOption])
	if globalACLFile == "" {
		return nil
	}
	for _, spec := range specs {
		declared := strings.TrimSpace(spec.Options[aclFileOption])
		if declared == "" {
			continue
		}
		if _, managed := mosquitto.BrokerACLPath(declared, globalACLFile, spec.ID); !managed {
			continue
		}
		if _, err := os.Stat(declared); err != nil {
			return fmt.Errorf("%w: listener %q declares %q: %v", ErrACLFileNotWritten, spec.ID, declared, err)
		}
	}
	return nil
}
