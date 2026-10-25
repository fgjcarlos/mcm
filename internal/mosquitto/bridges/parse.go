package bridges

import (
	"strconv"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// connectionKeys are the directives Mosquitto only accepts inside a
// connection section. Every other directive of the file is a global
// setting and is never attributed to a bridge, even when it appears
// between the options of a connection — Mosquitto applies it globally.
var connectionKeys = map[string]struct{}{
	"address":                  {},
	"addresses":                {},
	"remote_clientid":          {},
	"local_clientid":           {},
	"remote_username":          {},
	"remote_password":          {},
	"local_username":           {},
	"local_password":           {},
	"bridge_protocol_version":  {},
	"cleansession":             {},
	"local_cleansession":       {},
	"keepalive_interval":       {},
	"restart_timeout":          {},
	"notifications":            {},
	"notifications_local_only": {},
	"notification_topic":       {},
	"try_private":              {},
	"start_type":               {},
	"threshold":                {},
	"idle_timeout":             {},
	"round_robin":              {},
	"bridge_cafile":            {},
	"bridge_capath":            {},
	"bridge_certfile":          {},
	"bridge_keyfile":           {},
	"bridge_insecure":          {},
	"bridge_tls_version":       {},
	"bridge_alpn":              {},
	"bridge_identity":          {},
	"bridge_psk":               {},
	"bridge_require_ocsp":      {},
	"bridge_outgoing_retain":   {},
	"bridge_max_packet_size":   {},
	"topic":                    {},
}

// IsConnectionKey reports whether the directive belongs to a connection
// section.
func IsConnectionKey(key string) bool {
	_, ok := connectionKeys[key]
	return ok
}

// Parse reads the managed bridges out of a parsed mosquitto.conf.
//
// Only the `connection` form is recognised: Mosquitto 2.0 rejects the
// `bridge { … }` block form, so a file that uses it is left untouched by
// MCM and reported as an unknown directive by the catalog validator.
func Parse(f *conf.File) Document {
	doc := Document{}
	if f == nil {
		return doc
	}

	var current *Bridge
	depth := 0
	for _, it := range f.Items {
		switch it.Kind {
		case conf.ItemBlockOpen:
			depth++
			continue
		case conf.ItemBlockClose:
			depth--
			continue
		case conf.ItemComment:
			continue
		}
		if depth != 0 || it.Kind != conf.ItemDirective {
			continue
		}

		if it.Key == "connection" {
			name := ""
			if len(it.Values) > 0 {
				name = unquote(it.Values[0])
			}
			doc.Bridges = append(doc.Bridges, Bridge{Name: name})
			current = &doc.Bridges[len(doc.Bridges)-1]
			continue
		}
		if current == nil || !IsConnectionKey(it.Key) {
			continue
		}
		applyOption(current, it)
	}

	return doc
}

// applyOption folds one connection directive into the bridge.
func applyOption(bridge *Bridge, it conf.Item) {
	values := it.Values
	first := ""
	if len(values) > 0 {
		first = unquote(values[0])
	}

	switch it.Key {
	case "address":
		if first != "" {
			bridge.Address = first
		}
	case "remote_clientid":
		bridge.RemoteClientID = first
	case "local_clientid":
		bridge.LocalClientID = first
	case "remote_username":
		bridge.Username = first
	case "remote_password":
		bridge.Password = first
	case "local_username":
		bridge.LocalUsername = first
	case "local_password":
		bridge.LocalPassword = first
	case "bridge_protocol_version":
		bridge.ProtocolVersion = first
	case "cleansession":
		bridge.CleanSession = parseBool(first)
	case "local_cleansession":
		bridge.LocalCleanSession = parseBool(first)
	case "keepalive_interval":
		bridge.KeepaliveSeconds = parseInt(first)
	case "restart_timeout":
		bridge.RestartTimeout = strings.Join(values, " ")
	case "notifications":
		bridge.Notifications = parseBool(first)
	case "notifications_local_only":
		bridge.NotificationsLocalOnly = parseBool(first)
	case "notification_topic":
		bridge.NotificationTopic = first
	case "try_private":
		bridge.TryPrivate = parseBool(first)
	case "start_type":
		bridge.StartType = first
	case "threshold":
		bridge.Threshold = parseInt(first)
	case "idle_timeout":
		bridge.IdleTimeoutSeconds = parseInt(first)
	case "round_robin":
		bridge.RoundRobin = parseBool(first)
	case "bridge_cafile":
		bridge.TLS.CAFile = first
	case "bridge_capath":
		bridge.TLS.CAPath = first
	case "bridge_certfile":
		bridge.TLS.CertFile = first
	case "bridge_keyfile":
		bridge.TLS.KeyFile = first
	case "bridge_insecure":
		bridge.TLS.Insecure = parseBool(first)
	case "bridge_tls_version":
		bridge.TLS.TLSVersion = first
	case "bridge_alpn":
		bridge.TLS.ALPN = first
	case "bridge_identity":
		bridge.TLS.Identity = first
	case "bridge_psk":
		bridge.TLS.PSK = first
	case "bridge_require_ocsp":
		bridge.TLS.RequireOCSP = parseBool(first)
	case "bridge_outgoing_retain":
		bridge.TLS.OutgoingRetain = parseBool(first)
	case "bridge_max_packet_size":
		bridge.TLS.MaxPacketSize = parseInt(first)
	case "topic":
		if rule, ok := parseTopic(values); ok {
			bridge.Topics = append(bridge.Topics, rule)
		}
	}
}

// parseTopic reads the arguments of a `topic` directive. The accepted
// grammar is "<pattern> [<direction> <qos> [<local-prefix> [<remote-prefix>]]]".
func parseTopic(values []string) (TopicRule, bool) {
	if len(values) == 0 {
		return TopicRule{}, false
	}
	rule := TopicRule{
		Topic:     unquote(values[0]),
		Direction: DirectionBoth,
	}
	if len(values) == 1 {
		return rule, true
	}

	// Mosquitto refuses the whole file when the direction is not one of
	// in, out or both. The parser keeps the value it found so the
	// operator still sees what the file contains, and the catalog
	// validator reports the error.
	rule.Direction = Direction(strings.ToLower(unquote(values[1])))
	if len(values) == 2 {
		return rule, true
	}

	if qos, err := strconv.Atoi(unquote(values[2])); err == nil {
		rule.QoS = qos
	}
	if len(values) == 3 {
		return rule, true
	}

	rule.LocalPrefix = unquote(values[3])
	if len(values) == 4 {
		return rule, true
	}

	rule.RemotePrefix = unquote(values[4])
	return rule, true
}

// unquote removes the surrounding double quotes of a token. The conf
// parser keeps the quotes so a quoted value survives the round-trip.
func unquote(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return value[1 : len(value)-1]
	}
	return value
}

func parseBool(value string) *bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

func parseInt(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil
	}
	return &parsed
}

// LocalAddresses returns the `host:port` pairs of the listeners of the
// managed broker. The bridge validator uses them to refuse a bridge that
// connects the broker to itself.
//
// Mosquitto accepts a listener both as a braced block ("listener 1883 { … }")
// and as a brace-less line ("listener 1883 0.0.0.0"), so both forms are
// collected.
func LocalAddresses(f *conf.File) []string {
	if f == nil {
		return nil
	}
	var out []string
	depth := 0
	for _, it := range f.Items {
		switch it.Kind {
		case conf.ItemBlockOpen:
			depth++
			continue
		case conf.ItemBlockClose:
			depth--
			continue
		case conf.ItemComment:
			continue
		}
		if depth != 0 || it.Kind != conf.ItemDirective || it.Key != "listener" {
			continue
		}
		if len(it.Values) == 0 {
			continue
		}
		port := unquote(it.Values[0])
		bind := ""
		if len(it.Values) > 1 {
			bind = unquote(it.Values[1])
		}
		if bind == "" {
			bind = "0.0.0.0"
		}
		out = append(out, bind+":"+port)
	}
	return out
}
