// Package bridges models the Mosquitto bridge connections that link the
// managed broker to remote brokers (issue #303).
//
// A bridge is a "connection" section in mosquitto.conf:
//
//	connection upstream
//	address broker.example.org:1883
//	remote_username mcm
//	remote_password secret
//	topic both/# both 1 local/ remote/
//
// Mosquitto 2.0 reads every directive after a `connection` line as part of
// that connection until the next `connection` line. The directive names and
// the grammar of the `topic` line below were verified against Mosquitto
// 2.0.22 by loading rendered configurations with the real broker; the
// accepted grammar is:
//
//	topic <pattern> [<direction> <qos> [<local-prefix> [<remote-prefix>]]]
//
// where <direction> is one of "in", "out" or "both" and <qos> is 0, 1 or 2.
//
// Secrets (remote_password, local_password and the TLS pre-shared key) are
// write-only: the API never returns them, only whether they are set.
package bridges

import (
	"fmt"
	"sort"
	"strings"
)

// Direction is the flow of a topic rule between the local and the remote
// broker.
type Direction string

const (
	// DirectionIn forwards messages from the remote broker to the local one.
	DirectionIn Direction = "in"
	// DirectionOut forwards messages from the local broker to the remote one.
	DirectionOut Direction = "out"
	// DirectionBoth forwards messages in both directions.
	DirectionBoth Direction = "both"
)

// DefaultDirection is used when a topic rule does not state one.
const DefaultDirection = DirectionBoth

// Directions lists every accepted direction, in a stable order.
func Directions() []Direction {
	return []Direction{DirectionIn, DirectionOut, DirectionBoth}
}

// Valid reports whether d is a direction Mosquitto accepts.
func (d Direction) Valid() bool {
	switch d {
	case DirectionIn, DirectionOut, DirectionBoth:
		return true
	}
	return false
}

// Outbound reports whether the rule forwards messages towards the remote
// broker.
func (d Direction) Outbound() bool {
	return d == DirectionOut || d == DirectionBoth
}

// Inbound reports whether the rule forwards messages towards the local
// broker.
func (d Direction) Inbound() bool {
	return d == DirectionIn || d == DirectionBoth
}

// ProtocolVersions lists the bridge protocol versions Mosquitto 2.0 accepts
// for `bridge_protocol_version`.
func ProtocolVersions() []string {
	return []string{"mqttv31", "mqttv311", "mqttv50"}
}

// StartTypes lists the accepted values of `start_type`.
func StartTypes() []string {
	return []string{"automatic", "lazy", "once"}
}

// TLSVersions lists the accepted values of `bridge_tls_version`.
func TLSVersions() []string {
	return []string{"tlsv1.2", "tlsv1.3"}
}

// TopicRule is one `topic` line of a bridge.
type TopicRule struct {
	// Topic is the MQTT topic pattern that is bridged.
	Topic string `json:"topic"`
	// Direction is the flow of the rule. Empty means "both".
	Direction Direction `json:"direction,omitempty"`
	// QoS is the quality of service the rule forwards with (0, 1 or 2).
	QoS int `json:"qos"`
	// LocalPrefix is prepended to the topic on the local broker.
	LocalPrefix string `json:"local_prefix,omitempty"`
	// RemotePrefix is prepended to the topic on the remote broker.
	RemotePrefix string `json:"remote_prefix,omitempty"`
}

// EffectiveDirection returns the rule direction with the default applied.
func (t TopicRule) EffectiveDirection() Direction {
	if t.Direction == "" {
		return DefaultDirection
	}
	return t.Direction
}

// TLSOptions holds the transport security of a bridge connection. Every
// field maps to a `bridge_*` directive of the connection.
type TLSOptions struct {
	// CAFile is the CA certificate file used to verify the remote broker
	// (`bridge_cafile`).
	CAFile string `json:"ca_file,omitempty"`
	// CAPath is the directory of CA certificates (`bridge_capath`).
	CAPath string `json:"ca_path,omitempty"`
	// CertFile is the client certificate presented to the remote broker
	// (`bridge_certfile`).
	CertFile string `json:"cert_file,omitempty"`
	// KeyFile is the private key of the client certificate
	// (`bridge_keyfile`).
	KeyFile string `json:"key_file,omitempty"`
	// Insecure disables the verification of the remote certificate
	// (`bridge_insecure`).
	Insecure *bool `json:"insecure,omitempty"`
	// TLSVersion pins the TLS version (`bridge_tls_version`).
	TLSVersion string `json:"tls_version,omitempty"`
	// ALPN is the application protocol offered during the handshake
	// (`bridge_alpn`).
	ALPN string `json:"alpn,omitempty"`
	// PSK is the pre-shared key used instead of a certificate
	// (`bridge_psk`). It is a secret and is never returned by the API.
	PSK string `json:"psk,omitempty"`
	// PSKSet reports whether a pre-shared key is configured. It is
	// derived by the server and is never sent by the client.
	PSKSet bool `json:"psk_set,omitempty"`
	// Identity is the PSK identity (`bridge_identity`).
	Identity string `json:"identity,omitempty"`
	// RequireOCSP enables OCSP stapling checks (`bridge_require_ocsp`).
	RequireOCSP *bool `json:"require_ocsp,omitempty"`
	// OutgoingRetain keeps the retain flag on forwarded messages
	// (`bridge_outgoing_retain`).
	OutgoingRetain *bool `json:"outgoing_retain,omitempty"`
	// MaxPacketSize caps the size of the messages sent to the remote
	// broker (`bridge_max_packet_size`). Zero means unlimited.
	MaxPacketSize *int `json:"max_packet_size,omitempty"`
}

// Empty reports whether no TLS option is set at all.
func (t TLSOptions) Empty() bool {
	return t.CAFile == "" && t.CAPath == "" && t.CertFile == "" && t.KeyFile == "" &&
		t.Insecure == nil && t.TLSVersion == "" && t.ALPN == "" && t.PSK == "" &&
		t.Identity == "" && t.RequireOCSP == nil && t.OutgoingRetain == nil &&
		t.MaxPacketSize == nil
}

// Bridge is one connection to a remote broker.
type Bridge struct {
	// Name is the connection name (`connection <name>`).
	Name string `json:"name"`
	// Address is the remote broker address, `host` or `host:port`.
	Address string `json:"address"`
	// RemoteClientID is the client id the bridge uses on the remote broker
	// (`remote_clientid`).
	RemoteClientID string `json:"remote_client_id,omitempty"`
	// LocalClientID is the client id the bridge uses on the local broker
	// (`local_clientid`).
	LocalClientID string `json:"local_client_id,omitempty"`
	// Username authenticates the bridge on the remote broker
	// (`remote_username`).
	Username string `json:"username,omitempty"`
	// Password authenticates the bridge on the remote broker
	// (`remote_password`). It is a secret: the server never returns it.
	Password string `json:"password,omitempty"`
	// PasswordSet reports whether a remote password is configured. It is
	// derived by the server and is never sent by the client.
	PasswordSet bool `json:"password_set,omitempty"`
	// LocalUsername authenticates the bridge on the local broker
	// (`local_username`).
	LocalUsername string `json:"local_username,omitempty"`
	// LocalPassword authenticates the bridge on the local broker
	// (`local_password`). It is a secret: the server never returns it.
	LocalPassword string `json:"local_password,omitempty"`
	// LocalPasswordSet reports whether a local password is configured. It
	// is derived by the server and is never sent by the client.
	LocalPasswordSet bool `json:"local_password_set,omitempty"`
	// ProtocolVersion is the MQTT version spoken with the remote broker
	// (`bridge_protocol_version`).
	ProtocolVersion string `json:"protocol_version,omitempty"`
	// CleanSession controls the clean session flag of the remote
	// connection (`cleansession`).
	CleanSession *bool `json:"clean_session,omitempty"`
	// LocalCleanSession controls the clean session flag of the local
	// connection (`local_cleansession`).
	LocalCleanSession *bool `json:"local_clean_session,omitempty"`
	// KeepaliveSeconds is the keepalive of the remote connection
	// (`keepalive_interval`).
	KeepaliveSeconds *int `json:"keepalive_seconds,omitempty"`
	// RestartTimeout is the reconnection backoff. It is either a single
	// number of seconds or the "base cap" pair Mosquitto accepts
	// (`restart_timeout`).
	RestartTimeout string `json:"restart_timeout,omitempty"`
	// Notifications controls the connection notifications
	// (`notifications`).
	Notifications *bool `json:"notifications,omitempty"`
	// NotificationsLocalOnly keeps the notifications inside the local
	// broker (`notifications_local_only`).
	NotificationsLocalOnly *bool `json:"notifications_local_only,omitempty"`
	// NotificationTopic is the topic the notifications are published on
	// (`notification_topic`).
	NotificationTopic string `json:"notification_topic,omitempty"`
	// TryPrivate controls the private flag handling that avoids message
	// loops (`try_private`).
	TryPrivate *bool `json:"try_private,omitempty"`
	// StartType controls when the bridge connection is established
	// (`start_type`).
	StartType string `json:"start_type,omitempty"`
	// Threshold is the number of queued messages that triggers a
	// reconnection (`threshold`).
	Threshold *int `json:"threshold,omitempty"`
	// IdleTimeoutSeconds closes an idle connection (`idle_timeout`).
	IdleTimeoutSeconds *int `json:"idle_timeout_seconds,omitempty"`
	// RoundRobin spreads the messages over the remote addresses
	// (`round_robin`).
	RoundRobin *bool `json:"round_robin,omitempty"`
	// TLS holds the transport security of the connection.
	TLS TLSOptions `json:"tls"`
	// Topics are the topic rules of the connection.
	Topics []TopicRule `json:"topics"`
}

// Redacted returns a copy of b with every secret removed and the matching
// "*Set" flag raised. The API uses it so a credential never leaves the
// server.
func (b Bridge) Redacted() Bridge {
	out := b
	if out.Password != "" {
		out.PasswordSet = true
	}
	out.Password = ""
	if out.LocalPassword != "" {
		out.LocalPasswordSet = true
	}
	out.LocalPassword = ""
	if out.TLS.PSK != "" {
		out.TLS.PSKSet = true
	}
	out.TLS.PSK = ""
	return out
}

// Issue is one validation finding about a bridge document.
type Issue struct {
	// Kind groups the finding: "name", "address", "duplicate",
	// "dependency", "loop", "value" or "secret".
	Kind string `json:"kind"`
	// Severity is "error" or "warning". An error refuses the preview; a
	// warning is advisory and never blocks the apply.
	Severity string `json:"severity,omitempty"`
	// Bridge is the name of the bridge the finding belongs to.
	Bridge string `json:"bridge,omitempty"`
	// Field is the offending field of the bridge, when the finding is
	// about a single field.
	Field string `json:"field,omitempty"`
	// Topic is the offending topic rule, when the finding is about one.
	Topic string `json:"topic,omitempty"`
	// Message explains the finding in operator language.
	Message string `json:"message"`
}

// Issue kinds.
const (
	IssueKindName       = "name"
	IssueKindAddress    = "address"
	IssueKindDuplicate  = "duplicate"
	IssueKindDependency = "dependency"
	IssueKindLoop       = "loop"
	IssueKindValue      = "value"
	IssueKindSecret     = "secret"
)

// Issue severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
)

// IsError reports whether the finding refuses the preview.
func (i Issue) IsError() bool {
	return i.Severity != SeverityWarning
}

// HasErrors reports whether the findings contain at least one error.
func HasErrors(issues []Issue) bool {
	for _, issue := range issues {
		if issue.IsError() {
			return true
		}
	}
	return false
}

// Document is the complete set of managed bridges.
type Document struct {
	// Bridges are the managed connections, in configuration order.
	Bridges []Bridge `json:"bridges"`
}

// Redacted returns a copy of the document with every secret removed.
func (d Document) Redacted() Document {
	out := Document{Bridges: make([]Bridge, 0, len(d.Bridges))}
	for _, b := range d.Bridges {
		out.Bridges = append(out.Bridges, b.Redacted())
	}
	return out
}

// Clone returns a deep copy of the document. The deploy flow mutates the
// document while it merges the secrets the client did not resend, so the
// caller's copy must stay untouched.
func (d Document) Clone() Document {
	out := Document{Bridges: make([]Bridge, 0, len(d.Bridges))}
	for _, b := range d.Bridges {
		clone := b
		clone.Topics = append([]TopicRule(nil), b.Topics...)
		out.Bridges = append(out.Bridges, clone)
	}
	return out
}

// Find returns the bridge with the given name.
func (d Document) Find(name string) (Bridge, bool) {
	for _, b := range d.Bridges {
		if b.Name == name {
			return b, true
		}
	}
	return Bridge{}, false
}

// Host and Port split an address into its parts. The port defaults to the
// standard MQTT port when the address does not carry one.
func SplitAddress(address string) (host string, port int, err error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "", 0, fmt.Errorf("address is required")
	}
	if strings.Contains(trimmed, "://") {
		return "", 0, fmt.Errorf("address must not include a scheme")
	}
	if strings.ContainsAny(trimmed, " \t") {
		return "", 0, fmt.Errorf("address must not contain spaces")
	}
	if strings.Contains(trimmed, "/") {
		return "", 0, fmt.Errorf("address must not include a path")
	}

	host = trimmed
	port = 1883

	// Bracketed IPv6 literal, optionally with a port: [::1]:1883
	if strings.HasPrefix(trimmed, "[") {
		end := strings.Index(trimmed, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("address has an unterminated IPv6 literal")
		}
		host = trimmed[1:end]
		rest := trimmed[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", 0, fmt.Errorf("address has an unexpected suffix %q", rest)
			}
			parsed, err := parsePort(rest[1:])
			if err != nil {
				return "", 0, err
			}
			port = parsed
		}
		if host == "" {
			return "", 0, fmt.Errorf("address is missing the host")
		}
		return host, port, nil
	}

	// A bare IPv6 literal contains several colons and carries no port.
	if strings.Count(trimmed, ":") == 1 {
		hostPart, portPart, _ := strings.Cut(trimmed, ":")
		host = hostPart
		parsed, err := parsePort(portPart)
		if err != nil {
			return "", 0, err
		}
		port = parsed
	} else if strings.Count(trimmed, ":") > 1 {
		host = trimmed
	}

	if host == "" {
		return "", 0, fmt.Errorf("address is missing the host")
	}
	return host, port, nil
}

func parsePort(raw string) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("address is missing the port")
	}
	port := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("port %q is not a number", raw)
		}
		port = port*10 + int(r-'0')
		if port > 65535 {
			return 0, fmt.Errorf("port %q is out of range", raw)
		}
	}
	if port < 1 {
		return 0, fmt.Errorf("port %q is out of range", raw)
	}
	return port, nil
}

// SortIssues orders the findings deterministically so the API and the UI
// always present them in the same order.
func SortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if a.Bridge != b.Bridge {
			return a.Bridge < b.Bridge
		}
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Topic != b.Topic {
			return a.Topic < b.Topic
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Message < b.Message
	})
}
