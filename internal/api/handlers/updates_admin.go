package handlers

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/updates"
	"github.com/orvix/orvix/internal/updatecoord"
	"github.com/orvix/orvix/internal/updater"
	"go.uber.org/zap"
)

// Signed update-artifact verification (Milestone 13). This is
// independent of the legacy internal/updater package (which handles
// version checks/changelog/the systemd-oneshot runtime-update path but
// has no cryptographic signature verification) — internal/platform/updates
// adds real ed25519 manifest verification and a staged lifecycle on
// top, without touching internal/updater's existing behavior.

var (
	updatesServiceMu    sync.Mutex
	updatesServiceCache = map[*Handler]*updates.Service{}
)

// updatesTrustedKeyEnv names the environment variable holding one or
// more base64-encoded ed25519 public keys (comma-separated), trusted
// to sign update manifests. No key configured means updates.Service is
// unavailable (fails closed — never verifies against an empty trust
// set as "anything goes").
const updatesTrustedKeyEnv = "ORVIX_UPDATE_SIGNING_PUBLIC_KEYS"

func (h *Handler) updatesService() (*updates.Service, error) {
	updatesServiceMu.Lock()
	defer updatesServiceMu.Unlock()
	if svc, ok := updatesServiceCache[h]; ok && svc != nil {
		return svc, nil
	}
	raw := strings.TrimSpace(os.Getenv(updatesTrustedKeyEnv))
	if raw == "" {
		return nil, fmt.Errorf("no trusted update-signing keys configured (%s)", updatesTrustedKeyEnv)
	}
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(part)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid update-signing public key configured")
		}
		keys = append(keys, ed25519.PublicKey(b))
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid trusted update-signing keys configured")
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return nil, err
	}
	repo := updates.NewRepository(sqlDB)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("updates schema init failed: %w", err)
	}
	var auditStore *audit.ExtendedStore
	if h.auditStore != nil {
		auditStore = audit.NewExtendedStore(sqlDB)
		_ = auditStore.EnsureTable(context.Background())
	}
	bi := updater.ReadBuildInfo()
	stageDir := strings.TrimSpace(os.Getenv("ORVIX_UPDATE_STAGE_DIR"))
	if stageDir == "" {
		stageDir = "/var/lib/orvix/update-staging"
	}
	svc := updates.NewService(repo, updates.NewVerifier(keys...), stageDir, bi.Version, auditStore, nil, nil)
	updatesServiceCache[h] = svc
	return svc, nil
}

// PostUpdateArtifact submits an update artifact for signature
// verification and staging. It never executes anything from the
// artifact or its manifest — verification and staging are pure
// file/hash/signature operations. Expects a JSON body with base64
// fields so the handler stays a thin, testable transport layer over
// updates.Service.
func (h *Handler) PostUpdateArtifact(c fiber.Ctx) error {
	svc, err := h.updatesService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	var req struct {
		ArtifactB64  string `json:"artifact_base64"`
		ManifestJSON string `json:"manifest_json"`
		SignatureB64 string `json:"signature_base64"`
		Version      string `json:"expected_version"`
		Platform     string `json:"expected_platform"`
		Arch         string `json:"expected_arch"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	artifact, err := base64.StdEncoding.DecodeString(req.ArtifactB64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "artifact_base64 is not valid base64"})
	}
	var signature []byte
	if req.SignatureB64 != "" {
		signature, err = base64.StdEncoding.DecodeString(req.SignatureB64)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "signature_base64 is not valid base64"})
		}
	}
	// Validate manifest_json is at least well-formed JSON before handing
	// to the service, so a malformed body gets a 400 rather than a 500.
	if !json.Valid([]byte(req.ManifestJSON)) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "manifest_json is not valid JSON"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	rec, err := svc.SubmitArtifact(c.Context(), artifact, []byte(req.ManifestJSON), signature, req.Version, req.Platform, req.Arch, actorID)
	if err != nil {
		return updatesActionError(c, err)
	}
	h.writeAuditLog(c, "update.artifact.submit", fmt.Sprintf("version:%s|state:%s", rec.Version, rec.State))
	return c.Status(fiber.StatusCreated).JSON(rec)
}

// GetUpdateArtifactStatus returns one staged-update record's status.
func (h *Handler) GetUpdateArtifactStatus(c fiber.Ctx) error {
	svc, err := h.updatesService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid update id"})
	}
	rec, err := svc.GetStatus(c.Context(), uint(idVal))
	if err != nil {
		return updatesActionError(c, err)
	}
	return c.JSON(rec)
}

// GetUpdateArtifactHistory lists recent staged-update records.
func (h *Handler) GetUpdateArtifactHistory(c fiber.Ctx) error {
	svc, err := h.updatesService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	history, err := svc.History(c.Context(), limit)
	if err != nil {
		h.logger.Error("update history failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list update history"})
	}
	return c.JSON(fiber.Map{"history": history})
}

// updateCoordinatorRoot is the FIXED update job/result directory —
// analogous to restoreCoordinatorRoot — matching the systemd
// orvix-update.path watched directory (which cannot read config).
// Overridable via ORVIX_UPDATE_JOBS_DIR only for test/staging
// harnesses.
func (h *Handler) updateCoordinatorRoot() string {
	if v := strings.TrimSpace(os.Getenv("ORVIX_UPDATE_JOBS_DIR")); v != "" {
		return v
	}
	return "/var/lib/orvix/update-jobs"
}

// updateCoordinatorInstalled reports whether the external update-apply
// coordinator (systemd path + service units) is installed, mirroring
// restoreCoordinatorInstalled exactly — the API must fail closed
// rather than accepting an apply/rollback it can never actually run.
//
// ORVIX_UPDATE_COORDINATOR_ASSUME_READY is a staging/test-only
// override with the same "1"/"0"/unset semantics as its restore
// counterpart.
func updateCoordinatorInstalled() bool {
	switch strings.TrimSpace(os.Getenv("ORVIX_UPDATE_COORDINATOR_ASSUME_READY")) {
	case "1":
		return true
	case "0":
		return false
	}
	for _, p := range []string{
		"/etc/systemd/system/orvix-update.path",
		"/lib/systemd/system/orvix-update.path",
		"/usr/lib/systemd/system/orvix-update.path",
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// updateCoordinatorAdapter builds a fresh CoordinatorAdapter over the
// real on-disk job queue, scoped to this handler's stage directory so
// the coordinator's own path-allowlist check (never trust a caller
// path) is anchored at the exact directory updates.Service stages
// verified artifacts into.
func (h *Handler) updateCoordinatorAdapter(actor string) *updates.CoordinatorAdapter {
	stageDir := strings.TrimSpace(os.Getenv("ORVIX_UPDATE_STAGE_DIR"))
	if stageDir == "" {
		stageDir = "/var/lib/orvix/update-staging"
	}
	coord := updatecoord.New(h.updateCoordinatorRoot(), stageDir)
	return updates.NewCoordinatorAdapter(coord, actor)
}

// PostUpdateArtifactApply hands a staged, already-verified update off
// to the external update-apply coordinator — the same
// submit-a-durable-job / never-restart-in-process pattern as
// PostRestoreBackup. It fails closed (503) if the coordinator units
// are not installed, and requires an exact typed confirmation phrase
// since applying an update is a destructive, service-restarting
// action.
func (h *Handler) PostUpdateArtifactApply(c fiber.Ctx) error {
	svc, err := h.updatesService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid update id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Confirm != "APPLY-STAGED-UPDATE" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "apply requires typed confirmation: APPLY-STAGED-UPDATE"})
	}
	if !updateCoordinatorInstalled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "no update-apply coordinator is installed (orvix-update.path/.service); artifact remains staged and was not applied",
		})
	}
	actorID, _ := c.Locals("user_id").(uint)
	actor := fmt.Sprintf("user:%d", actorID)
	coord := h.updateCoordinatorAdapter(actor)
	rec, jobID, err := svc.TriggerApply(c.Context(), uint(idVal), coord, actorID)
	if err == updatecoord.ErrActiveJob {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "an update operation is already in progress"})
	}
	if err == updates.ErrNoCoordinator {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "no update-apply coordinator is installed; artifact remains staged and was not applied",
		})
	}
	if err != nil {
		return updatesActionError(c, err)
	}
	h.writeAuditLog(c, "update.apply.submitted", fmt.Sprintf("update_id:%d|job_id:%s", idVal, jobID))
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"update":   rec,
		"job_id":   jobID,
		"status":   string(updatecoord.StatusPending),
		"poll_url": "/api/v1/admin/updates/operations/" + jobID,
		"message":  "Apply accepted. Orvix will restart and this connection may drop; poll the operation status. Success is reported only after the restarted service passes a health check. On failure the previous version is restored automatically.",
	})
}

// PostUpdateArtifactRollback hands a previously applied update off to
// the same external coordinator for reversion, using the rollback
// metadata (previous version/hash) captured at staging time — the
// actual revert is the coordinator's responsibility, never performed
// in-process. Apply and rollback are mutually exclusive: the
// coordinator's ErrActiveJob check covers both directions since they
// share one job queue/lock.
func (h *Handler) PostUpdateArtifactRollback(c fiber.Ctx) error {
	svc, err := h.updatesService()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": err.Error()})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid update id"})
	}
	var req struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "reason is required"})
	}
	if req.Confirm != "ROLLBACK-APPLIED-UPDATE" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "rollback requires typed confirmation: ROLLBACK-APPLIED-UPDATE"})
	}
	if !updateCoordinatorInstalled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "no update-apply coordinator is installed (orvix-update.path/.service); rollback is unavailable",
		})
	}
	actorID, _ := c.Locals("user_id").(uint)
	actor := fmt.Sprintf("user:%d", actorID)
	coord := h.updateCoordinatorAdapter(actor)
	rec, jobID, err := svc.Rollback(c.Context(), uint(idVal), coord, req.Reason, actorID)
	if err == updatecoord.ErrActiveJob {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "an update operation is already in progress"})
	}
	if err == updates.ErrNoCoordinator {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "no update-apply coordinator is installed"})
	}
	if err != nil {
		return updatesActionError(c, err)
	}
	h.writeAuditLog(c, "update.rollback.submitted", fmt.Sprintf("update_id:%d|job_id:%s|reason:%s", idVal, jobID, req.Reason))
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"update":   rec,
		"job_id":   jobID,
		"status":   string(updatecoord.StatusPending),
		"poll_url": "/api/v1/admin/updates/operations/" + jobID,
	})
}

// GetUpdateOperationStatus returns the durable status of an
// apply/rollback job, read fresh from disk on every call so it works
// across the Orvix restart the coordinator performs.
func (h *Handler) GetUpdateOperationStatus(c fiber.Ctx) error {
	jobID := c.Params("job_id")
	if !updatecoord.ValidJobID(jobID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid update job id"})
	}
	coord := updatecoord.New(h.updateCoordinatorRoot(), "")
	res, err := coord.GetResult(jobID)
	if err == updatecoord.ErrNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "update job not found"})
	}
	if err != nil {
		h.logger.Error("update job status failed", zap.String("job_id", jobID), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read update job status"})
	}
	return c.JSON(res)
}

func updatesActionError(c fiber.Ctx, err error) error {
	switch err {
	case updates.ErrUnsigned, updates.ErrInvalidSignature, updates.ErrHashMismatch, updates.ErrVersionMismatch, updates.ErrPlatformMismatch:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case updates.ErrRecordNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case updates.ErrInvalidTransition:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
}
