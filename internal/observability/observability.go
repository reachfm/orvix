package observability

// Observability bundles all operational visibility components.
type Observability struct {
	Logger         *Logger
	Metrics        *MetricsCollector
	Health         *HealthChecker
	EventHistory   *EventHistory
	Snapshot       *SnapshotCollector
}

// NewObservability creates a fully wired observability instance.
func NewObservability(maxLogHistory, maxEventHistory int) *Observability {
	logger := NewLogger(maxLogHistory)
	metrics := NewMetricsCollector()
	health := NewHealthChecker()
	events := NewEventHistory(maxEventHistory)
	snapshot := NewSnapshotCollector(metrics, logger, health, events)

	return &Observability{
		Logger:       logger,
		Metrics:      metrics,
		Health:       health,
		EventHistory: events,
		Snapshot:     snapshot,
	}
}
