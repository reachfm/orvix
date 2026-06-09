package observability

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Logger handles structured event logging with optional history ring buffer.
type Logger struct {
	mu       sync.RWMutex
	history  []LogEvent
	maxSize  int
	sink     func(LogEvent) // optional external sink (e.g., log.Printf)
}

// NewLogger creates a structured logger with bounded history.
func NewLogger(maxHistory int) *Logger {
	return &Logger{
		maxSize: maxHistory,
	}
}

// SetSink sets an optional external output function.
func (l *Logger) SetSink(sink func(LogEvent)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sink = sink
}

// Event records a structured log event.
func (l *Logger) Event(typ EventType, fields map[string]string) {
	e := LogEvent{
		Type:      typ,
		Fields:    fields,
		Timestamp: time.Now().UnixNano(),
	}

	l.mu.Lock()
	if l.sink != nil {
		l.sink(e)
	}
	if l.maxSize > 0 {
		if len(l.history) >= l.maxSize {
			l.history = l.history[1:]
		}
		l.history = append(l.history, e)
	}
	l.mu.Unlock()
}

// RecentEvents returns a copy of recent events.
func (l *Logger) RecentEvents() []LogEvent {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]LogEvent, len(l.history))
	copy(result, l.history)
	return result
}

// FormatEvent produces a human-readable log line from a LogEvent.
// Does NOT include passwords, keys, or raw message bodies.
func FormatEvent(e LogEvent) string {
	var b strings.Builder
	b.WriteString("evt=")
	b.WriteString(string(e.Type))

	// Fields that are always safe to log.
	safeKeys := map[string]bool{
		"domain": true, "sender": true, "recipient": true,
		"remote_ip": true, "helo": true, "mechanism": true,
		"policy": true, "reason": true, "score": true,
		"verdict": true, "selector": true, "mx_host": true,
		"duration_ms": true, "attempt": true, "queue_id": true,
		"message_id": true, "worker": true, "status": true,
		"method": true, "identity": true, "error": true,
		"hostname": true,
	}

	for k, v := range e.Fields {
		if safeKeys[k] {
			b.WriteString(" ")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(sanitizeValue(v))
		}
	}
	return b.String()
}

func sanitizeValue(v string) string {
	// Truncate long values and remove control characters.
	if len(v) > 256 {
		v = v[:256] + "..."
	}
	v = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' {
			return -1
		}
		return r
	}, v)
	return v
}

// ValidateEventSafety checks that an event contains no sensitive data.
// Returns an error if password, key, or raw body is detected.
func ValidateEventSafety(e LogEvent) error {
	if e.Fields == nil {
		return nil
	}
	for k, v := range e.Fields {
		switch k {
		case "password", "passwd", "secret", "token", "private_key", "key_pem":
			return fmt.Errorf("sensitive field %q in log event", k)
		}
		if len(v) > 512 {
			return fmt.Errorf("suspiciously large value (%d bytes) in field %q", len(v), k)
		}
	}
	return nil
}
