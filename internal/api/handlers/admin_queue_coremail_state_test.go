package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
)

// buildQueueStateHandler builds a bare Handler (no router/middleware — the
// three queue-admin handlers are mounted directly) against a fresh SQLite
// DB, with CoreMail enabled/disabled and the coremail_queue table present
// or absent, as requested. This isolates the exact contract under test
// (internal/api/handlers/admin_queue.go's coreMailUnavailableResponse and
// its three callers) from authorization/routing, which are already
// covered by TestAdminQueueRoutesAuth and route_separation_test.go.
func buildQueueStateHandler(t *testing.T, coreMailEnabled, createTable bool) (*handlers.Handler, *fiber.App) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/state_test.db?_loc=auto&_busy_timeout=5000"
	cfg.CoreMail.Enabled = coreMailEnabled

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if createTable {
		for _, stmt := range append(queue.Tables(), queue.Indexes()...) {
			if _, err := sqlDB.Exec(stmt); err != nil {
				t.Fatalf("queue schema: %v", err)
			}
		}
	}

	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	h := handlers.NewHandler(db, authn, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	if createTable {
		h.SetQueueEngine(queue.NewQueueEngine(sqlDB))
	}

	app := fiber.New()
	app.Get("/api/v1/queue", h.ListQueue)
	app.Get("/api/v1/admin/queue/summary", h.AdminQueueSummary)
	app.Get("/api/v1/admin/queue/messages", h.AdminQueueList)
	return h, app
}

func decodeBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func assertNoLeakage(t *testing.T, m map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(m)
	s := strings.ToLower(string(raw))
	for _, forbidden := range []string{"sql", "coremail_queue", "dsn", "sqlite", "postgres", "password"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, string(raw))
		}
	}
}

// 1. CoreMail disabled -> exact 503 contract for all three handlers.
func TestQueueHandlers_CoreMailDisabled_Exact503Contract(t *testing.T) {
	_, app := buildQueueStateHandler(t, false, true)

	for _, path := range []string{"/api/v1/queue", "/api/v1/admin/queue/summary", "/api/v1/admin/queue/messages"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if resp.StatusCode != fiber.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", path, resp.StatusCode)
		}
		body := decodeBody(t, resp)
		if body["code"] != "COREMAIL_DISABLED" {
			t.Fatalf("%s: code = %v, want COREMAIL_DISABLED", path, body["code"])
		}
		if body["error"] != "mail queue unavailable" {
			t.Fatalf("%s: error = %v, want the stable contract string", path, body["error"])
		}
		assertNoLeakage(t, body)
	}
}

// 2 & 3. CoreMail enabled with a valid table: current 200 schema preserved,
// including the empty-queue case (a valid 200 empty result, never 503).
func TestQueueHandlers_CoreMailEnabled_ValidTable_200(t *testing.T) {
	_, app := buildQueueStateHandler(t, true, true)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("/queue: status = %d, want 200", resp.StatusCode)
	}
	var arr []any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		t.Fatalf("decode /queue: %v", err)
	}
	if len(arr) != 0 {
		t.Fatalf("expected an empty queue, got %d entries", len(arr))
	}

	resp2, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/messages", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != fiber.StatusOK {
		t.Fatalf("/admin/queue/messages: status = %d, want 200", resp2.StatusCode)
	}
	body2 := decodeBody(t, resp2)
	if body2["total"] != float64(0) {
		t.Fatalf("expected total=0 for an empty enabled queue, got %v", body2["total"])
	}

	resp3, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/summary", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp3.StatusCode != fiber.StatusOK {
		t.Fatalf("/admin/queue/summary: status = %d, want 200", resp3.StatusCode)
	}
}

// 4. CoreMail enabled but the required table is missing: sanitized 500,
// never a raw SQL/table-name leak, never silently reported as empty.
func TestQueueHandlers_CoreMailEnabled_MissingTable_Sanitized500(t *testing.T) {
	_, app := buildQueueStateHandler(t, true, false)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/admin/queue/messages", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	assertNoLeakage(t, decodeBody(t, resp))

	resp2, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != fiber.StatusInternalServerError {
		t.Fatalf("/queue: status = %d, want 500 (missing table must not be silently reported as an empty queue)", resp2.StatusCode)
	}
	assertNoLeakage(t, decodeBody(t, resp2))
}
