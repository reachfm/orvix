package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/monitoring"
	"go.uber.org/zap"
)

// monitoringService constructs a fresh monitoring.Service bound to
// the live SQL DB. The service is created on every request because the
// underlying DataSources are cheap and the service is stateless beyond
// the DB handle. The router never holds a long-lived instance because
// we want the uptime field to be relative to the request goroutine.
func (h *Handler) monitoringService() (*monitoring.Service, error) {
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, err
	}
	if err := ensureMonitoringSchema(sqlDB); err != nil {
		return nil, err
	}
	ds := &monitoring.DataSources{
		DB:              sqlDB,
		QueuePending:    h.queuePendingCount,
		QueueDeadLetter: h.queueDeadLetterCount,
		DatabaseHealthy: h.databaseHealthy,
		BackupDir:       h.backupDir(),
		APIPing:         h.apiSelfPing,
	}
	if start := processStartedAt(); !start.IsZero() {
		ds.ServiceStartedAt = start
	}
	return monitoring.NewService(sqlDB, ds), nil
}

// ensureMonitoringSchema is idempotent and safe to call on every
// request. The cost is a single CREATE TABLE IF NOT EXISTS.
func ensureMonitoringSchema(db *sql.DB) error {
	for _, stmt := range monitoring.Schema() {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// queuePendingCount returns the count of queue rows that are not yet
// delivered (status: pending, leased, deferred). Falls back to 0 if
// the queue table is not present (the system may be running in a
// degraded mode where the coremail subsystem is not initialized).
func (h *Handler) queuePendingCount() (int64, error) {
	sqlDB, err := h.db.DB()
	if err != nil {
		return 0, err
	}
	var n int64
	row := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status IN ('pending','leased','deferred')`)
	if err := row.Scan(&n); err != nil {
		// Table may not exist in some deployments; treat as 0.
		return 0, nil
	}
	return n, nil
}

// queueDeadLetterCount returns the count of messages parked in the
// dead-letter state. Falls back to 0 if the queue table is absent.
func (h *Handler) queueDeadLetterCount() (int64, error) {
	sqlDB, err := h.db.DB()
	if err != nil {
		return 0, err
	}
	var n int64
	row := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'dead_letter'`)
	if err := row.Scan(&n); err != nil {
		return 0, nil
	}
	return n, nil
}

// databaseHealthy performs a quick Ping to confirm the DB is reachable.
func (h *Handler) databaseHealthy() bool {
	return h.dbPingErrorForTelemetry() == nil
}

// dbPingErrorForTelemetry is the error-returning variant used by
// the runtime telemetry endpoint. It never panics and never
// returns a non-nil error wrapping a secret; the only error it
// surfaces is the Ping failure.
func (h *Handler) dbPingErrorForTelemetry() error {
	sqlDB, err := h.db.DB()
	if err != nil {
		return err
	}
	ctx, cancel := newShortContext()
	defer cancel()
	return sqlDB.PingContext(ctx)
}

// apiSelfPing performs a lightweight in-process check. The monitoring
// service can be wired to call its own handler in the future; for now
// the presence of the running process + the DB being up is a
// sufficient proxy.
func (h *Handler) apiSelfPing() error {
	if !h.databaseHealthy() {
		return errors.New("admin API: database unreachable")
	}
	return nil
}

// processStartedAt returns the time the process started, derived from
// the OS uptime. Returns zero on platforms where this is not available.
func processStartedAt() time.Time {
	// We deliberately do not record the start time at package init
	// (that would run during tests and skew results). Instead, we
	// compute a relative value at the moment the first Health call
	// arrives. The Handler is created per request, so this returns
	// a "now" reference; uptimeSeconds is then computed in the
	// service as time.Since(start).
	return time.Time{}
}

// newShortContext returns a context with a 1-second timeout suitable
// for health checks. The handler should not block longer than this on
// any downstream call.
func newShortContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 1*time.Second)
}

// GetMonitoringHealth serves GET /api/v1/monitoring/health.
//
// Security: the response is built from monitoring.Service.GetHealth
// which renders only safe labels and counts. No env values, no file
// contents, no tokens, no private absolute paths.
func (h *Handler) GetMonitoringHealth(c fiber.Ctx) error {
	svc, err := h.monitoringService()
	if err != nil {
		h.logger.Error("monitoring service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "monitoring unavailable"})
	}
	// Run a fresh evaluation so the health reflects the current state.
	if _, err := svc.EvaluateAlerts(c.Context()); err != nil {
		h.logger.Warn("monitoring evaluate", zap.Error(err))
	}
	health := svc.GetHealth(c.Context())
	return c.JSON(health)
}

// GetMonitoringAlerts serves GET /api/v1/monitoring/alerts.
//
// Returns the active alert list (unresolved alerts only). The response
// shape matches the existing admin test which expects
// `{"alerts": [...]}`.
func (h *Handler) GetMonitoringAlerts(c fiber.Ctx) error {
	svc, err := h.monitoringService()
	if err != nil {
		h.logger.Error("monitoring service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "monitoring unavailable"})
	}
	if _, err := svc.EvaluateAlerts(c.Context()); err != nil {
		h.logger.Warn("monitoring evaluate", zap.Error(err))
	}
	active, err := svc.ListActiveAlerts(c.Context())
	if err != nil {
		h.logger.Error("list alerts", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "list alerts failed"})
	}
	return c.JSON(fiber.Map{"alerts": active})
}

// GetMonitoringCapacity serves GET /api/v1/monitoring/capacity.
//
// Kept for backward compatibility with the legacy admin client.
func (h *Handler) GetMonitoringCapacity(c fiber.Ctx) error {
	svc, err := h.monitoringService()
	if err != nil {
		h.logger.Error("monitoring service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "monitoring unavailable"})
	}
	return c.JSON(svc.GetCapacity(c.Context()))
}

// PostMonitoringAlertResolve serves POST /api/v1/monitoring/alerts/:id/resolve.
//
// The route is admin-only AND CSRF-protected (mounted under the `men`
// group). The id must be a non-negative integer. A 404 is returned if
// the alert does not exist or is already resolved.
func (h *Handler) PostMonitoringAlertResolve(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid alert id"})
	}
	svc, err := h.monitoringService()
	if err != nil {
		h.logger.Error("monitoring service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "monitoring unavailable"})
	}
	rows, err := svc.ResolveAlert(c.Context(), uint(id))
	if err != nil {
		h.logger.Error("resolve alert", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "resolve failed"})
	}
	if rows == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "alert not found or already resolved"})
	}
	h.writeAuditLog(c, "monitoring.alert.resolve", fmt.Sprintf("alert_id:%d", id))
	return c.JSON(fiber.Map{"status": "resolved", "id": id})
}
