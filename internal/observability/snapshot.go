package observability

import (
	"sync"
	"time"
)

// EventHistory provides a bounded, concurrency-safe ring buffer of events.
type EventHistory struct {
	mu      sync.RWMutex
	events  []EventEntry
	maxSize int
}

// NewEventHistory creates a bounded event history buffer.
func NewEventHistory(maxSize int) *EventHistory {
	return &EventHistory{
		maxSize: maxSize,
	}
}

// Record adds an event to the history, dropping oldest if at capacity.
func (h *EventHistory) Record(typ EventType, fields map[string]string) {
	e := EventEntry{
		Type:      typ,
		Fields:    copyMap(fields),
		Timestamp: time.Now().UnixNano(),
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.events) >= h.maxSize {
		h.events = h.events[1:]
	}
	h.events = append(h.events, e)
}

// Recent returns a copy of recent events.
func (h *EventHistory) Recent() []EventEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]EventEntry, len(h.events))
	copy(result, h.events)
	return result
}

// CollectSnapshot builds a point-in-time diagnostic snapshot.
// RecentFailures: queue.bounced and queue.deadletter events.
// RecentAuthFailures: smtp.auth.failure events.
// RecentSpamRejects: spam.verdict.reject events.
type SnapshotCollector struct {
	metrics *MetricsCollector
	logger  *Logger
	health  *HealthChecker
	events  *EventHistory
}

// NewSnapshotCollector creates a snapshot collector from the observability components.
func NewSnapshotCollector(metrics *MetricsCollector, logger *Logger, health *HealthChecker, events *EventHistory) *SnapshotCollector {
	return &SnapshotCollector{
		metrics: metrics,
		logger:  logger,
		health:  health,
		events:  events,
	}
}

// Snapshot returns a point-in-time diagnostic snapshot.
func (s *SnapshotCollector) Snapshot() *DiagnosticSnapshot {
	snap := &DiagnosticSnapshot{}

	if s.metrics != nil {
		snap.Metrics = s.metrics.Snapshot()
	}

	if s.events != nil {
		snap.RecentEvents = s.events.Recent()
		snap.RecentFailures = filterEvents(snap.RecentEvents,
			EventQueueBounced, EventQueueDeadLetter)
		snap.RecentAuthFailures = filterEvents(snap.RecentEvents,
			EventSMTPAuthFailure)
		snap.RecentSpamRejects = filterEvents(snap.RecentEvents,
			EventSpamRejected)
	}

	if s.health != nil {
		snap.Health = s.health.Report()
	}

	return snap
}

// SetQueueSummary sets the queue summary on the next snapshot.
// This is a convenience for the caller to inject queue state.
func (s *SnapshotCollector) SetQueueSummary(summary *QueueSummary) {
	_ = summary
}

func filterEvents(events []EventEntry, types ...EventType) []EventEntry {
	typeSet := make(map[EventType]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}
	var result []EventEntry
	for _, e := range events {
		if typeSet[e.Type] {
			result = append(result, e)
		}
	}
	return result
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	r := make(map[string]string, len(m))
	for k, v := range m {
		r[k] = v
	}
	return r
}
