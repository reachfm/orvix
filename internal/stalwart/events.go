package stalwart

import (
	"encoding/json"
	"sync"
)

type EventType string

const (
	EventDelivery   EventType = "delivery"
	EventBounce     EventType = "bounce"
	EventReject     EventType = "reject"
	EventSpam       EventType = "spam"
	EventAuth       EventType = "auth"
	EventQueue      EventType = "queue"
	EventConnection EventType = "connection"
	EventError      EventType = "error"
)

type Event struct {
	Type      EventType `json:"type"`
	Source    string    `json:"source,omitempty"`
	MessageID string    `json:"message_id,omitempty"`
	FromAddr  string    `json:"from_addr,omitempty"`
	ToAddr    string    `json:"to_addr,omitempty"`
	Details   string    `json:"details,omitempty"`
	Timestamp int64     `json:"timestamp"`
	Raw       string    `json:"raw,omitempty"`
}

type EventHandler struct {
	mu        sync.RWMutex
	callbacks map[EventType][]func(Event)
	buffer    []Event
}

func NewEventHandler() *EventHandler {
	return &EventHandler{
		callbacks: make(map[EventType][]func(Event)),
		buffer:    make([]Event, 0, 1000),
	}
}

func (h *EventHandler) On(eventType EventType, fn func(Event)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.callbacks[eventType] = append(h.callbacks[eventType], fn)
}

func (h *EventHandler) Handle(event Event) {
	h.mu.RLock()
	fns := h.callbacks[event.Type]
	h.mu.RUnlock()

	for _, fn := range fns {
		fn(event)
	}

	h.mu.Lock()
	h.buffer = append(h.buffer, event)
	if len(h.buffer) > 10000 {
		h.buffer = h.buffer[len(h.buffer)-5000:]
	}
	h.mu.Unlock()
}

func (h *EventHandler) Normalize(rawJSON []byte) (*Event, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, err
	}

	event := &Event{
		Timestamp: 0,
	}

	if t, ok := raw["type"].(string); ok {
		event.Type = EventType(t)
	}
	if s, ok := raw["source"].(string); ok {
		event.Source = s
	}
	if m, ok := raw["message_id"].(string); ok {
		event.MessageID = m
	}
	if f, ok := raw["from"].(string); ok {
		event.FromAddr = f
	}
	if t, ok := raw["to"].(string); ok {
		event.ToAddr = t
	}
	if d, ok := raw["details"].(string); ok {
		event.Details = d
	}

	return event, nil
}

func (h *EventHandler) GetRecent(count int) []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if count <= 0 || count > len(h.buffer) {
		count = len(h.buffer)
	}

	result := make([]Event, count)
	copy(result, h.buffer[len(h.buffer)-count:])
	return result
}

func (h *EventHandler) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buffer = h.buffer[:0]
}
