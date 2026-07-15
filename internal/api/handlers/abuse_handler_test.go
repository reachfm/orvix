package handlers_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/abuse"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

func TestAbuseSignalHandlersValidateIDAndEnvelope(t *testing.T) {
	app, _ := newAbuseHandlerApp(t, 1, 42)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"malformed acknowledge", "/enterprise/abuse/signals/not-a-number/acknowledge"},
		{"zero acknowledge", "/enterprise/abuse/signals/0/acknowledge"},
		{"malformed resolve", "/enterprise/abuse/signals/not-a-number/resolve"},
		{"zero resolve", "/enterprise/abuse/signals/0/resolve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := abuseHandlerPost(t, app, tc.path)
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%v, want 400", status, body)
			}
			if body["error"] != "invalid signal id" {
				t.Fatalf("stable JSON error envelope missing: %v", body)
			}
		})
	}
}

func TestAbuseSignalHandlersUseTenantAndOperatorContext(t *testing.T) {
	app, db := newAbuseHandlerApp(t, 1, 42)
	ownSignal := insertAbuseSignal(t, db, 1)
	otherTenantSignal := insertAbuseSignal(t, db, 2)

	status, body := abuseHandlerPost(t, app, "/enterprise/abuse/signals/"+abuseIDString(ownSignal)+"/acknowledge")
	if status != http.StatusOK || body["status"] != "acknowledged" {
		t.Fatalf("acknowledge own signal: status=%d body=%v", status, body)
	}
	assertSignalAuditColumn(t, db, ownSignal, "acknowledged_by", 42)

	status, body = abuseHandlerPost(t, app, "/enterprise/abuse/signals/"+abuseIDString(ownSignal)+"/resolve")
	if status != http.StatusOK || body["status"] != "resolved" {
		t.Fatalf("resolve own signal: status=%d body=%v", status, body)
	}
	assertSignalAuditColumn(t, db, ownSignal, "resolved_by", 42)

	status, body = abuseHandlerPost(t, app, "/enterprise/abuse/signals/"+abuseIDString(otherTenantSignal)+"/acknowledge")
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant acknowledge: status=%d body=%v, want safe 404", status, body)
	}
	if body["error"] != "signal not found" {
		t.Fatalf("cross-tenant response leaked or changed envelope: %v", body)
	}
	status, body = abuseHandlerPost(t, app, "/enterprise/abuse/signals/"+abuseIDString(otherTenantSignal)+"/resolve")
	if status != http.StatusNotFound {
		t.Fatalf("cross-tenant resolve: status=%d body=%v, want safe 404", status, body)
	}
	if body["error"] != "signal not found" {
		t.Fatalf("cross-tenant response leaked or changed envelope: %v", body)
	}
}

func newAbuseHandlerApp(t *testing.T, tenantID, operatorID uint) (*fiber.App, *sql.DB) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/abuse-handler.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	gdb, err := config.NewDatabase(&cfg.Database, zap.NewNop())
	if err != nil {
		t.Fatalf("open sqlite gorm: %v", err)
	}
	db, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := billing.CreateTables(db); err != nil {
		t.Fatalf("billing tables: %v", err)
	}

	h := handlers.NewHandler(gdb, nil, nil, zap.NewNop(), cfg, modules.NewRegistry(zap.NewNop()), license.NewFeatureFlags(zap.NewNop()), nil)
	h.SetAbuseSignalService(abuse.NewSignalService(db, abuse.NewRateLimitService(db)))

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenant_id", tenantID)
		c.Locals("user_id", operatorID)
		return c.Next()
	})
	app.Post("/enterprise/abuse/signals/:id/acknowledge", h.AcknowledgeAbuseSignal)
	app.Post("/enterprise/abuse/signals/:id/resolve", h.ResolveAbuseSignal)
	return app, db
}

func abuseHandlerPost(t *testing.T, app *fiber.App, path string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil))
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	return resp.StatusCode, body
}

func insertAbuseSignal(t *testing.T, db *sql.DB, tenantID uint) uint {
	t.Helper()
	now := time.Now().UTC()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO abuse_signals (tenant_id, signal_type, severity, description, detected_at, created_at)
		VALUES (?, 'high_bounce_rate', 'warning', 'test signal', ?, ?)`,
		tenantID, now, now)
	if err != nil {
		t.Fatalf("insert abuse signal: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("signal id: %v", err)
	}
	return uint(id)
}

func assertSignalAuditColumn(t *testing.T, db *sql.DB, signalID uint, column string, want uint) {
	t.Helper()
	var got uint
	if err := db.QueryRow("SELECT "+column+" FROM abuse_signals WHERE id = ?", signalID).Scan(&got); err != nil {
		t.Fatalf("read %s: %v", column, err)
	}
	if got != want {
		t.Fatalf("%s=%d want %d", column, got, want)
	}
}

func abuseIDString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
