package observability

import "sync"

// HealthChecker provides system health checks.
type HealthChecker struct {
	mu       sync.RWMutex
	checks   map[string]HealthCheck
}

// NewHealthChecker creates a health checker.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		checks: make(map[string]HealthCheck),
	}
}

// SetCheck updates the health status of a subsystem.
func (h *HealthChecker) SetCheck(name string, status HealthStatus, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = HealthCheck{Status: status, Message: message}
}

// Report produces a consolidated health report.
func (h *HealthChecker) Report() *HealthReport {
	h.mu.RLock()
	defer h.mu.RUnlock()

	report := &HealthReport{
		Overall: HealthReady,
		Checks:  make(map[string]HealthCheck, len(h.checks)),
	}

	for k, v := range h.checks {
		report.Checks[k] = v
		if v.Status == HealthNotReady {
			report.Overall = HealthNotReady
		} else if v.Status == HealthDegraded && report.Overall == HealthReady {
			report.Overall = HealthDegraded
		}
	}

	return report
}

// HealthCheckNames returns standard health check names.
const (
	HealthCheckSMTPReceive     = "smtp_receive"
	HealthCheckQueue           = "queue"
	HealthCheckMailStore       = "mailstore"
	HealthCheckDNSResolver     = "dns_resolver"
	HealthCheckDKIMConfig      = "dkim_config"
	HealthCheckDatabase        = "database"
)

// Built-in check helpers.
func (h *HealthChecker) Ready(name string) {
	h.SetCheck(name, HealthReady, "")
}

func (h *HealthChecker) NotReady(name, reason string) {
	h.SetCheck(name, HealthNotReady, reason)
}

func (h *HealthChecker) Degraded(name, reason string) {
	h.SetCheck(name, HealthDegraded, reason)
}
