package server

import "sync"

// Hub broadcasts server events to websocket clients.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

// NewHub creates an event broadcaster.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan Event]struct{}),
	}
}

// Subscribe registers a listener.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, 16)

	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	return ch
}

// Unsubscribe removes a listener.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
	h.mu.Unlock()
}

// Broadcast sends an event to all listeners, dropping slow consumers.
func (h *Hub) Broadcast(event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- event:
		default:
		}
	}
}
