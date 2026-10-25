package bridges

import (
	"strconv"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// optionIndent is the indentation MCM uses for the options of a
// connection. Mosquitto ignores it; it keeps the rendered file readable
// and makes the connection sections easy to spot.
const optionIndent = 4

// Apply replaces the connection sections of a parsed mosquitto.conf with
// the bridges of the document and returns the new file.
//
// Everything the document does not own is preserved byte for byte:
// comments, blank lines, global directives, listener blocks, include
// order and directives the catalog does not know. Only the `connection`
// lines and the connection scoped directives that belong to them are
// rewritten, and they are written back at the position of the first
// connection they replace.
//
// The input file is never mutated.
func Apply(f *conf.File, doc Document) *conf.File {
	if f == nil {
		return nil
	}

	kept := make([]conf.Item, 0, len(f.Items))
	insertAt := -1
	depth := 0
	inConnection := false

	for _, it := range f.Items {
		switch it.Kind {
		case conf.ItemBlockOpen:
			depth++
			kept = append(kept, it)
			continue
		case conf.ItemBlockClose:
			depth--
			kept = append(kept, it)
			continue
		case conf.ItemComment:
			kept = append(kept, it)
			continue
		}

		if depth != 0 || it.Kind != conf.ItemDirective {
			kept = append(kept, it)
			continue
		}

		if it.Key == "connection" {
			inConnection = true
			if insertAt < 0 {
				insertAt = len(kept)
			}
			continue
		}
		if inConnection && IsConnectionKey(it.Key) {
			if insertAt < 0 {
				insertAt = len(kept)
			}
			continue
		}
		kept = append(kept, it)
	}

	rendered := renderDocument(doc)
	if insertAt < 0 {
		// No connection existed yet: the bridges are appended at the end
		// of the file, after a blank line so the section is readable.
		if len(rendered) > 0 {
			if len(kept) > 0 {
				kept = append(kept, conf.Item{Kind: conf.ItemComment, Text: "\n", Line: -1})
			}
			kept = append(kept, rendered...)
		}
	} else {
		out := make([]conf.Item, 0, len(kept)+len(rendered))
		out = append(out, kept[:insertAt]...)
		out = append(out, rendered...)
		out = append(out, kept[insertAt:]...)
		kept = out
	}

	return &conf.File{Items: kept, Path: f.Path}
}

// renderDocument turns the document into the conf items of its bridges.
func renderDocument(doc Document) []conf.Item {
	var items []conf.Item
	for _, bridge := range doc.Bridges {
		items = append(items, renderBridge(bridge)...)
	}
	return items
}

// renderBridge turns one bridge into the conf items of its connection
// section.
func renderBridge(bridge Bridge) []conf.Item {
	items := []conf.Item{{
		Kind:   conf.ItemDirective,
		Key:    "connection",
		Values: []string{bridge.Name},
		Line:   -1,
	}}

	option := func(key string, values ...string) {
		// An option that is not set is omitted entirely; Mosquitto then
		// applies its own default.
		kept := values[:0:0]
		for _, value := range values {
			if value != "" {
				kept = append(kept, value)
			}
		}
		if len(kept) == 0 {
			return
		}
		items = append(items, conf.Item{
			Kind:   conf.ItemDirective,
			Key:    key,
			Values: kept,
			Indent: optionIndent,
			Line:   -1,
		})
	}

	option("address", bridge.Address)
	option("remote_clientid", bridge.RemoteClientID)
	option("local_clientid", bridge.LocalClientID)
	option("remote_username", bridge.Username)
	option("remote_password", bridge.Password)
	option("local_username", bridge.LocalUsername)
	option("local_password", bridge.LocalPassword)
	option("bridge_protocol_version", bridge.ProtocolVersion)
	option("cleansession", formatBool(bridge.CleanSession))
	option("local_cleansession", formatBool(bridge.LocalCleanSession))
	option("keepalive_interval", formatInt(bridge.KeepaliveSeconds))
	option("restart_timeout", bridge.RestartTimeout)
	option("notifications", formatBool(bridge.Notifications))
	option("notifications_local_only", formatBool(bridge.NotificationsLocalOnly))
	option("notification_topic", bridge.NotificationTopic)
	option("try_private", formatBool(bridge.TryPrivate))
	option("start_type", bridge.StartType)
	option("threshold", formatInt(bridge.Threshold))
	option("idle_timeout", formatInt(bridge.IdleTimeoutSeconds))
	option("round_robin", formatBool(bridge.RoundRobin))
	option("bridge_cafile", bridge.TLS.CAFile)
	option("bridge_capath", bridge.TLS.CAPath)
	option("bridge_certfile", bridge.TLS.CertFile)
	option("bridge_keyfile", bridge.TLS.KeyFile)
	option("bridge_insecure", formatBool(bridge.TLS.Insecure))
	option("bridge_tls_version", bridge.TLS.TLSVersion)
	option("bridge_alpn", bridge.TLS.ALPN)
	option("bridge_identity", bridge.TLS.Identity)
	option("bridge_psk", bridge.TLS.PSK)
	option("bridge_require_ocsp", formatBool(bridge.TLS.RequireOCSP))
	option("bridge_outgoing_retain", formatBool(bridge.TLS.OutgoingRetain))
	option("bridge_max_packet_size", formatInt(bridge.TLS.MaxPacketSize))

	for _, rule := range bridge.Topics {
		items = append(items, renderTopic(rule))
	}
	return items
}

// renderTopic turns one topic rule into the conf item of its `topic`
// directive.
func renderTopic(rule TopicRule) conf.Item {
	values := []string{rule.Topic}
	direction := rule.EffectiveDirection()
	if direction != "" {
		values = append(values, string(direction))
		values = append(values, strconv.Itoa(rule.QoS))
		if rule.LocalPrefix != "" {
			values = append(values, rule.LocalPrefix)
			if rule.RemotePrefix != "" {
				values = append(values, rule.RemotePrefix)
			}
		}
	}
	return conf.Item{
		Kind:   conf.ItemDirective,
		Key:    "topic",
		Values: values,
		Indent: optionIndent,
		Line:   -1,
	}
}

// Render returns the conf text of a document. It is used by the tests and
// by the callers that need the bridge section without the rest of the
// file.
func Render(doc Document) string {
	items := renderDocument(doc)
	var buf strings.Builder
	for _, it := range items {
		for i := 0; i < it.Indent; i++ {
			buf.WriteByte(' ')
		}
		buf.WriteString(it.Key)
		for _, value := range it.Values {
			buf.WriteByte(' ')
			buf.WriteString(value)
		}
		buf.WriteByte('\n')
	}
	return buf.String()
}

func formatBool(value *bool) string {
	if value == nil {
		return ""
	}
	return strconv.FormatBool(*value)
}

func formatInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}
