package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	platformjobs "github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
	"go.uber.org/zap"
)

func newAutomationHandlerHarness(t *testing.T) (*fiber.App, *platformjobs.Service, *sql.DB) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "automation.db") + "?_pragma=busy_timeout(5000)&_txlock=immediate"
	gdb, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err = models.MigrateAllRaw(gdb); err != nil {
		t.Fatal(err)
	}
	db, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := platformjobs.NewJobRepository(db)
	if err = repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	registry := platformjobs.NewRegistry()
	validator := func(payload json.RawMessage) error {
		var body struct {
			DomainID uint `json:"domain_id"`
		}
		if json.Unmarshal(payload, &body) != nil || body.DomainID == 0 {
			return context.Canceled
		}
		return nil
	}
	noop := func(context.Context, platformjobs.Execution, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"ok":true}`), nil
	}
	if err = registry.Register(platformjobs.Definition{Type: "tenant.domain.verify", Scope: platformjobs.ScopeTenant, PayloadVersion: 1, Timeout: time.Minute, Validate: validator, Handle: noop}); err != nil {
		t.Fatal(err)
	}
	if err = registry.Register(platformjobs.Definition{Type: "platform.webhooks.outbox", Scope: platformjobs.ScopePlatform, PayloadVersion: 1, Timeout: time.Minute, Validate: func(payload json.RawMessage) error { return nil }, Handle: noop}); err != nil {
		t.Fatal(err)
	}
	service := platformjobs.NewServiceWithRegistry(repo, registry, kernel.NewFixedClock(time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)))
	h := NewHandler(gdb, nil, nil, zap.NewNop(), cfg, modules.NewRegistry(zap.NewNop()), license.NewFeatureFlags(zap.NewNop()), nil)
	h.SetAutomationJobs(service, nil)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenant_id", uint(7))
		c.Locals("user_id", uint(42))
		c.Locals("role", auth.RoleTenantAdmin)
		return c.Next()
	})
	app.Post("/jobs", h.SubmitTenantAutomationJob)
	app.Get("/jobs", h.ListTenantAutomationJobs)
	app.Get("/jobs/:id", h.GetTenantAutomationJob)
	app.Post("/jobs/:id/cancel", h.CancelTenantAutomationJob)
	app.Post("/jobs/:id/retry", h.RetryTenantAutomationJob)
	app.Post("/platform/jobs", h.SubmitPlatformAutomationJob)
	return app, service, db
}

func automationRequest(t *testing.T, app *fiber.App, method, path, body, key string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	req.Header.Set("X-Request-ID", "req-test")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAutomationHandlersSubmissionIdempotencyAndRedaction(t *testing.T) {
	app, _, db := newAutomationHandlerHarness(t)
	body := `{"type":"tenant.domain.verify","payload":{"domain_id":9}}`
	if resp := automationRequest(t, app, http.MethodPost, "/jobs", body, ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing idempotency status=%d", resp.StatusCode)
	}
	first := automationRequest(t, app, http.MethodPost, "/jobs", body, "idem-1")
	firstBody := readAutomationBody(t, first)
	if first.StatusCode != http.StatusAccepted || strings.Contains(firstBody, `"payload":`) || strings.Contains(firstBody, "lease_token") || strings.Contains(firstBody, "idempotency_key") {
		t.Fatalf("unsafe submission status=%d body=%s", first.StatusCode, firstBody)
	}
	second := automationRequest(t, app, http.MethodPost, "/jobs", body, "idem-1")
	if second.StatusCode != http.StatusAccepted || !strings.Contains(readAutomationBody(t, second), `"idempotent_replay":true`) {
		t.Fatalf("idempotent replay status=%d", second.StatusCode)
	}
	changed := automationRequest(t, app, http.MethodPost, "/jobs", `{"type":"tenant.domain.verify","payload":{"domain_id":10}}`, "idem-1")
	if changed.StatusCode != http.StatusConflict {
		t.Fatalf("changed payload status=%d body=%s", changed.StatusCode, readAutomationBody(t, changed))
	}
	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_audit WHERE action LIKE 'automation.job.%' AND tenant_id=7 AND actor='user:42'`).Scan(&auditCount); err != nil || auditCount < 2 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
}

func TestAutomationHandlersTenantIsolationFiltersAndPlatformTypeGate(t *testing.T) {
	app, service, _ := newAutomationHandlerHarness(t)
	job, _, err := service.Submit(context.Background(), platformjobs.Submission{TenantID: 8, Scope: platformjobs.ScopeTenant, Actor: "user:8", Type: "tenant.domain.verify", Payload: json.RawMessage(`{"domain_id":4}`), IdempotencyKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if resp := automationRequest(t, app, http.MethodGet, "/jobs/"+strconv.Itoa(int(job.ID)), "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant get status=%d", resp.StatusCode)
	}
	if resp := automationRequest(t, app, http.MethodGet, "/jobs?page_size=201", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid page size status=%d", resp.StatusCode)
	}
	if resp := automationRequest(t, app, http.MethodGet, "/jobs?status=invented", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid status status=%d", resp.StatusCode)
	}
	platformType := `{"type":"platform.webhooks.outbox","payload":{}}`
	if resp := automationRequest(t, app, http.MethodPost, "/jobs", platformType, "wrong-scope"); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tenant submitted platform type status=%d", resp.StatusCode)
	}
	if resp := automationRequest(t, app, http.MethodPost, "/platform/jobs", platformType, "platform-ok"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("platform submission status=%d body=%s", resp.StatusCode, readAutomationBody(t, resp))
	}
}

func TestAutomationHandlersCancelAndRetryContracts(t *testing.T) {
	app, service, _ := newAutomationHandlerHarness(t)
	create := automationRequest(t, app, http.MethodPost, "/jobs", `{"type":"tenant.domain.verify","payload":{"domain_id":9}}`, "cancel")
	var envelope struct {
		Job platformjobs.Job `json:"job"`
	}
	if err := json.NewDecoder(create.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	cancel := automationRequest(t, app, http.MethodPost, "/jobs/"+strconv.Itoa(int(envelope.Job.ID))+"/cancel", "", "")
	if cancel.StatusCode != http.StatusOK || !strings.Contains(readAutomationBody(t, cancel), `"status":"cancelled"`) {
		t.Fatalf("cancel status=%d", cancel.StatusCode)
	}
	failed, _, err := service.Submit(context.Background(), platformjobs.Submission{TenantID: 7, Scope: platformjobs.ScopeTenant, Actor: "user:42", Type: "tenant.domain.verify", Payload: json.RawMessage(`{"domain_id":10}`), IdempotencyKey: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := service.Claim(context.Background(), "worker", time.Minute)
	if claim.ID != failed.ID {
		t.Fatalf("claimed id=%d want=%d", claim.ID, failed.ID)
	}
	if err = service.Fail(context.Background(), platformjobs.Lease{JobID: claim.ID, Owner: claim.LeaseOwner, Token: claim.LeaseToken, LeaseVersion: claim.LeaseVersion}, "FAILED", "safe failure", false); err != nil {
		t.Fatal(err)
	}
	path := "/jobs/" + strconv.Itoa(int(failed.ID)) + "/retry"
	if resp := automationRequest(t, app, http.MethodPost, path, "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing retry key status=%d", resp.StatusCode)
	}
	first := automationRequest(t, app, http.MethodPost, path, "", "retry-1")
	second := automationRequest(t, app, http.MethodPost, path, "", "retry-1")
	if first.StatusCode != http.StatusOK || second.StatusCode != http.StatusOK || !strings.Contains(readAutomationBody(t, second), `"idempotent_replay":true`) {
		t.Fatalf("retry status=%d/%d", first.StatusCode, second.StatusCode)
	}
}

func TestAutomationHandlersRejectUnknownFieldsAndTrailingJSON(t *testing.T) {
	app, _, _ := newAutomationHandlerHarness(t)
	for _, body := range []string{
		`{"type":"tenant.domain.verify","payload":{"domain_id":9},"command":"rm"}`,
		`{"type":"tenant.domain.verify","payload":{"domain_id":9}} {}`,
	} {
		resp := automationRequest(t, app, http.MethodPost, "/jobs", body, "strict")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("strict JSON status=%d body=%s", resp.StatusCode, readAutomationBody(t, resp))
		}
	}
}

func readAutomationBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(response.Body)
	return body.String()
}
