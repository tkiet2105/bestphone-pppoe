// Package events — in-process pub/sub. Bounded buffer per subscriber (drop policy nếu chậm).
package events

import (
	"encoding/json"
	"sync"
)

type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: make(map[chan Event]struct{})}
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(eventType string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := Event{Type: eventType, Data: b}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// drop nếu subscriber chậm — không block publisher
		}
	}
}
