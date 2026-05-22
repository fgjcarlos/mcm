package sparkplug

import "strings"

const Namespace = "spBv1.0"

var nodeMessageTypes = map[string]struct{}{
	"NBIRTH": {},
	"NDEATH": {},
	"NDATA":  {},
	"NCMD":   {},
}

var deviceMessageTypes = map[string]struct{}{
	"DBIRTH": {},
	"DDEATH": {},
	"DDATA":  {},
	"DCMD":   {},
}

// Metadata describes the Sparkplug B fields encoded in an MQTT topic.
// It is intentionally limited to topic-level information and does not decode
// Sparkplug protobuf payloads.
type Metadata struct {
	Namespace   string `json:"namespace"`
	GroupID     string `json:"group_id"`
	MessageType string `json:"message_type"`
	EdgeNodeID  string `json:"edge_node_id"`
	DeviceID    string `json:"device_id,omitempty"`
}

// ParseTopic parses supported Sparkplug B topic shapes:
//
//	spBv1.0/<group_id>/<message_type>/<edge_node_id>
//	spBv1.0/<group_id>/<message_type>/<edge_node_id>/<device_id>
//
// It returns false for non-Sparkplug topics, unsupported message types, empty
// topic levels, wildcard topic filters, or malformed Sparkplug-looking topics.
func ParseTopic(topic string) (Metadata, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) != 4 && len(parts) != 5 {
		return Metadata{}, false
	}
	if parts[0] != Namespace {
		return Metadata{}, false
	}
	for _, part := range parts {
		if part == "" || strings.ContainsAny(part, "+#") {
			return Metadata{}, false
		}
	}

	messageType := parts[2]
	metadata := Metadata{
		Namespace:   parts[0],
		GroupID:     parts[1],
		MessageType: messageType,
		EdgeNodeID:  parts[3],
	}

	if len(parts) == 4 {
		if _, ok := nodeMessageTypes[messageType]; !ok {
			return Metadata{}, false
		}
		return metadata, true
	}

	if _, ok := deviceMessageTypes[messageType]; !ok {
		return Metadata{}, false
	}
	metadata.DeviceID = parts[4]
	return metadata, true
}

// ClassifyTopic returns the parsed Sparkplug B topic metadata, if the topic
// matches a supported Sparkplug B namespace shape.
func ClassifyTopic(topic string) *Metadata {
	metadata, ok := ParseTopic(topic)
	if !ok {
		return nil
	}
	return &metadata
}
