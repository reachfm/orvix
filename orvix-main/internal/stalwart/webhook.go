package stalwart

import (
	"encoding/json"
	"io"
	"net/http"

	"go.uber.org/zap"
)

// EventType represents a Stalwart webhook event type.
type EventType string

const (
	EventEmailReceived  EventType = "EMAIL_RECEIVED"
	EventEmailSent      EventType = "EMAIL_SENT"
	EventBounceReceived EventType = "BOUNCE_RECEIVED"
	EventAuthFailure    EventType = "AUTH_FAILURE"
)

// WebhookEvent represents an event from Stalwart.
type WebhookEvent struct {
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// EventHandler processes Stalwart webhook events.
type EventHandler struct {
	handlers map[EventType]func(WebhookEvent) error
	logger   *zap.Logger
}

// NewEventHandler creates a new webhook event handler.
func NewEventHandler(logger *zap.Logger) *EventHandler {
	return &EventHandler{
		handlers: make(map[EventType]func(WebhookEvent) error),
		logger:   logger,
	}
}

// RegisterHandler registers a handler for an event type.
func (eh *EventHandler) RegisterHandler(event EventType, handler func(WebhookEvent) error) {
	eh.handlers[event] = handler
	eh.logger.Info("webhook handler registered", zap.String("event", string(event)))
}

// ServeHTTP handles incoming webhook requests from Stalwart.
func (eh *EventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		eh.logger.Error("failed to read webhook body", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var event WebhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		eh.logger.Error("failed to decode webhook event", zap.Error(err))
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	handler, ok := eh.handlers[event.Type]
	if !ok {
		eh.logger.Warn("no handler for webhook event", zap.String("type", string(event.Type)))
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := handler(event); err != nil {
		eh.logger.Error("webhook handler failed",
			zap.String("type", string(event.Type)),
			zap.Error(err),
		)
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
