package delivery

import (
	"context"
	"fmt"
	"time"
)

// DeliveryEventType classifies the stage of a delivery attempt.
type DeliveryEventType string

const (
	EventQueued         DeliveryEventType = "queued"
	EventLeased         DeliveryEventType = "leased"
	EventConnecting     DeliveryEventType = "connecting"
	EventConnected      DeliveryEventType = "connected"
	EventRemoteAccepted DeliveryEventType = "remote_accepted"
	EventRemoteRejected DeliveryEventType = "remote_rejected"
	EventDeferred       DeliveryEventType = "deferred"
	EventBounced        DeliveryEventType = "bounced"
	EventDelivered      DeliveryEventType = "delivered"
	EventDeadLetter     DeliveryEventType = "dead_letter"
	EventPolicyRejected DeliveryEventType = "policy_rejected"
	EventLoopDetected   DeliveryEventType = "loop_detected"
)

// DeliveryEvent records a single delivery lifecycle event.
type DeliveryEvent struct {
	QueueEntryID uint              `json:"queue_entry_id"`
	MessageID    string            `json:"message_id"`
	FromAddress  string            `json:"from_address"`
	ToAddress    string            `json:"to_address"`
	EventType    DeliveryEventType `json:"event_type"`
	Timestamp    time.Time         `json:"timestamp"`
	RemoteHost   string            `json:"remote_host,omitempty"`
	RemoteIP     string            `json:"remote_ip,omitempty"`
	StatusCode   int               `json:"status_code,omitempty"`
	StatusMsg    string            `json:"status_msg,omitempty"`
	EnhancedCode string            `json:"enhanced_code,omitempty"`
	Attempt      int               `json:"attempt"`
	WorkerID     string            `json:"worker_id,omitempty"`
	Direction    string            `json:"direction"`
	DurationMs   int64             `json:"duration_ms,omitempty"`
}

// AuditLog is an interface for recording delivery events.
type AuditLog interface {
	RecordEvent(ctx context.Context, event DeliveryEvent) error
}

// AuditLogger records delivery events.
type AuditLogger struct {
	events []DeliveryEvent
}

// NewAuditLogger creates an audit logger.
func NewAuditLogger() *AuditLogger {
	return &AuditLogger{
		events: make([]DeliveryEvent, 0, 100),
	}
}

// RecordEvent stores a delivery event.
func (a *AuditLogger) RecordEvent(ctx context.Context, event DeliveryEvent) error {
	a.events = append(a.events, event)
	return nil
}

// Events returns all recorded events.
func (a *AuditLogger) Events() []DeliveryEvent {
	return a.events
}

// LastEvent returns the most recent event for a queue entry.
func (a *AuditLogger) LastEvent(entryID uint) *DeliveryEvent {
	for i := len(a.events) - 1; i >= 0; i-- {
		if a.events[i].QueueEntryID == entryID {
			return &a.events[i]
		}
	}
	return nil
}

// BuildEvent creates a delivery event from delivery parameters.
func BuildEvent(entryID uint, msgID, from, to, workerID, direction string, eventType DeliveryEventType) DeliveryEvent {
	return DeliveryEvent{
		QueueEntryID: entryID,
		MessageID:    msgID,
		FromAddress:  from,
		ToAddress:    to,
		EventType:    eventType,
		Timestamp:    time.Now().UTC(),
		WorkerID:     workerID,
		Direction:    direction,
	}
}

// FormatEnhancedCode constructs an SMTP enhanced status code string.
// Format: X.Y.Z where X=class (2=success,4=temp,5=perm), Y=subject, Z=detail
func FormatEnhancedCode(class, subject, detail int) string {
	return fmt.Sprintf("%d.%d.%d", class, subject, detail)
}

// ParseEnhancedCode attempts to extract an enhanced status code from an SMTP response.
// Returns empty string if not found.
func ParseEnhancedCode(msg string) string {
	if len(msg) < 7 {
		return ""
	}
	// Look for pattern "X.Y.Z " at the start of the message.
	for i := 0; i <= len(msg)-5; i++ {
		if msg[i] >= '2' && msg[i] <= '5' && i+4 < len(msg) && msg[i+1] == '.' && msg[i+3] == '.' {
			if msg[i+2] >= '0' && msg[i+2] <= '9' && msg[i+4] >= '0' && msg[i+4] <= '9' {
				// Found a pattern like "5.1.1"
				end := i + 5
				for end < len(msg) && msg[end] >= '0' && msg[end] <= '9' {
					end++
				}
				return msg[i:end]
			}
		}
	}
	return ""
}
