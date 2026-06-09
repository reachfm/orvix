package delivery

import (
	"sync"
)

// ReliabilityMetrics tracks delivery reliability counters.
type ReliabilityMetrics struct {
	mu sync.Mutex

	// All counters are protected by mu.
	TotalDeliveries  int64
	TotalDeferrals   int64
	TotalBounces     int64
	TotalDeadLetters int64
	TotalTimeouts    int64
	TotalConnFails   int64

	// Complex counters (mutex-protected).
	LeaseRecoveries    int64
	DuplicateDetects   int64
	GracefulShutdowns  int64
	TotalRetryCount    int64
	MaxRetryObserved   int64
	ActiveWorkers      int64
	TotalDeliveryDurMs int64
	MinDeliveryDurMs   int64
	MaxDeliveryDurMs   int64
}

// RecordDelivery increments counters for a successful delivery.
func (m *ReliabilityMetrics) RecordDelivery(durationMs int64) {
	m.mu.Lock()
	m.TotalDeliveries++
	m.TotalDeliveryDurMs += durationMs
	if m.MinDeliveryDurMs == 0 || durationMs < m.MinDeliveryDurMs {
		m.MinDeliveryDurMs = durationMs
	}
	if durationMs > m.MaxDeliveryDurMs {
		m.MaxDeliveryDurMs = durationMs
	}
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordDeferral() {
	m.mu.Lock()
	m.TotalDeferrals++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordBounce() {
	m.mu.Lock()
	m.TotalBounces++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordDeadLetter() {
	m.mu.Lock()
	m.TotalDeadLetters++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordTimeout() {
	m.mu.Lock()
	m.TotalTimeouts++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordConnFail() {
	m.mu.Lock()
	m.TotalConnFails++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordLeaseRecovery() {
	m.mu.Lock()
	m.LeaseRecoveries++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordDuplicateDetect() {
	m.mu.Lock()
	m.DuplicateDetects++
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) RecordRetry(attempt int) {
	m.mu.Lock()
	m.TotalRetryCount++
	if int64(attempt) > m.MaxRetryObserved {
		m.MaxRetryObserved = int64(attempt)
	}
	m.mu.Unlock()
}

func (m *ReliabilityMetrics) SetActiveWorkers(count int64) {
	m.mu.Lock()
	m.ActiveWorkers = count
	m.mu.Unlock()
}

// Snapshot returns a consistent snapshot of all metrics.
func (m *ReliabilityMetrics) Snapshot() ReliabilityMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ReliabilityMetrics{
		TotalDeliveries:    m.TotalDeliveries,
		TotalDeferrals:     m.TotalDeferrals,
		TotalBounces:       m.TotalBounces,
		TotalDeadLetters:   m.TotalDeadLetters,
		TotalTimeouts:      m.TotalTimeouts,
		TotalConnFails:     m.TotalConnFails,
		LeaseRecoveries:    m.LeaseRecoveries,
		DuplicateDetects:   m.DuplicateDetects,
		GracefulShutdowns:  m.GracefulShutdowns,
		TotalRetryCount:    m.TotalRetryCount,
		MaxRetryObserved:   m.MaxRetryObserved,
		ActiveWorkers:      m.ActiveWorkers,
		TotalDeliveryDurMs: m.TotalDeliveryDurMs,
		MinDeliveryDurMs:   m.MinDeliveryDurMs,
		MaxDeliveryDurMs:   m.MaxDeliveryDurMs,
	}
}
