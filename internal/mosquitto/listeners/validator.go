package listeners

import (
	"errors"
	"fmt"
	"net"
	"sort"
)

// Issue describes a validation error for one listener directive.
type Issue struct {
	Kind       string
	ListenerID string
	Directive  string
	Message    string
}

// OptionWarning describes a non-blocking concern in a listener spec.
// Common Kind values: "options_unknown_key".
type OptionWarning struct {
	Kind       string
	ListenerID string
	Key        string
	Message    string
}

// ErrInvalidSpec indicates that a listener specification cannot be used.
var ErrInvalidSpec = errors.New("invalid listener spec")

var optionWhitelist = map[string]struct{}{
	"max_connections": {},
	"http_dir":        {},
	"tls_version":     {},
	"cafile":          {},
	"certfile":        {},
	"keyfile":         {},
	"allow_anonymous": {},
}

// Validate reports blocking listener issues and non-blocking option warnings.
func Validate(specs []ListenerSpec) (issues []Issue, warnings []OptionWarning) {
	seen := make(map[string]struct{})
	for _, spec := range specs {
		if net.ParseIP(spec.Bind) == nil {
			issues = append(issues, Issue{
				Kind:       "bind",
				ListenerID: spec.ID,
				Message:    "bind must be an IP address",
			})
		}
		if spec.Port < 1 || spec.Port > 65535 {
			issues = append(issues, Issue{
				Kind:       "port_range",
				ListenerID: spec.ID,
				Message:    "port must be between 1 and 65535",
			})
		}
		if len(spec.Protocols) == 0 {
			issues = append(issues, Issue{
				Kind:       "protocols_required",
				ListenerID: spec.ID,
				Directive:  "protocol",
				Message:    "at least one protocol is required",
			})
		}
		for _, protocol := range spec.Protocols {
			if !protocol.IsValid() {
				issues = append(issues, Issue{
					Kind:       "protocols_unknown",
					ListenerID: spec.ID,
					Directive:  "protocol",
					Message:    fmt.Sprintf("unknown protocol %q", protocol),
				})
				continue
			}
			key := fmt.Sprintf("%d\x00%s\x00%s", spec.Port, spec.Bind, protocol)
			if _, duplicate := seen[key]; duplicate {
				issues = append(issues, Issue{
					Kind:       "duplicate",
					ListenerID: spec.ID,
					Directive:  "listener",
					Message:    fmt.Sprintf("duplicate listener for port %d, bind %s, protocol %s", spec.Port, spec.Bind, protocol),
				})
				continue
			}
			seen[key] = struct{}{}
		}
		for _, key := range sortedOptionKeys(spec.Options) {
			if _, known := optionWhitelist[key]; !known {
				warnings = append(warnings, OptionWarning{
					Kind:       "options_unknown_key",
					ListenerID: spec.ID,
					Key:        key,
					Message:    fmt.Sprintf("unknown option key %q", key),
				})
			}
		}
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].ListenerID != issues[j].ListenerID {
			return issues[i].ListenerID < issues[j].ListenerID
		}
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		return issues[i].Message < issues[j].Message
	})
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].ListenerID != warnings[j].ListenerID {
			return warnings[i].ListenerID < warnings[j].ListenerID
		}
		if warnings[i].Key != warnings[j].Key {
			return warnings[i].Key < warnings[j].Key
		}
		return warnings[i].Message < warnings[j].Message
	})
	return issues, warnings
}
