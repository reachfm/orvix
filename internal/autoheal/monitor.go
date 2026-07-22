package autoheal

import (
	"log"
	"sync"
	"time"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type CheckResult struct {
	Name     string   `json:"name"`
	Healthy  bool     `json:"healthy"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type HealthCheck struct {
	Name     string
	Severity Severity
	Interval time.Duration
	Check    func() CheckResult
	Fix      func() error
}

type Monitor struct {
	mu      sync.RWMutex
	checks  []HealthCheck
	history *History
	stopCh  chan struct{}
	running bool
}

func NewMonitor() *Monitor {
	return &Monitor{
		history: NewHistory(),
		stopCh:  make(chan struct{}),
	}
}

func (m *Monitor) AddCheck(hc HealthCheck) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checks = append(m.checks, hc)
}

func (m *Monitor) Start(interval time.Duration) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				results := m.RunAll()
				for _, r := range results {
					log.Printf("[autoheal] %s: healthy=%v severity=%s message=%s", r.Name, r.Healthy, r.Severity, r.Message)
				}
			case <-m.stopCh:
				return
			}
		}
	}()
}

func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		close(m.stopCh)
		m.running = false
	}
}

func (m *Monitor) RunAll() []CheckResult {
	m.mu.RLock()
	checks := make([]HealthCheck, len(m.checks))
	copy(checks, m.checks)
	m.mu.RUnlock()

	var results []CheckResult
	for _, c := range checks {
		result := c.Check()
		result.Name = c.Name
		result.Severity = c.Severity

		if !result.Healthy && c.Fix != nil {
			beforeState := result.Message

			if err := c.Fix(); err != nil {
				result.Error = err.Error()
			} else {
				result.Healthy = true
				result.Message = "fixed"
			}

			m.history.Log(HealEntry{
				CheckName:   c.Name,
				Severity:    string(c.Severity),
				Issue:       beforeState,
				BeforeState: beforeState,
				AfterState:  result.Message,
				AutoFixed:   result.Healthy,
				Success:     result.Healthy,
				PerformedAt: time.Now(),
			})
		}

		results = append(results, result)
	}

	return results
}

func (m *Monitor) History() *History {
	return m.history
}
