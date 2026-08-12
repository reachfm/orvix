package handlers_test

// Handler-level idempotency-key-replay integration tests for the
// destructive M13-15 endpoints (dr_admin.go, retention_admin.go,
// platform_billing_admin.go). Service-level idempotency is already
// covered by internal/platform/{dr,retention,billing}'s own tests;
// these tests prove the HTTP layer actually threads the
// Idempotency-Key header through to the service and that a byte-for-
// byte identical replay does NOT perform the destructive action a
// second time — using the real fiber app + route wiring + an
// isolated in-memory SQLite fixture per test, following the same
// harness pattern as backups_test.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/platform/cluster"
	"go.uber.org/zap"
)

// buildIdemHarness spins up a full api.Router (real route wiring,
// real enterprise-admin service construction — dr/retention/platform
// billing services are all wired automatically by api.NewRouter
// exactly as in production) against a fresh in-memory-backed SQLite
// database, and returns an authenticated platform-superadmin
// token+csrf pair.
func buildIdemHarness(t *testing.T) (*api.Router, *sql.DB, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	root := t.TempDir()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(root, "orvix.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Backup.Dir = filepath.Join(root, "backups")
	cfg.CoreMail.DataPath = filepath.Join(root, "coremail")
	cfg.CoreMail.MailStorePath = filepath.Join(cfg.CoreMail.DataPath, "mailstore")
	if err := os.MkdirAll(cfg.CoreMail.MailStorePath, 0750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.CoreMail.DataPath, "attachments"), 0750); err != nil {
		t.Fatalf("mkdir attachments: %v", err)
	}

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	seedPlatformSuperAdminWithPassword(t, sqlDB, "idem-admin@test.local", "TestPassword123!")

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)

	// The cluster service is normally wired by the coremail runtime
	// module self-enrolling at boot (see router.go's ClusterService()
	// wiring); this test harness has no such module, so wire it
	// directly — the DR coordination layer requires it for the
	// fenced-lease backup/restore guard.
	clusterRepo := cluster.NewRepository(sqlDB)
	if err := clusterRepo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("cluster schema: %v", err)
	}
	router.SetClusterService(cluster.NewService(clusterRepo, nil, nil, nil))

	token := idemLogin(t, router)
	csrf := idemCSRF(t, router, token)
	return router, sqlDB, token, csrf
}

func idemLogin(t *testing.T, router *api.Router) string {
	t.Helper()
	req := httptest.NewRequest("POST", "/admin/login", strings.NewReader(`{"username":"idem-admin@test.local","password":"TestPassword123!"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := readIdemBody(resp)
		t.Fatalf("login status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if data.AccessToken == "" {
		t.Fatal("missing access token")
	}
	return data.AccessToken
}

func idemCSRF(t *testing.T, router *api.Router, token string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/csrf-token", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("csrf: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := readIdemBody(resp)
		t.Fatalf("csrf status %d: %s", resp.StatusCode, body)
	}
	var data struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode csrf: %v", err)
	}
	return data.CSRFToken
}

func readIdemBody(resp *http.Response) ([]byte, error) {
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return buf[:n], nil
}

// idemRequest issues one authenticated, CSRF-protected request,
// optionally carrying an Idempotency-Key header, and returns the
// status code and decoded body.
func idemRequest(t *testing.T, router *api.Router, method, path, body, token, csrf, idemKey string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cookie", "csrf_token="+csrf)
	req.Header.Set("X-CSRF-Token", csrf)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	return resp.StatusCode, data
}

// ── DR coordinated backup ────────────────────────────────────────

func TestDRCoordinatedBackup_IdempotencyKeyReplay_DoesNotDoubleExecute(t *testing.T) {
	router, sqlDB, token, csrf := buildIdemHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	idemKey := "dr-backup-replay-1"
	status1, body1 := idemRequest(t, router, "POST", "/api/v1/dr/backup", `{"name":"nightly"}`, token, csrf, idemKey)
	if status1 != http.StatusCreated {
		t.Fatalf("first backup status %d: %s", status1, body1)
	}
	var first struct {
		BackupID string `json:"backup_id"`
	}
	if err := json.Unmarshal(body1, &first); err != nil {
		t.Fatalf("decode first: %v: %s", err, body1)
	}
	if first.BackupID == "" {
		t.Fatalf("expected non-empty backup_id: %s", body1)
	}

	status2, body2 := idemRequest(t, router, "POST", "/api/v1/dr/backup", `{"name":"nightly"}`, token, csrf, idemKey)
	if status2 != http.StatusCreated {
		t.Fatalf("replay status %d: %s", status2, body2)
	}
	var second struct {
		BackupID string `json:"backup_id"`
	}
	if err := json.Unmarshal(body2, &second); err != nil {
		t.Fatalf("decode replay: %v: %s", err, body2)
	}
	if second.BackupID != first.BackupID {
		t.Fatalf("replay created a second backup: first=%s second=%s", first.BackupID, second.BackupID)
	}

	// Confirm exactly one operation was recorded in DR history, and
	// exactly one backup exists in the underlying backup store —
	// proving the replay short-circuited before CreateBackup ran
	// again, not merely that the response happened to match.
	histStatus, histBody := idemRequest(t, router, "GET", "/api/v1/dr/operations", "", token, csrf, "")
	if histStatus != http.StatusOK {
		t.Fatalf("history status %d: %s", histStatus, histBody)
	}
	var hist struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(histBody, &hist); err != nil {
		t.Fatalf("decode history: %v: %s", err, histBody)
	}
	if hist.Total != 1 {
		t.Fatalf("expected exactly one recorded DR operation after replay, got %d", hist.Total)
	}

	listStatus, listBody := idemRequest(t, router, "GET", "/api/v1/admin/backups", "", token, csrf, "")
	if listStatus != http.StatusOK {
		t.Fatalf("backups list status %d: %s", listStatus, listBody)
	}
	var backups []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(listBody, &backups); err != nil {
		t.Fatalf("decode backups list: %v: %s", err, listBody)
	}
	if len(backups) != 1 {
		t.Fatalf("expected exactly one backup on disk after replay, got %d: %s", len(backups), listBody)
	}
}

// ── Retention purge execute ──────────────────────────────────────

// seedPurgeEligibleMailbox inserts a coremail_mailboxes row directly
// (bypassing the API) that is soft-deleted well in the past, so it is
// eligible for retention.ExecutePurge's real MailboxPurgeAdapter
// target under scope "tenant"/tenantID.
func seedPurgeEligibleMailbox(t *testing.T, db *sql.DB, tenantID uint, email string) {
	t.Helper()
	now := time.Now().UTC()
	deletedAt := now.Add(-365 * 24 * time.Hour)
	_, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, status, created_at, updated_at, deleted_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		1, tenantID, "purge-target", email, "", "x", "deleted", now, now, deletedAt)
	if err != nil {
		t.Fatalf("seed purge-eligible mailbox: %v", err)
	}
}

func TestRetentionPurgeExecute_IdempotencyKeyReplay_DoesNotDoubleExecute(t *testing.T) {
	router, sqlDB, token, csrf := buildIdemHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	const tenantID = uint(1)
	seedPurgeEligibleMailbox(t, sqlDB, tenantID, "purge1@example.test")
	seedPurgeEligibleMailbox(t, sqlDB, tenantID, "purge2@example.test")

	idemKey := "retention-purge-replay-1"
	reqBody := fmt.Sprintf(`{"scope_kind":"tenant","scope_id":%d,"confirm":"PURGE-ELIGIBLE-DATA","reason":"idempotency replay test","older_than":"%s"}`,
		tenantID, time.Now().UTC().Format(time.RFC3339))

	status1, body1 := idemRequest(t, router, "POST", "/api/v1/retention/purge/execute", reqBody, token, csrf, idemKey)
	if status1 != http.StatusOK {
		t.Fatalf("first purge status %d: %s", status1, body1)
	}
	var first struct {
		Purged int `json:"purged"`
	}
	if err := json.Unmarshal(body1, &first); err != nil {
		t.Fatalf("decode first purge: %v: %s", err, body1)
	}
	if first.Purged != 2 {
		t.Fatalf("expected first purge to remove the 2 seeded eligible mailboxes, got %d: %s", first.Purged, body1)
	}

	// Confirm the mailboxes are actually gone before replaying.
	var remaining int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=?`, tenantID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining mailboxes: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 mailboxes remaining after first purge, got %d", remaining)
	}

	status2, body2 := idemRequest(t, router, "POST", "/api/v1/retention/purge/execute", reqBody, token, csrf, idemKey)
	if status2 != http.StatusOK {
		t.Fatalf("replay purge status %d: %s", status2, body2)
	}
	var second struct {
		Purged int `json:"purged"`
	}
	if err := json.Unmarshal(body2, &second); err != nil {
		t.Fatalf("decode replay purge: %v: %s", err, body2)
	}
	// If the replay were NOT idempotent, ExecutePurge would run again
	// against an already-empty scope and legitimately return 0 (there
	// is nothing left to purge) — a DIFFERENT number from the first
	// call. Getting back the same purged count (2) proves the replay
	// short-circuited to the cached original result rather than
	// re-running the purge.
	if second.Purged != first.Purged {
		t.Fatalf("replay did not return the cached original result: first=%d second=%d", first.Purged, second.Purged)
	}
}

// ── Platform billing manual adjustment ───────────────────────────

func TestPlatformBillingAdjustment_IdempotencyKeyReplay_DoesNotDoubleExecute(t *testing.T) {
	router, sqlDB, token, csrf := buildIdemHarness(t)
	defer router.App().Shutdown()
	defer sqlDB.Close()

	const tenantID = uint(1)
	idemKey := "billing-adjustment-replay-1"
	reqBody := `{"type":"credit","amount_cents":5000,"currency":"USD","reason":"idempotency replay test"}`

	status1, body1 := idemRequest(t, router, "POST", fmt.Sprintf("/api/v1/platform/billing/tenants/%d/adjustments", tenantID), reqBody, token, csrf, idemKey)
	if status1 != http.StatusCreated {
		t.Fatalf("first adjustment status %d: %s", status1, body1)
	}
	var first struct {
		ID          uint  `json:"id"`
		AmountCents int64 `json:"amount_cents"`
	}
	if err := json.Unmarshal(body1, &first); err != nil {
		t.Fatalf("decode first adjustment: %v: %s", err, body1)
	}

	status2, body2 := idemRequest(t, router, "POST", fmt.Sprintf("/api/v1/platform/billing/tenants/%d/adjustments", tenantID), reqBody, token, csrf, idemKey)
	if status2 != http.StatusCreated {
		t.Fatalf("replay adjustment status %d: %s", status2, body2)
	}
	var second struct {
		ID          uint  `json:"id"`
		AmountCents int64 `json:"amount_cents"`
	}
	if err := json.Unmarshal(body2, &second); err != nil {
		t.Fatalf("decode replay adjustment: %v: %s", err, body2)
	}
	if second.ID != first.ID {
		t.Fatalf("replay created a second adjustment row: first_id=%d second_id=%d", first.ID, second.ID)
	}

	balStatus, balBody := idemRequest(t, router, "GET", fmt.Sprintf("/api/v1/platform/billing/tenants/%d/balance", tenantID), "", token, csrf, "")
	if balStatus != http.StatusOK {
		t.Fatalf("balance status %d: %s", balStatus, balBody)
	}
	var bal struct {
		BalanceCents int64 `json:"balance_cents"`
	}
	if err := json.Unmarshal(balBody, &bal); err != nil {
		t.Fatalf("decode balance: %v: %s", err, balBody)
	}
	if bal.BalanceCents != 5000 {
		t.Fatalf("expected balance to reflect exactly ONE 5000-cent credit (not double-applied), got %d", bal.BalanceCents)
	}

	adjStatus, adjBody := idemRequest(t, router, "GET", fmt.Sprintf("/api/v1/platform/billing/tenants/%d/adjustments", tenantID), "", token, csrf, "")
	if adjStatus != http.StatusOK {
		t.Fatalf("adjustments list status %d: %s", adjStatus, adjBody)
	}
	var adjustments struct {
		Adjustments []struct {
			ID uint `json:"id"`
		} `json:"adjustments"`
	}
	if err := json.Unmarshal(adjBody, &adjustments); err != nil {
		t.Fatalf("decode adjustments list: %v: %s", err, adjBody)
	}
	if len(adjustments.Adjustments) != 1 {
		t.Fatalf("expected exactly one adjustment row after replay, got %d: %s", len(adjustments.Adjustments), adjBody)
	}

	// Gap 2(c): the reconciliation report must agree there is no
	// discrepancy after this single idempotent credit — a real
	// end-to-end check that the reconciliation endpoint reads the
	// same ledger the idempotent adjustment wrote to.
	recStatus, recBody := idemRequest(t, router, "GET", fmt.Sprintf("/api/v1/platform/billing/tenants/%d/reconciliation", tenantID), "", token, csrf, "")
	if recStatus != http.StatusOK {
		t.Fatalf("reconciliation status %d: %s", recStatus, recBody)
	}
	var rec struct {
		StoredBalanceCents     int64 `json:"stored_balance_cents"`
		RecomputedBalanceCents int64 `json:"recomputed_balance_cents"`
		Discrepant             bool  `json:"discrepant"`
	}
	if err := json.Unmarshal(recBody, &rec); err != nil {
		t.Fatalf("decode reconciliation: %v: %s", err, recBody)
	}
	if rec.Discrepant || rec.StoredBalanceCents != 5000 || rec.RecomputedBalanceCents != 5000 {
		t.Fatalf("unexpected reconciliation report after idempotent replay: %+v", rec)
	}
}
