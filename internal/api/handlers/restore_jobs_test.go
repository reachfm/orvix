package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/restorecoord"
)

func restoreTestHandler(t *testing.T) *Handler {
	t.Helper()
	// The coordinator root is a fixed path in production; point it at a temp
	// dir for the test via the documented override.
	t.Setenv("ORVIX_RESTORE_JOBS_DIR", t.TempDir())
	return &Handler{logger: zap.NewNop(), cfg: &config.Config{}}
}

// Restore must fail closed (503) when the external coordinator units are not
// installed: without them the restart/health/rollback lifecycle cannot run, so
// the API must not accept a job it can never complete.
func TestPostRestore_FailsClosedWhenCoordinatorMissing(t *testing.T) {
	t.Setenv("ORVIX_RESTORE_COORDINATOR_ASSUME_READY", "") // ensure no override
	h := restoreTestHandler(t)
	app := fiber.New()
	app.Post("/api/v1/admin/backups/:id/restore", h.PostRestoreBackup)

	req := httptest.NewRequest("POST", "/api/v1/admin/backups/abc123/restore", strings.NewReader(`{"confirm":"restore-orvix-backup"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail-closed)", resp.StatusCode)
	}
}

// The submit response must never claim activation/success; the pre-restart API
// only accepts the job.
func TestPostRestore_SubmitDoesNotClaimSuccess(t *testing.T) {
	t.Setenv("ORVIX_RESTORE_COORDINATOR_ASSUME_READY", "1")
	h := restoreTestHandler(t)
	// Seed a pending result directly via the coordinator so we can assert the
	// pending status wording without a live backup DB. (The POST->submit glue
	// over coordinator.Submit is covered in internal/restorecoord.)
	coord := h.restoreCoordinator()
	job, err := coord.Submit("backup-1", "user:1")
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/api/v1/admin/backups/restore-jobs/:job_id", h.GetRestoreJobStatus)

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/backups/restore-jobs/"+job.ID, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var res restorecoord.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Status != restorecoord.StatusPending {
		t.Fatalf("status = %s, want pending", res.Status)
	}
	if strings.Contains(strings.ToLower(res.Message), "activated") || strings.Contains(strings.ToLower(res.Message), "health verified") {
		t.Fatalf("pending job must not claim activation/health: %q", res.Message)
	}
}

func TestGetRestoreJobStatus_InvalidAndNotFound(t *testing.T) {
	h := restoreTestHandler(t)
	app := fiber.New()
	app.Get("/api/v1/admin/backups/restore-jobs/:job_id", h.GetRestoreJobStatus)

	// Invalid (non-canonical) job id -> 400, never touches the filesystem path.
	resp, _ := app.Test(httptest.NewRequest("GET", "/api/v1/admin/backups/restore-jobs/..%2f..%2fetc%2fpasswd", nil))
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("invalid id: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// Well-formed but unknown id -> 404.
	valid, _ := restorecoord.NewJobID()
	resp, _ = app.Test(httptest.NewRequest("GET", "/api/v1/admin/backups/restore-jobs/"+valid, nil))
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}
