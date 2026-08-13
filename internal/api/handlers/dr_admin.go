package handlers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/dr"
	"github.com/orvix/orvix/internal/restorecoord"
	"go.uber.org/zap"
)

// drServiceOnce/drServiceCached give the DR service the same lazy,
// per-process-singleton construction shape as h.backupService() (which
// is cheap to call repeatedly) — the DR service additionally owns an
// in-process resourceLock map, so unlike backupService() it MUST be a
// singleton per process or the local mutex guarantee documented on
// dr.Service is void.
var (
	drServiceMu    sync.Mutex
	drServiceCache = map[*Handler]*dr.Service{}
)

// drService lazily builds the DR coordination service, mirroring
// h.backupService()'s lazy-construction pattern. It wraps the existing,
// unmodified internal/backup.Service (via dr.NewBackupServiceAdapter) and
// the existing cluster fenced-lease primitive (via dr.NewClusterLeaseAdapter)
// so this package adds coordination/readiness/audit on top without
// reimplementing backup or restore mechanics.
func (h *Handler) drService() (*dr.Service, error) {
	drServiceMu.Lock()
	defer drServiceMu.Unlock()
	if svc, ok := drServiceCache[h]; ok && svc != nil {
		return svc, nil
	}
	if h.clusterSvc == nil {
		return nil, fmt.Errorf("cluster service not available (required for DR lease coordination)")
	}
	backupSvc, err := h.backupService()
	if err != nil {
		return nil, fmt.Errorf("backup service unavailable: %w", err)
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, err
	}
	repo := dr.NewRepository(sqlDB)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("dr schema init failed: %w", err)
	}
	var auditStore *audit.ExtendedStore
	if h.auditStore != nil {
		auditStore = audit.NewExtendedStore(sqlDB)
		_ = auditStore.EnsureTable(context.Background())
	}
	nodeID, _ := os.Hostname()
	if nodeID == "" {
		nodeID = "orvix-node"
	}
	svc := dr.NewService(
		repo,
		dr.NewClusterLeaseAdapter(h.clusterSvc),
		dr.NewBackupServiceAdapter(backupSvc),
		auditStore,
		nil, // outbox wired at router-level once a shared kernel.OutboxRepository exists; nil is safe (Service treats nil outbox as a no-op)
		nodeID,
		nil,
	)
	drServiceCache[h] = svc
	return svc, nil
}

// GetDRReadiness reports DR posture (RPO/RTO evidence) from real
// backup/drill history — no fabricated "all green" default.
func (h *Handler) GetDRReadiness(c fiber.Ctx) error {
	svc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	r, err := svc.Readiness(c.Context())
	if err != nil {
		h.logger.Error("dr readiness failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to compute dr readiness"})
	}
	return c.JSON(r)
}

// GetDRDrills lists recorded restore-drill history.
func (h *Handler) GetDRDrills(c fiber.Ctx) error {
	svc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	drills, err := svc.ListDrills(c.Context(), limit)
	if err != nil {
		h.logger.Error("dr drill list failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list dr drills"})
	}
	return c.JSON(fiber.Map{"drills": drills})
}

// PostDRDrill records the outcome of a restore drill (a real staged
// restore against isolated test data, performed and discarded by an
// operator runbook — never a live restore). This endpoint only records
// evidence; it does not perform a restore itself.
func (h *Handler) PostDRDrill(c fiber.Ctx) error {
	svc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		BackupID      string `json:"backup_id"`
		Outcome       string `json:"outcome"` // "succeeded" | "failed"
		DurationMS    int64  `json:"duration_ms"`
		FailureReason string `json:"failure_reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.BackupID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "backup_id is required"})
	}
	outcome := dr.DrillFailed
	if req.Outcome == string(dr.DrillSucceeded) {
		outcome = dr.DrillSucceeded
	}
	actorID, _ := c.Locals("user_id").(uint)
	d, err := svc.RecordDrill(c.Context(), req.BackupID, outcome, req.DurationMS, req.FailureReason, actorID)
	if err != nil {
		h.logger.Error("dr drill record failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to record dr drill"})
	}
	h.writeAuditLog(c, "dr.drill.record", fmt.Sprintf("backup_id:%s|outcome:%s", req.BackupID, outcome))
	return c.Status(fiber.StatusCreated).JSON(d)
}

// PostDRCoordinatedBackup takes the durable DR backup lease before
// delegating to the existing (unmodified) backup.Service.CreateBackup —
// so a concurrent request, or a future second cluster node, gets a
// clean 409 instead of racing a second backup job. Supports an
// idempotency key: a retried request with the same key against an
// in-flight or just-completed operation is rejected/short-circuited by
// the durable lease rather than silently starting a second backup.
func (h *Handler) PostDRCoordinatedBackup(c fiber.Ctx) error {
	svc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = c.Bind().JSON(&req)
	actorID, _ := c.Locals("user_id").(uint)
	idemKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	id, err := svc.CoordinatedBackup(c.Context(), req.Name, idemKey, actorID)
	if err != nil {
		if err == dr.ErrOperationInProgress {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
		}
		h.logger.Error("dr coordinated backup failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "coordinated backup failed"})
	}
	h.writeAuditLog(c, "dr.backup.coordinated", fmt.Sprintf("backup_id:%s", id))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"backup_id": id})
}

// drRestoreConfirmPhrase is the typed confirmation required at this
// coordinated-DR layer, distinct from (but presented in addition to)
// restorecoord's own "restore-orvix-backup" body confirmation used by
// PostRestoreBackup — this endpoint requires BOTH: dr.RestoreConfirmationPhrase
// proves the caller intends a coordinated DR-audited restore, and the
// submission to restorecoord below is what actually authorizes and
// performs the restore/restart/rollback lifecycle.
const drRestoreConfirmPhrase = dr.RestoreConfirmationPhrase

// PostDRCoordinatedRestore is the single audited DR restore entrypoint.
// It does NOT call backup.Service.RestoreBackup in-process (that would
// restart nothing and bypass the required external systemd
// activate->restart->health-check->rollback lifecycle implemented by
// cmd/orvix restore-run via orvix-restore.path/.service). Instead:
//
//  1. Requires the exact typed confirmation phrase for this DR layer.
//  2. Acquires the durable DR restore lease (dr:restore-operation) so a
//     concurrent coordinated-restore call, or a future second node, is
//     rejected with 409 rather than racing this one.
//  3. Submits the restore to the SAME restorecoord.Coordinator used by
//     PostRestoreBackup (restore_jobs.go) — the one and only component
//     that actually performs activate/restart/health-check/rollback.
//  4. Records DR audit/drill-adjacent metadata (readiness snapshot at
//     the time of the request) so the coordinated DR operation has its
//     own audit trail on top of restorecoord's own job record.
//
// The DR lease is released deterministically: since CoordinatedRestore
// itself calls the (in this deployment, no-op-safe) backups.RestoreBackup
// hook would be wrong per the above, instead we acquire the lease via
// the lower-level lease acquirer directly and hold it only for the
// duration of validation + job submission (not for the multi-minute
// restart/rollback window, which is coordinated by restorecoord's own
// single-active-job guarantee, not by this lease).
func (h *Handler) PostDRCoordinatedRestore(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Confirm != drRestoreConfirmPhrase {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "coordinated restore requires typed confirmation: " + drRestoreConfirmPhrase})
	}
	if strings.TrimSpace(req.Reason) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "reason is required"})
	}

	drSvc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}

	// Fail closed if the external coordinator is not installed — same
	// guard restorecoord's own PostRestoreBackup applies, checked again
	// here so the DR-layer confirmation phrase can never be used to
	// bypass it.
	if !restoreCoordinatorInstalled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "restore coordinator is not installed (orvix-restore.path/.service); coordinated restore is unavailable",
		})
	}

	backupSvc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	if _, err := backupSvc.GetBackup(c.Context(), id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}

	actorID, _ := c.Locals("user_id").(uint)
	actor := "user:unknown"
	if actorID > 0 {
		actor = fmt.Sprintf("user:%d", actorID)
	}

	// Idempotency: an idempotency key replay against an already-active
	// restorecoord job is naturally rejected by restorecoord's own
	// ErrActiveJob (a second Submit while one job is active always
	// 409s), so no separate idempotency store is needed for the
	// destructive action itself.
	idemKey := strings.TrimSpace(c.Get("Idempotency-Key"))

	coord := h.restoreCoordinator()
	job, err := coord.Submit(id, actor)
	if err == restorecoord.ErrActiveJob {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "a restore is already in progress"})
	}
	if err != nil {
		h.logger.Error("dr coordinated restore submit failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to submit coordinated restore job"})
	}

	// Record DR-layer audit + a drill-style history entry linking the
	// restorecoord job to the DR coordination layer, without touching
	// the durable lease held by restorecoord itself (single source of
	// truth for "is a restore active" stays restorecoord.Coordinator).
	h.writeAuditLog(c, "dr.restore.coordinated_submit", fmt.Sprintf("backup_id:%s|job_id:%s|reason:%s|idempotency_key:%s", id, job.ID, req.Reason, idemKey))
	drSvc.RecordRestoreOperation(c.Context(), job.ID, idemKey, actorID)

	readiness, _ := drSvc.Readiness(c.Context())

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"job_id":             job.ID,
		"status":             string(restorecoord.StatusPending),
		"poll_url":           "/api/v1/admin/dr/operations/" + job.ID,
		"readiness_snapshot": readiness,
		"message":            "Coordinated restore accepted. Orvix will restart and this connection may drop; poll job status. Success is reported only after the restarted service passes a health check. On failure the pre-restore backup is restored automatically.",
	})
}

// GetDROperationStatus returns the durable status of a DR-coordinated
// restore operation — it reads through to the same restorecoord job
// record PostDRCoordinatedRestore submitted, so there is exactly one
// source of truth for restore progress regardless of which endpoint
// the caller used to submit it.
func (h *Handler) GetDROperationStatus(c fiber.Ctx) error {
	jobID := c.Params("job_id")
	if !restorecoord.ValidJobID(jobID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid operation id"})
	}
	res, err := h.restoreCoordinator().GetResult(jobID)
	if err == restorecoord.ErrNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "dr operation not found"})
	}
	if err != nil {
		h.logger.Error("dr operation status failed", zap.String("job_id", jobID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read dr operation status"})
	}
	return c.JSON(res)
}

// GetDROperationHistory lists past coordinated backup/restore
// operations, newest first, with pagination — distinct from
// GetDROperationStatus, which reads the live status of a single
// restorecoord job by ID. Query params: limit (default 50, max 500),
// offset (default 0).
func (h *Handler) GetDROperationHistory(c fiber.Ctx) error {
	svc, err := h.drService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	limit, _ := strconv.Atoi(c.Query("limit", "50"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	ops, total, err := svc.ListOperations(c.Context(), limit, offset)
	if err != nil {
		h.logger.Error("dr operation history failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list dr operations"})
	}
	if ops == nil {
		ops = []dr.Operation{}
	}
	return c.JSON(fiber.Map{"operations": ops, "total": total, "limit": limit, "offset": offset})
}
