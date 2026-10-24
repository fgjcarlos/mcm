package writer

import (
	"fmt"
	"strings"

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

// ErrPerListenerSettingsDisabled is returned when the configuration turns
// per-listener settings off while a listener declares its own ACL file.
//
// MCM refuses the change instead of writing a configuration whose scoped
// rules would never apply: the operator would see a successful apply and a
// listener that still serves the broker-wide access.
var ErrPerListenerSettingsDisabled = fmt.Errorf(
	"per_listener_settings must be true when a listener declares its own acl_file",
)

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
// The directive is appended when it is missing and kept when it is already
// true. A configuration that sets it to false is refused: Mosquitto would
// ignore the listener-local `acl_file` and serve the broker-wide access on
// that listener, which is not what the operator asked for.
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
			return head, nil
		default:
			return "", fmt.Errorf("%w: found %s %s", ErrPerListenerSettingsDisabled, perListenerSettingsDirective, value)
		}
	}
	parsed.Items = append(parsed.Items, conf.Item{
		Kind:   conf.ItemDirective,
		Key:    perListenerSettingsDirective,
		Values: []string{"true"},
		Line:   -1,
	})
	rendered, err := parsed.Render()
	if err != nil {
		return "", fmt.Errorf("render config with per-listener settings: %w", err)
	}
	return strings.TrimRight(string(rendered), "\n"), nil
}
