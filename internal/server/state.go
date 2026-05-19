package server

import (
	"sort"
	"strconv"
	"sync"
	"time"
)

// StateStore maintains the dashboard snapshot consumed by websocket clients.
type StateStore struct {
	mu     sync.RWMutex
	broker BrokerStatus
	topics map[string]TopicActivity
}

// NewStateStore creates a store seeded with a disconnected broker state.
func NewStateStore(brokerURL string) *StateStore {
	return &StateStore{
		broker: BrokerStatus{
			State:         "disconnected",
			BrokerURL:     brokerURL,
			LastChangedAt: time.Now().UTC(),
		},
		topics: make(map[string]TopicActivity),
	}
}

// Snapshot returns a copy of the current state.
func (s *StateStore) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	topics := make([]TopicActivity, 0, len(s.topics))
	for _, topic := range s.topics {
		topics = append(topics, topic)
	}

	sort.Slice(topics, func(i, j int) bool {
		return topics[i].SeenAt.After(topics[j].SeenAt)
	})

	if len(topics) > MaxTopicEntries {
		topics = topics[:MaxTopicEntries]
	}

	broker := s.broker
	broker.ObservedTopics = len(s.topics)

	return Snapshot{
		Broker: broker,
		Topics: topics,
	}
}

// SetBrokerConnected updates the broker connectivity state.
func (s *StateStore) SetBrokerConnected(connected bool, lastErr string) BrokerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := "disconnected"
	if connected {
		state = "connected"
		lastErr = ""
	}

	s.broker.State = state
	s.broker.Connected = connected
	s.broker.LastError = lastErr
	s.broker.LastChangedAt = time.Now().UTC()

	return s.copyBrokerLocked()
}

// UpdateSystemTopic folds selected $SYS values into the broker status.
func (s *StateStore) UpdateSystemTopic(topic string, payload []byte) (BrokerStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value, err := strconv.Atoi(string(payload))
	if err != nil {
		return BrokerStatus{}, false
	}

	switch topic {
	case "$SYS/broker/clients/connected":
		s.broker.ClientsConnected = value
	case "$SYS/broker/clients/total":
		s.broker.ClientsTotal = value
	default:
		return BrokerStatus{}, false
	}

	return s.copyBrokerLocked(), true
}

// AddTopicMessage records topic activity and increments message counters.
func (s *StateStore) AddTopicMessage(topic string, payload []byte) TopicActivity {
	s.mu.Lock()
	defer s.mu.Unlock()

	preview, truncated, isJSON := formatPayloadPreview(payload, MaxPayloadPreviewBytes)
	activity := TopicActivity{
		Topic:        topic,
		Payload:      preview,
		PayloadBytes: len(payload),
		Truncated:    truncated,
		IsJSON:       isJSON,
		SeenAt:       time.Now().UTC(),
	}

	s.topics[topic] = activity
	s.broker.MessagesSeen++

	if len(s.topics) > MaxTopicEntries {
		s.trimTopicsLocked()
	}

	return activity
}

func (s *StateStore) trimTopicsLocked() {
	if len(s.topics) <= MaxTopicEntries {
		return
	}

	type pair struct {
		topic string
		seen  time.Time
	}

	entries := make([]pair, 0, len(s.topics))
	for topic, item := range s.topics {
		entries = append(entries, pair{topic: topic, seen: item.SeenAt})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].seen.After(entries[j].seen)
	})

	for _, entry := range entries[MaxTopicEntries:] {
		delete(s.topics, entry.topic)
	}
}

func (s *StateStore) copyBrokerLocked() BrokerStatus {
	broker := s.broker
	broker.ObservedTopics = len(s.topics)
	return broker
}
