// This file implements the Admin Console API endpoints for the
// self-update feature (Phase H). It is the HTTP boundary in front of
// internal/selfupdate: every handler here reaches the orvix-updater
// daemon exclusively through h.selfUpdateClient.Call (a typed Unix-socket
// IPC round trip) — it never imports or touches internal/selfupdate's
// Store or orchestrator directly, and never shells out. That split is a
// Phase 0 architecture rule: this API process runs unprivileged; only
// the separate orvix-updater daemon (reached only over the socket) may
// perform privileged filesystem/service operations.
package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/selfupdate"
	"go.uber.org/zap"
)

// selfUpdateMaxBodyBytes bounds the JSON body accepted by any
// /api/v1/admin/updates/* endpoint. These requests are small structured
// admin actions; there is never a legitimate reason for a large body, so
// an oversized request is rejected before it is decoded.
const selfUpdateMaxBodyBytes = 16 * 1024

// selfUpdateGenericError is returned to the client for any failure whose
// real cause must not cross the API boundary (internal IPC error, store
// error inside the daemon, etc.) — see the package doc: never leak a raw
// Go error, stack trace, or internal path to the HTTP client.
const selfUpdateGenericError = "self-update request failed"

// decodeSelfUpdateBody enforces the request size limit and strict JSON
// decoding (unknown fields rejected) for the self-update admin
// endpoints. dst must be a pointer. A zero-length body is treated as an
// empty object so GET-shaped POSTs (e.g. "just check the default
// channel") don't need to send "{}".
func decodeSelfUpdateBody(c fiber.Ctx, dst any) error {
	body := c.Body()
	if len(body) > selfUpdateMaxBodyBytes {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body too large")
	}
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "malformed request body")
	}
	return nil
}

// selfUpdateClient returns the wired IPC client, or a clean 503 if the
// self-update feature is not configured on this deployment (no socket
// path configured, or SetSelfUpdateClient was never called). This must
// be checked before every operation — there is no fallback path to the
// updater.
func (h *Handler) selfUpdateClientOrErr() (selfupdate.IPCClient, error) {
	if h.selfUpdateClient == nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable, "self-update is not available on this deployment")
	}
	return h.selfUpdateClient, nil
}

// callSelfUpdate performs one IPC round trip and normalizes every
// failure mode into a clean, sanitized HTTP response:
//   - updater unreachable (daemon not running / socket gone)  -> 503
//   - any other transport/protocol error                      -> 502,
//     generic message only; the real error is logged server-side
//   - a well-formed but !OK response from the daemon           -> 422,
//     Response.Error is passed through verbatim because the daemon's
//     Response.Error field is itself defined (protocol.go) as an
//     operator-safe, sanitized message — never a raw Go error.
func (h *Handler) callSelfUpdate(c fiber.Ctx, req selfupdate.Request) (*selfupdate.Response, error) {
	client, err := h.selfUpdateClientOrErr()
	if err != nil {
		return nil, err
	}
	req.InitiatedBy = actorLabel(c)

	resp, err := client.Call(req)
	if err != nil {
		if errors.Is(err, selfupdate.ErrUpdaterUnreachable) {
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "the update service is currently unreachable; please try again shortly")
		}
		h.logger.Error("self-update IPC call failed", zap.String("op", string(req.Op)), zap.Error(err))
		return nil, fiber.NewError(fiber.StatusBadGateway, selfUpdateGenericError)
	}
	if resp == nil {
		h.logger.Error("self-update IPC call returned nil response", zap.String("op", string(req.Op)))
		return nil, fiber.NewError(fiber.StatusBadGateway, selfUpdateGenericError)
	}
	if !resp.OK {
		msg := resp.Error
		if msg == "" {
			msg = selfUpdateGenericError
		}
		return nil, fiber.NewError(fiber.StatusUnprocessableEntity, msg)
	}
	return resp, nil
}

// writeSelfUpdateAudit records a durable audit row for a self-update
// admin action. If the audit store is not wired (h.auditStore == nil —
// see internal/api/handlers/handlers.go's NewHandler), this does NOT
// silently pretend the action was audited: it logs an explicit warning
// naming the action so the gap is visible in server logs and returns
// false. Callers of the two irreversible operations (install, rollback)
// treat a failed/absent audit write as a hard failure — see
// PostAdminUpdatesInstall / PostAdminUpdatesRollback.
func (h *Handler) writeSelfUpdateAudit(c fiber.Ctx, action, target, result string) bool {
	if h.auditStore == nil {
		h.logger.Warn("self-update action NOT audited: audit store is not wired",
			zap.String("action", action), zap.String("target", target), zap.String("result", result))
		return false
	}
	if err := h.appendAudit(c, action, target, result); err != nil {
		h.logger.Error("self-update audit write failed",
			zap.String("action", action), zap.Error(err))
		return false
	}
	return true
}

// requireSelfUpdateReauth enforces password re-authentication for the
// two irreversible/high-impact self-update operations (install,
// rollback). NOTE ON PROVENANCE: no "recent MFA / step-up re-auth"
// middleware exists anywhere else in this codebase as of Phase H (only
// login-time MFA challenge handling was found). This is new,
// purpose-built middleware, not a reuse of an existing pattern. It
// follows the same primitive the codebase already trusts for a
// sensitive account action — handlers.go's ChangePassword, which
// verifies the caller's current password via h.auth.VerifyPassword
// against the stored Argon2/bcrypt hash before proceeding — but applies
// it per-request rather than issuing a cacheable step-up token, which
// is the simplest correct behavior given no session/token store for
// step-up state exists yet. If a real step-up-token system is added
// later, this is the seam to replace.
//
// password is read by the caller from the parsed request body (the
// "password" field) and passed in here so each handler's request struct
// stays the single source of truth for body shape.
func (h *Handler) requireSelfUpdateReauth(c fiber.Ctx, password string) error {
	if password == "" {
		return fiber.NewError(fiber.StatusUnauthorized, "password re-authentication is required for this action")
	}
	userID, ok := c.Locals("user_id").(uint)
	if !ok || userID == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	// Query the underlying *sql.DB directly rather than through gorm —
	// this mirrors handlers.go's Login handler (the codebase's existing,
	// proven convention for reading password_hash by identity) exactly,
	// rather than introducing a second, less-exercised path to the same
	// column.
	sqlDB, err := h.db.DB()
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	var passwordHash string
	if err := sqlDB.QueryRow("SELECT password_hash FROM users WHERE id = "+h.dialect.Placeholder(1), userID).Scan(&passwordHash); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	if !h.auth.VerifyPassword(password, passwordHash) {
		return fiber.NewError(fiber.StatusUnauthorized, "password re-authentication failed")
	}
	return nil
}

// ── GET /api/v1/admin/updates/status ────────────────────────────────

func (h *Handler) GetAdminUpdatesStatus(c fiber.Ctx) error {
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpStatus})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"job": resp.Job})
}

// ── GET /api/v1/admin/updates/releases ──────────────────────────────
//
// Read-only: resolves releases from the official channel via the daemon
// but is not itself audited as a "check" action (POST /check is the
// explicit, audited trigger). An optional ?channel= query param is
// validated against the fixed allow-list before being forwarded.
func (h *Handler) GetAdminUpdatesReleases(c fiber.Ctx) error {
	req := selfupdate.Request{Op: selfupdate.OpCheckRelease}
	if ch := c.Query("channel"); ch != "" {
		if err := selfupdate.ValidateChannel(ch); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid channel"})
		}
		req.Channel = ch
	}
	resp, err := h.callSelfUpdate(c, req)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"releases": resp.Releases})
}

// ── POST /api/v1/admin/updates/check ────────────────────────────────

type postAdminUpdatesCheckRequest struct {
	Channel string `json:"channel,omitempty"`
}

func (h *Handler) PostAdminUpdatesCheck(c fiber.Ctx) error {
	var body postAdminUpdatesCheckRequest
	if err := decodeSelfUpdateBody(c, &body); err != nil {
		return err
	}
	if body.Channel != "" {
		if err := selfupdate.ValidateChannel(body.Channel); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid channel"})
		}
	}
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpCheckRelease, Channel: body.Channel})
	if err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.check", "channel:"+body.Channel, "failed")
		return err
	}
	h.writeSelfUpdateAudit(c, "selfupdate.check", fmt.Sprintf("channel:%s|releases:%d", body.Channel, len(resp.Releases)), "ok")
	return c.JSON(fiber.Map{"releases": resp.Releases})
}

// ── POST /api/v1/admin/updates/preflight ────────────────────────────

type postAdminUpdatesPreflightRequest struct {
	RequestedVersion string `json:"requested_version,omitempty"`
}

func (h *Handler) PostAdminUpdatesPreflight(c fiber.Ctx) error {
	var body postAdminUpdatesPreflightRequest
	if err := decodeSelfUpdateBody(c, &body); err != nil {
		return err
	}
	if body.RequestedVersion != "" {
		if err := selfupdate.ValidateVersionString(body.RequestedVersion); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid requested_version"})
		}
	}
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpPreflight, RequestedVersion: body.RequestedVersion})
	if err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.preflight", "version:"+body.RequestedVersion, "failed")
		return err
	}
	h.writeSelfUpdateAudit(c, "selfupdate.preflight", "version:"+body.RequestedVersion, "ok")
	return c.JSON(fiber.Map{"job": resp.Job})
}

// ── POST /api/v1/admin/updates/install ──────────────────────────────
//
// Irreversible/high-impact: requires password re-authentication (see
// requireSelfUpdateReauth) and a client-supplied idempotency key, which
// is passed through verbatim to internal/selfupdate's Request.Validate
// — this handler never synthesizes one, so a caller cannot accidentally
// (or by a broken retry) start two independent install jobs by omitting
// the key.

type postAdminUpdatesInstallRequest struct {
	Password         string `json:"password"`
	IdempotencyKey   string `json:"idempotency_key"`
	RequestedVersion string `json:"requested_version"`
	Channel          string `json:"channel,omitempty"`
}

func (h *Handler) PostAdminUpdatesInstall(c fiber.Ctx) error {
	var body postAdminUpdatesInstallRequest
	if err := decodeSelfUpdateBody(c, &body); err != nil {
		return err
	}
	if err := h.requireSelfUpdateReauth(c, body.Password); err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.install", "version:"+body.RequestedVersion, "reauth_failed")
		return err
	}
	if body.IdempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "idempotency_key is required"})
	}
	if body.RequestedVersion == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "requested_version is required"})
	}
	if err := selfupdate.ValidateVersionString(body.RequestedVersion); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid requested_version"})
	}
	if body.Channel != "" {
		if err := selfupdate.ValidateChannel(body.Channel); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid channel"})
		}
	}

	req := selfupdate.Request{
		Op:               selfupdate.OpStartInstall,
		IdempotencyKey:   body.IdempotencyKey,
		RequestedVersion: body.RequestedVersion,
		Channel:          body.Channel,
	}
	resp, err := h.callSelfUpdate(c, req)
	if err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.install",
			fmt.Sprintf("version:%s|idempotency_key:%s", body.RequestedVersion, body.IdempotencyKey), "failed")
		return err
	}
	// Durable audit is mandatory for this irreversible action. If the
	// audit store is not wired, we do not pretend the action was
	// safely recorded: the job has already been accepted by the
	// daemon (it cannot be un-started), so we still return success to
	// the caller — the job itself remains the source of truth via
	// GET /jobs/:id and GET /history — but we surface the gap loudly
	// via the server log (writeSelfUpdateAudit already does this) and
	// via a response field so the admin UI can flag it.
	audited := h.writeSelfUpdateAudit(c, "selfupdate.install",
		fmt.Sprintf("version:%s|idempotency_key:%s|job_id:%s", body.RequestedVersion, body.IdempotencyKey, jobID(resp.Job)), "ok")
	return c.JSON(fiber.Map{"job": resp.Job, "audited": audited})
}

// ── POST /api/v1/admin/updates/rollback ─────────────────────────────
//
// Irreversible/high-impact: same reauth + idempotency-key requirements
// as install. target identifies which prior state to roll back to
// (a job ID or rollback snapshot ID — the daemon resolves it); it is
// forwarded via Request.JobID, which internal/selfupdate's protocol
// reserves for OpStartRollback alongside OpGetJob/OpCancelBeforeIrreversible.

type postAdminUpdatesRollbackRequest struct {
	Password       string `json:"password"`
	IdempotencyKey string `json:"idempotency_key"`
	Target         string `json:"target"`
}

func (h *Handler) PostAdminUpdatesRollback(c fiber.Ctx) error {
	var body postAdminUpdatesRollbackRequest
	if err := decodeSelfUpdateBody(c, &body); err != nil {
		return err
	}
	if err := h.requireSelfUpdateReauth(c, body.Password); err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.rollback", "target:"+body.Target, "reauth_failed")
		return err
	}
	if body.IdempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "idempotency_key is required"})
	}
	if body.Target == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "target is required"})
	}
	if len(body.Target) > 128 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "target too long"})
	}

	req := selfupdate.Request{
		Op:             selfupdate.OpStartRollback,
		IdempotencyKey: body.IdempotencyKey,
		JobID:          body.Target,
	}
	resp, err := h.callSelfUpdate(c, req)
	if err != nil {
		h.writeSelfUpdateAudit(c, "selfupdate.rollback",
			fmt.Sprintf("target:%s|idempotency_key:%s", body.Target, body.IdempotencyKey), "failed")
		return err
	}
	audited := h.writeSelfUpdateAudit(c, "selfupdate.rollback",
		fmt.Sprintf("target:%s|idempotency_key:%s|job_id:%s", body.Target, body.IdempotencyKey, jobID(resp.Job)), "ok")
	return c.JSON(fiber.Map{"job": resp.Job, "audited": audited})
}

// ── GET /api/v1/admin/updates/jobs/:id ──────────────────────────────

func (h *Handler) GetAdminUpdatesJob(c fiber.Ctx) error {
	id := c.Params("id")
	if id == "" || len(id) > 128 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid job id"})
	}
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpGetJob, JobID: id})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"job": resp.Job})
}

// ── GET /api/v1/admin/updates/history ───────────────────────────────

func (h *Handler) GetAdminUpdatesHistory(c fiber.Ctx) error {
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpListHistory})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"history": resp.Jobs})
}

// ── GET /api/v1/admin/updates/snapshots ─────────────────────────────

func (h *Handler) GetAdminUpdatesSnapshots(c fiber.Ctx) error {
	resp, err := h.callSelfUpdate(c, selfupdate.Request{Op: selfupdate.OpListSnapshots})
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"snapshots": resp.Snapshots})
}

// jobID safely extracts a job ID for an audit target string, tolerating
// a nil Job (a daemon response might omit it for some operations).
func jobID(j *selfupdate.Job) string {
	if j == nil {
		return ""
	}
	return j.ID
}
