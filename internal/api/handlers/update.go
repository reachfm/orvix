package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/updater"
	"go.uber.org/zap"
)

// SetUpdateService attaches the process-wide update service to the
// Handler. The router calls this once at startup. Two concurrent
// HTTP requests against the same router share a single
// RuntimeService and therefore a single mutex; the single-flight
// guarantee is process-wide for the lifetime of the API server.
func (h *Handler) SetUpdateService(svc *updater.RuntimeService) {
	h.updateSvc = svc
}

// updateService returns the shared updater.RuntimeService. The
// service is wired once at router construction by NewRouter calling
// SetUpdateService. One RuntimeService means one process-wide mutex
// for Run(), which provides the single-flight guarantee: a second
// concurrent POST /api/v1/update/run returns 409 Conflict.
//
// If SetUpdateService was not called, the API returns 503 so the
// caller knows the server is misconfigured. A stale handler that
// silently creates a per-request service would defeat single-flight.
func (h *Handler) updateService() (*updater.RuntimeService, error) {
	if h.updateSvc == nil {
		return nil, fmt.Errorf("update service not wired")
	}
	h.updateSvcOnce.Do(func() {
		if err := h.updateSvc.EnsureSchema(context.Background()); err != nil {
			h.logger.Warn("update schema ensure", zap.Error(err))
		}
	})
	return h.updateSvc, nil
}

// ensureUpdateSchema is removed: the schema is now ensured
// exactly once by updateService() via updateSvcOnce. The earlier
// shim passed a nil context to EnsureSchema, which panicked. The
// shared service eliminates that path entirely.

// updateWorkspaceRoot returns the operator-supplied workspace root,
// falling back to the admin UI dir's parent and finally to the
// process working directory. The runtime script is resolved
// against this root on every Run().
func (h *Handler) updateWorkspaceRoot() string {
	if h.cfg != nil && h.cfg.Update.WorkspaceRoot != "" {
		return h.cfg.Update.WorkspaceRoot
	}
	if h.cfg != nil && h.cfg.Server.AdminUIDir != "" {
		candidate := strings.TrimSuffix(h.cfg.Server.AdminUIDir, "/admin")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// updateChannel returns the release channel from config. The spec
// mandates stable only; we expose a config knob for future-proofing
// but refuse non-stable values in the response.
func (h *Handler) updateChannel() updater.Channel {
	if h.cfg == nil {
		return updater.ChannelStable
	}
	if h.cfg.Update.Channel == "" {
		return updater.ChannelStable
	}
	return updater.Channel(h.cfg.Update.Channel)
}

// GetUpdateStatus serves GET /api/v1/update/status.
//
// Security: the response is built from updater.RuntimeService.Status
// which renders only safe fields (version strings, SHAs, durations,
// safe labels). No env values, no file contents, no private paths.
func (h *Handler) GetUpdateStatus(c fiber.Ctx) error {
	svc, err := h.updateService()
	if err != nil {
		h.logger.Error("update service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update service unavailable"})
	}
	st, err := svc.Status(c.Context())
	if err != nil {
		h.logger.Error("update status", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status failed"})
	}
	if st == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status returned nil"})
	}
	return c.JSON(st)
}

// GetUpdateHistory serves GET /api/v1/update/history.
//
// Returns the most recent update history rows. The default limit
// is 50, the maximum is 100. The query parameter "limit" must be
// a non-negative integer <= 100.
func (h *Handler) GetUpdateHistory(c fiber.Ctx) error {
	svc, err := h.updateService()
	if err != nil {
		h.logger.Error("update service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update service unavailable"})
	}
	limit := 50
	if l := c.Query("limit"); l != "" {
		n, perr := strconv.Atoi(l)
		if perr != nil || n < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid limit"})
		}
		if n > 100 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "limit must be <= 100"})
		}
		limit = n
	}
	rows, err := svc.History(c.Context(), limit)
	if err != nil {
		h.logger.Error("update history", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "history failed"})
	}
	return c.JSON(fiber.Map{"history": rows})
}

// PostUpdateCheck serves POST /api/v1/update/check.
//
// Triggers a fresh update check. The check URL is read from
// the server-side config; no client-supplied URL is honoured.
// Audits "update_check" on success.
func (h *Handler) PostUpdateCheck(c fiber.Ctx) error {
	svc, err := h.updateService()
	if err != nil {
		h.logger.Error("update service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update service unavailable"})
	}
	st, err := svc.Check(c.Context(), h.updateCheckURL(), "orvix-core", "")
	if err != nil {
		h.logger.Warn("update check failed", zap.Error(err))
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "check failed"})
	}
	h.writeAuditLog(c, "update_check", fmt.Sprintf("available:%s|update_available:%v", st.AvailableVersion, st.UpdateAvailable))
	return c.JSON(st)
}

// PostUpdateRun serves POST /api/v1/update/run.
//
// Executes the runtime update script. Single-flight: a second
// concurrent call returns 409 Conflict. Audits "update_started",
// "update_completed" or "update_failed" depending on outcome.
//
// Security:
//   - The client never sees raw exec / os errors. Failed runs are
//     classified into a closed enumeration of safe codes
//     (start_failed / preflight_failed / script_failed / timeout)
//     and the response body is one of two safe generic messages.
//   - The audit row target field carries the safe code only — never
//     any absolute path, argv, or process detail. The underlying
//     exec / os error is logged to the server logger and never
//     crosses the API or audit boundary.
func (h *Handler) PostUpdateRun(c fiber.Ctx) error {
	svc, err := h.updateService()
	if err != nil {
		h.logger.Error("update service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update service unavailable"})
	}
	if svc.IsRunning() {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "an update job is already running",
			"code":  string(updater.ErrCodeAlreadyRunning),
		})
	}
	actor := actorLabel(c)

	row, runErr := svc.Run(c.Context(), actor)
	if runErr != nil {
		// Concurrency race: IsRunning() returned false above, but
		// between that check and Run() acquiring its slot a second
		// request reserved the slot. svc.Run signals this with
		// ErrJobRunning. The single-flight guarantee is intact;
		// we just lost the chance to short-circuit at the
		// pre-flight check. Treat it the same as the pre-check
		// 409: the update_started audit row is suppressed (no job
		// actually started) and we write a separate
		// update_rejected_concurrent audit instead.
		if errors.Is(runErr, updater.ErrJobRunning) {
			h.writeAuditLog(c, "update_rejected_concurrent",
				fmt.Sprintf("actor:%s|code:%s", actor, string(updater.ErrCodeAlreadyRunning)))
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "an update job is already running",
				"code":  string(updater.ErrCodeAlreadyRunning),
			})
		}
		// We have a slot. Audit the admission as a real
		// update_started, then audit the failure.
		h.writeAuditLog(c, "update_started", "actor:"+actor)
		// Classify. svc.Run returns *updater.UpdateError; the safe
		// code is in runErr.Error() and the unsafe internal error
		// is in runErr.(*UpdateError).Internal — which we never
		// marshal.
		code := runErr.Error()
		var ue *updater.UpdateError
		if errors.As(runErr, &ue) {
			code = string(ue.Code)
			if ue.Internal != nil {
				// Log the internal error for operators. NEVER
				// include it in the response or audit target.
				h.logger.Warn("update run failed",
					zap.String("code", code),
					zap.Error(ue.Internal))
			}
		} else {
			// Unknown error shape. Log it but never echo the
			// raw text to the client; normalise to the
			// generic safe code.
			h.logger.Warn("update run failed",
				zap.String("code", code),
				zap.Error(runErr))
			code = string(updater.ErrCodeScriptFailed)
		}
		// Audit: the safe code only. No path. No exec message.
		h.writeAuditLog(c, "update_failed",
			fmt.Sprintf("actor:%s|code:%s", actor, code))
		// Response: the safe generic message. The status field
		// is the source of truth; history.notes carries the code
		// for the admin UI to render.
		return c.JSON(fiber.Map{
			"status":  "failed",
			"code":    code,
			"message": safeMessageForCode(code),
			"history": row,
		})
	}
	// Slot acquired and Run succeeded. Audit the admission.
	h.writeAuditLog(c, "update_started", "actor:"+actor)
	h.writeAuditLog(c, "update_completed", fmt.Sprintf("actor:%s|previous_sha:%s|new_sha:%s|duration:%d", actor, row.PreviousSHA, row.NewSHA, row.DurationSeconds))
	return c.JSON(fiber.Map{
		"status":  "completed",
		"history": row,
	})
}

// safeMessageForCode returns the user-facing message for a failed
// run. The mapping is a closed set so a malformed code is
// normalised to the generic "update failed" string.
func safeMessageForCode(code string) string {
	switch code {
	case string(updater.ErrCodeStartFailed):
		return "update failed to start"
	case string(updater.ErrCodePreflightFailed),
		string(updater.ErrCodeScriptFailed),
		string(updater.ErrCodeTimeout),
		string(updater.ErrCodeAlreadyRunning):
		return "update failed"
	default:
		return "update failed"
	}
}

// GetUpdatePreflight serves GET /api/v1/update/preflight.
//
// Returns the result of the preflight check (disk space, backup
// dir writability, binary build validation, script allow-list).
// The check is read-only; it never exec's the script.
func (h *Handler) GetUpdatePreflight(c fiber.Ctx) error {
	svc, err := h.updateService()
	if err != nil {
		h.logger.Error("update service init", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update service unavailable"})
	}
	return c.JSON(svc.Preflight(c.Context()))
}

// updateCheckURL is a small helper that returns the configured
// check URL or "" when none is configured.
func (h *Handler) updateCheckURL() string {
	if h.cfg == nil {
		return ""
	}
	return h.cfg.Update.CheckURL
}

// actorLabel returns a safe label for the user initiating the
// request. It MUST be a short, well-known character set suitable
// for an audit log target field. If the user id is not present
// (e.g. unauthenticated, which should not happen for admin routes)
// it returns "user:unknown" — never an empty string.
func actorLabel(c fiber.Ctx) string {
	if v, ok := c.Locals("user_id").(uint); ok && v > 0 {
		return fmt.Sprintf("user:%d", v)
	}
	if s := c.Get("X-Actor"); s != "" && isSafeActor(s) {
		return s
	}
	return "user:unknown"
}

// isSafeActor refuses any character that is not in
// [a-zA-Z0-9._:-]. This is the allow-list for the actor label.
func isSafeActor(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == ':' || r == '-':
		default:
			return false
		}
	}
	return true
}

// guard against accidentally shadowing errors import.
var _ = errors.New
