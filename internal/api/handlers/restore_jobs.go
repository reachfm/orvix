package handlers

import (
	"fmt"
	"os"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/restorecoord"
	"go.uber.org/zap"
)

// Restore is an ASYNCHRONOUS, job-based operation. The Orvix API process
// (User=orvix, NoNewPrivileges) cannot safely restart itself and then observe
// the restart/health/rollback, so it does NOT restore in-process. Instead:
//
//	POST .../:id/restore        -> 202 Accepted + {job_id}. Submits a job file.
//	GET  .../restore-jobs/:jid  -> the durable job status (survives the restart).
//
// A root systemd path unit (orvix-restore.path) notices the queued job and
// starts orvix-restore.service (oneshot, root, a SEPARATE unit) which runs
// `orvix restore-run`: activate -> restart orvix -> verify restarted health ->
// roll back on failure -> write the durable result this endpoint returns. Only
// that external coordinator may mark a job succeeded.

// restoreCoordinatorRoot is the FIXED restore job/result directory, matching
// cmd/orvix restoreJobsDir and the systemd orvix-restore.path watched path
// (which cannot read config). Overridable via ORVIX_RESTORE_JOBS_DIR only for
// test/staging harnesses.
func (h *Handler) restoreCoordinatorRoot() string {
	if v := strings.TrimSpace(os.Getenv("ORVIX_RESTORE_JOBS_DIR")); v != "" {
		return v
	}
	return "/var/lib/orvix/restore-jobs"
}

func (h *Handler) restoreCoordinator() *restorecoord.Coordinator {
	return restorecoord.New(h.restoreCoordinatorRoot())
}

// restoreCoordinatorInstalled reports whether the external restore coordinator
// (systemd path + service units) is installed, so the API fails closed rather
// than accepting a restore that can never actually run. An explicit env opt-in
// is provided for controlled test/staging harnesses; it is never set in a
// normal production install (which ships the real unit files).
func restoreCoordinatorInstalled() bool {
	if os.Getenv("ORVIX_RESTORE_COORDINATOR_ASSUME_READY") == "1" {
		return true
	}
	for _, p := range []string{
		"/etc/systemd/system/orvix-restore.path",
		"/lib/systemd/system/orvix-restore.path",
		"/usr/lib/systemd/system/orvix-restore.path",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// PostRestoreBackup submits a restore job and returns 202 Accepted. It performs
// NO activation and NO restart, and never claims success.
func (h *Handler) PostRestoreBackup(c fiber.Ctx) error {
	id := c.Params("id")
	if invalidBackupID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid backup id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Confirm != "restore-orvix-backup" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "restore requires typed confirmation: restore-orvix-backup"})
	}

	// Fail closed if the external coordinator is not installed: without it the
	// restart/health/rollback lifecycle cannot run, so we must not accept the
	// job and pretend it will complete.
	if !restoreCoordinatorInstalled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "restore coordinator is not installed (orvix-restore.path/.service); restore is unavailable",
		})
	}

	// Reject an obviously bad backup up front (existence/validation) so we
	// don't queue a doomed job.
	svc, err := h.backupService()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup service unavailable"})
	}
	if _, err := svc.GetBackup(c.Context(), id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}

	actor := "user:unknown"
	if v, ok := c.Locals("user_id").(uint); ok && v > 0 {
		actor = fmt.Sprintf("user:%d", v)
	}

	coord := h.restoreCoordinator()
	job, err := coord.Submit(id, actor)
	if err == restorecoord.ErrActiveJob {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "a restore is already in progress"})
	}
	if err != nil {
		h.logger.Error("restore job submit failed", zap.String("backup_id", id), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to submit restore job"})
	}

	h.writeAuditLog(c, "backup.restore.submitted", fmt.Sprintf("backup_id:%s|job_id:%s", id, job.ID))
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"job_id":   job.ID,
		"status":   string(restorecoord.StatusPending),
		"poll_url": "/api/v1/admin/backups/restore-jobs/" + job.ID,
		"message":  "Restore accepted. Orvix will restart and this connection may drop; poll the job status. Success is reported only after the restarted service passes a health check. On failure the pre-restore backup is restored automatically.",
	})
}

// GetRestoreJobStatus returns the durable status of a restore job. It reads the
// result file fresh on every call, so it works after the Orvix restart that the
// coordinator performs (the original request's connection may have dropped).
func (h *Handler) GetRestoreJobStatus(c fiber.Ctx) error {
	jobID := c.Params("job_id")
	if !restorecoord.ValidJobID(jobID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid restore job id"})
	}
	res, err := h.restoreCoordinator().GetResult(jobID)
	if err == restorecoord.ErrNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "restore job not found"})
	}
	if err != nil {
		h.logger.Error("restore job status failed", zap.String("job_id", jobID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read restore job status"})
	}
	return c.JSON(res)
}
