package server

import "time"

const (
	// MaxTopicEntries bounds in-memory topic history for the MVP explorer.
	MaxTopicEntries = 50
	// MaxPayloadPreviewBytes caps payload preview size before it is sent to the UI.
	MaxPayloadPreviewBytes = 2048
)

// SnapshotEventType is sent when a websocket client first connects.
const SnapshotEventType = "snapshot"

// BrokerStatus represents the current development broker connection state.
type BrokerStatus struct {
	State            string    `json:"state"`
	Connected        bool      `json:"connected"`
	BrokerURL        string    `json:"brokerUrl"`
	LastChangedAt    time.Time `json:"lastChangedAt"`
	LastError        string    `json:"lastError,omitempty"`
	ClientsConnected int       `json:"clientsConnected"`
	ClientsTotal     int       `json:"clientsTotal"`
	MessagesSeen     int64     `json:"messagesSeen"`
	ObservedTopics   int       `json:"observedTopics"`
}

// TopicActivity represents the most recent message observed for a topic.
type TopicActivity struct {
	Topic        string    `json:"topic"`
	Payload      string    `json:"payload"`
	PayloadBytes int       `json:"payloadBytes"`
	Truncated    bool      `json:"truncated"`
	IsJSON       bool      `json:"isJson"`
	SeenAt       time.Time `json:"seenAt"`
}

// Snapshot contains the dashboard and explorer state needed by the UI.
type Snapshot struct {
	Broker BrokerStatus    `json:"broker"`
	Topics []TopicActivity `json:"topics"`
}

// Event is the websocket envelope consumed by the frontend.
type Event struct {
	Type     string         `json:"type"`
	Snapshot *Snapshot      `json:"snapshot,omitempty"`
	Broker   *BrokerStatus  `json:"broker,omitempty"`
	Topic    *TopicActivity `json:"topic,omitempty"`
}
