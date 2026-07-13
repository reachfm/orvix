package handlers_test

// Tests for the per-mailbox rules engine API
// (/api/v1/webmail/{rules,vacation,forwarding}).
//
// Pins the contract that each authenticated user can
// only see and modify their OWN rules / vacation /
// forwarding configuration. The contract is enforced
// at two layers:
//
//   1. The handler resolves the caller's mailbox from
//      the JWT identity via resolveWebmailUserContext.
//      The mailbox id is never taken from the URL or
//      body — the caller literally cannot ask for
//      another user's data through the public API.
//
//   2. The storage repository SQL is parameterised on
//      mailbox_id. The WHERE mailbox_id = ? predicate
//      means that even if a caller guesses a foreign
//      rule id, the UPDATE / DELETE returns
//      ErrNoRows. The handler maps that to a 404, the
//      same response shape as "no such rule", so the
//      cross-mailbox probe is indistinguishable from a
//      legitimate not-found.
//
// We exercise the API through the real router, the real
// auth middleware, and the real storage repositories.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// rulesOwnershipEnv bundles a router wired with a
// MailStore + QueueEngine plus two authenticated users
// in the same domain. Both have active mailbox rows.
type rulesOwnershipEnv struct {
	router         *api.Router
	sqlDB          *sql.DB
	aliceToken     string
	bobToken       string
	aliceMailboxID uint
	bobMailboxID   uint
	aliceEmail     string
	bobEmail       string
}

func buildRulesOwnershipEnv(t *testing.T) *rulesOwnershipEnv {
	t.Helper()

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "rules_owner.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	mailstoreDir := filepath.Join(t.TempDir(), "ms")
	_ = mkdirAllHelper(mailstoreDir, 0o750)
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("mailstore ddl: %v", err)
		}
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	for _, stmt := range queue.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("queue ddl: %v", err)
		}
	}
	qe := queue.NewQueueEngine(sqlDB)

	rm := &routingRuntimeModule{store: mailStore, queue: qe}

	scratchDir := t.TempDir()
	adminDir := filepath.Join(scratchDir, "admin")
	_ = mkdirAllHelper(adminDir, 0o755)
	_ = writeFileHelper(filepath.Join(adminDir, "index.html"), "<html></html>")
	_ = writeFileHelper(filepath.Join(adminDir, "app.js"), "")
	_ = writeFileHelper(filepath.Join(adminDir, "styles.css"), "")
	webmailDir := filepath.Join(scratchDir, "webmail")
	_ = mkdirAllHelper(webmailDir, 0o755)
	_ = writeFileHelper(filepath.Join(webmailDir, "index.html"), "<html></html>")
	_ = writeFileHelper(filepath.Join(webmailDir, "auth-gate.css"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "auth-gate.js"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "webmail.css"), "")
	_ = writeFileHelper(filepath.Join(webmailDir, "webmail.js"), "")

	cfg.Server.AdminUIDir = adminDir
	cfg.Server.WebmailUIDir = webmailDir
	cfg.CoreMail.MailStorePath = mailstoreDir
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)
	reg.Register(rm)
	router := api.NewRouter(cfg, authenticator, logger, db, reg, ff, nil)

	// Provision domain + tenant + two users + two mailboxes.
	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		"INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'orvix', 'orvix', 'orvix.email', 'enterprise', 1)",
		now, now,
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ('orvix.email', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)",
		now, now,
	); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	const (
		aliceEmail = "alice@orvix.email"
		alicePass  = "AlicePass!2026"
		bobEmail   = "bob@orvix.email"
		bobPass    = "BobPass!2026"
	)

	aliceBcrypt, _ := bcrypt.GenerateFromPassword([]byte(alicePass), bcrypt.MinCost)
	bobBcrypt, _ := bcrypt.GenerateFromPassword([]byte(bobPass), bcrypt.MinCost)
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'user', 1, 1, 1)",
		now, now, aliceEmail, string(aliceBcrypt),
	); err != nil {
		t.Fatalf("insert alice user: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, 'user', 1, 1, 1)",
		now, now, bobEmail, string(bobBcrypt),
	); err != nil {
		t.Fatalf("insert bob user: %v", err)
	}
	aliceArgon, _ := hashArgon2idRouting(alicePass)
	bobArgon, _ := hashArgon2idRouting(bobPass)
	aliceRes, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes
		 (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'alice', ?, 'Alice', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		aliceEmail, aliceArgon, now, now)
	if err != nil {
		t.Fatalf("insert alice mailbox: %v", err)
	}
	aliceID, _ := aliceRes.LastInsertId()
	bobRes, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes
		 (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'bob', ?, 'Bob', ?, 'argon2id', 'active', 1024, 0, ?, ?)`,
		bobEmail, bobArgon, now, now)
	if err != nil {
		t.Fatalf("insert bob mailbox: %v", err)
	}
	bobID, _ := bobRes.LastInsertId()

	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	return &rulesOwnershipEnv{
		router:         router,
		sqlDB:          sqlDB,
		aliceToken:     loginOwnershipUser(t, router, aliceEmail, alicePass),
		bobToken:       loginOwnershipUser(t, router, bobEmail, bobPass),
		aliceMailboxID: uint(aliceID),
		bobMailboxID:   uint(bobID),
		aliceEmail:     aliceEmail,
		bobEmail:       bobEmail,
	}
}

// loginOwnershipUser POSTs to /api/v1/webmail/login and
// returns the access_token cookie.
func loginOwnershipUser(t *testing.T, router *api.Router, email, password string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/webmail/login", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login(%s): expected 200, got %d, body=%s", email, resp.StatusCode, string(body))
	}
	for _, c := range resp.Cookies() {
		if c.Name == "access_token" {
			return c.Value
		}
	}
	t.Fatalf("login(%s): no access_token cookie", email)
	return ""
}

// doJSON is a small helper that sends an authenticated
// JSON request and returns (status, decoded body).
func doJSON(t *testing.T, router *api.Router, method, path, token string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rdr = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	out := map[string]interface{}{}
	respBody, _ := io.ReadAll(resp.Body)
	if len(respBody) > 0 {
		_ = json.Unmarshal(respBody, &out)
	}
	return resp.StatusCode, out
}

// ── Test: rules API mailbox ownership isolation ─────────────
//
// Alice and Bob are both authenticated users in the same
// domain. Alice creates a rule on her mailbox. Bob's
// GET /webmail/rules MUST NOT include Alice's rule.
// Alice's PUT /webmail/rules/:id on Bob's rule id MUST
// return 404 (the storage layer's mailbox_id predicate
// blocks the cross-mailbox write).

func TestRulesAPI_MailboxOwnershipIsolation(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	// Alice creates a rule.
	status, body := doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.aliceToken, map[string]interface{}{
		"name":            "alice-rule",
		"enabled":         true,
		"sort_order":      0,
		"stop_processing": false,
		"conditions_json": `[{"type":"subject_contains","value":"secret"}]`,
		"actions_json":    `[{"type":"forward","forward_to":"alice-fwd@elsewhere.test"}]`,
	})
	if status != fiber.StatusCreated {
		t.Fatalf("alice create rule: expected 201, got %d, body=%v", status, body)
	}
	aliceRuleIDFloat, _ := body["id"].(float64)
	aliceRuleID := uint(aliceRuleIDFloat)

	// Bob creates a rule.
	status, body = doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.bobToken, map[string]interface{}{
		"name":            "bob-rule",
		"enabled":         true,
		"sort_order":      0,
		"stop_processing": false,
		"conditions_json": `[{"type":"from_contains","value":"vip"}]`,
		"actions_json":    `[{"type":"set_flag","set_flag":{"flagged":true}}]`,
	})
	if status != fiber.StatusCreated {
		t.Fatalf("bob create rule: expected 201, got %d, body=%v", status, body)
	}
	bobRuleIDFloat, _ := body["id"].(float64)
	bobRuleID := uint(bobRuleIDFloat)

	// Alice's list shows only Alice's rule.
	_, aliceList := doJSON(t, e.router, "GET", "/api/v1/webmail/rules", e.aliceToken, nil)
	aliceRules, _ := aliceList["rules"].([]interface{})
	if len(aliceRules) != 1 {
		t.Fatalf("alice rules list: expected 1, got %d", len(aliceRules))
	}
	if name, _ := aliceRules[0].(map[string]interface{})["name"].(string); name != "alice-rule" {
		t.Fatalf("alice rule name: %v, want alice-rule", aliceRules[0])
	}

	// Bob's list shows only Bob's rule.
	_, bobList := doJSON(t, e.router, "GET", "/api/v1/webmail/rules", e.bobToken, nil)
	bobRules, _ := bobList["rules"].([]interface{})
	if len(bobRules) != 1 {
		t.Fatalf("bob rules list: expected 1, got %d", len(bobRules))
	}
	if name, _ := bobRules[0].(map[string]interface{})["name"].(string); name != "bob-rule" {
		t.Fatalf("bob rule name: %v, want bob-rule", bobRules[0])
	}

	// Alice tries to delete Bob's rule by id. The
	// storage layer's WHERE mailbox_id = ? blocks it.
	path := "/api/v1/webmail/rules/" + uintToStr(bobRuleID)
	status, _ = doJSON(t, e.router, "DELETE", path, e.aliceToken, nil)
	if status != fiber.StatusNotFound {
		t.Fatalf("alice deleting bob's rule: expected 404, got %d", status)
	}

	// Bob's rule is still alive.
	var n int
	if err := e.sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_rules WHERE id = ? AND mailbox_id = ?", bobRuleID, e.bobMailboxID).Scan(&n); err != nil {
		t.Fatalf("count bob rule: %v", err)
	}
	if n != 1 {
		t.Fatalf("bob rule should still exist after alice's cross-mailbox DELETE attempt; got %d rows", n)
	}

	// Alice updates her own rule — should succeed.
	path = "/api/v1/webmail/rules/" + uintToStr(aliceRuleID)
	status, _ = doJSON(t, e.router, "PUT", path, e.aliceToken, map[string]interface{}{
		"name": "alice-rule-renamed",
	})
	if status != fiber.StatusOK {
		t.Fatalf("alice updating her own rule: expected 200, got %d", status)
	}

	// Alice tries to update Bob's rule by id — 404.
	path = "/api/v1/webmail/rules/" + uintToStr(bobRuleID)
	status, _ = doJSON(t, e.router, "PUT", path, e.aliceToken, map[string]interface{}{
		"name": "hijack",
	})
	if status != fiber.StatusNotFound {
		t.Fatalf("alice updating bob's rule: expected 404, got %d", status)
	}

	// Bob's rule is still named "bob-rule".
	var name string
	if err := e.sqlDB.QueryRow("SELECT name FROM coremail_rules WHERE id = ? AND mailbox_id = ?", bobRuleID, e.bobMailboxID).Scan(&name); err != nil {
		t.Fatalf("read bob rule: %v", err)
	}
	if name != "bob-rule" {
		t.Fatalf("bob rule was mutated by alice; got name=%q", name)
	}
}

// ── Test: forwarding API mailbox ownership isolation ─────────

func TestForwardingAPI_MailboxOwnershipIsolation(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	// Alice sets her forwarding to alice-fwd.
	status, body := doJSON(t, e.router, "PUT", "/api/v1/webmail/forwarding", e.aliceToken, map[string]interface{}{
		"enabled":    true,
		"forward_to": "alice-fwd@elsewhere.test",
		"keep_copy":  true,
	})
	if status != fiber.StatusOK {
		t.Fatalf("alice put forwarding: expected 200, got %d, body=%v", status, body)
	}
	if got, _ := body["forward_to"].(string); got != "alice-fwd@elsewhere.test" {
		t.Fatalf("alice forward_to = %q", got)
	}

	// Bob fetches his forwarding — should still be defaults
	// (enabled=false, forward_to="").
	_, bobFwd := doJSON(t, e.router, "GET", "/api/v1/webmail/forwarding", e.bobToken, nil)
	if enabled, _ := bobFwd["enabled"].(bool); enabled {
		t.Fatalf("bob forwarding enabled leaked from alice's update")
	}
	if got, _ := bobFwd["forward_to"].(string); got != "" {
		t.Fatalf("bob forward_to leaked: %q", got)
	}

	// Alice fetches her forwarding — what she set.
	_, aliceFwd := doJSON(t, e.router, "GET", "/api/v1/webmail/forwarding", e.aliceToken, nil)
	if got, _ := aliceFwd["forward_to"].(string); got != "alice-fwd@elsewhere.test" {
		t.Fatalf("alice forward_to = %q, want alice-fwd@elsewhere.test", got)
	}
}

// ── Test: vacation API mailbox ownership isolation ───────────

func TestVacationAPI_MailboxOwnershipIsolation(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	// Alice enables her vacation.
	status, body := doJSON(t, e.router, "PUT", "/api/v1/webmail/vacation", e.aliceToken, map[string]interface{}{
		"enabled":                true,
		"subject":                "Alice OOO",
		"body":                   "I am out of the office",
		"reply_interval_seconds": 3600,
	})
	if status != fiber.StatusOK {
		t.Fatalf("alice put vacation: expected 200, got %d, body=%v", status, body)
	}
	if got, _ := body["subject"].(string); got != "Alice OOO" {
		t.Fatalf("alice vacation subject: %q", got)
	}

	// Bob fetches his vacation — still disabled, empty
	// subject and body.
	_, bobVac := doJSON(t, e.router, "GET", "/api/v1/webmail/vacation", e.bobToken, nil)
	if enabled, _ := bobVac["enabled"].(bool); enabled {
		t.Fatalf("bob vacation enabled leaked from alice's update")
	}
	if got, _ := bobVac["subject"].(string); got != "" {
		t.Fatalf("bob vacation subject leaked: %q", got)
	}

	// The vacation rows in the DB confirm Alice and Bob
	// each have their own row.
	var aliceN, bobN int
	if err := e.sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_vacation WHERE mailbox_id = ?", e.aliceMailboxID).Scan(&aliceN); err != nil {
		t.Fatalf("count alice vacation: %v", err)
	}
	if err := e.sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_vacation WHERE mailbox_id = ?", e.bobMailboxID).Scan(&bobN); err != nil {
		t.Fatalf("count bob vacation: %v", err)
	}
	if aliceN != 1 || bobN != 1 {
		t.Fatalf("vacation rows: alice=%d, bob=%d, want both = 1", aliceN, bobN)
	}

	// Alice's vacation row has subject = "Alice OOO";
	// Bob's has subject = "".
	var aliceSubj, bobSubj string
	if err := e.sqlDB.QueryRow("SELECT subject FROM coremail_vacation WHERE mailbox_id = ?", e.aliceMailboxID).Scan(&aliceSubj); err != nil {
		t.Fatalf("read alice vacation: %v", err)
	}
	if err := e.sqlDB.QueryRow("SELECT subject FROM coremail_vacation WHERE mailbox_id = ?", e.bobMailboxID).Scan(&bobSubj); err != nil {
		t.Fatalf("read bob vacation: %v", err)
	}
	if aliceSubj != "Alice OOO" || bobSubj != "" {
		t.Fatalf("vacation subjects: alice=%q bob=%q", aliceSubj, bobSubj)
	}
}

// ── Test: invalid conditions / actions are rejected ─────────
//
// The validator in the rules package is the gatekeeper —
// the handler forwards validation errors as 400 before
// touching the DB.

func TestRulesAPI_RejectsInvalidJSON(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "invalid conditions json",
			body: map[string]interface{}{
				"conditions_json": "not-json",
				"actions_json":    `[{"type":"set_flag","set_flag":{"flagged":true}}]`,
			},
		},
		{
			name: "invalid action type",
			body: map[string]interface{}{
				"conditions_json": `[{"type":"subject_contains","value":"x"}]`,
				"actions_json":    `[{"type":"unicorn_ride"}]`,
			},
		},
		{
			name: "forward action without forward_to",
			body: map[string]interface{}{
				"conditions_json": `[{"type":"subject_contains","value":"x"}]`,
				"actions_json":    `[{"type":"forward"}]`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.aliceToken, tc.body)
			if status != fiber.StatusBadRequest {
				t.Fatalf("expected 400, got %d", status)
			}
		})
	}
}

// ── Test: BLOCKER 6 — strict JSON decoding ─────────────────────
//
// c.Bind().JSON() silently ignores unknown keys, so a
// buggy client could POST {"name": ..., "enabled": ...,
// "evil": "..."} and never see the typo. The handler
// now uses json.NewDecoder(...).DisallowUnknownFields()
// and returns a 400 with the offending field name.

func TestRulesAPI_StrictJSON_RejectsUnknownField_CreateRule(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	status, body := doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.aliceToken, map[string]interface{}{
		"name":            "test",
		"enabled":         true,
		"sort_order":      0,
		"stop_processing": false,
		"conditions_json": `[{"type":"subject_contains","value":"x"}]`,
		"actions_json":    `[{"type":"set_flag","set_flag":{"flagged":true}}]`,
		"evil_field":      "should be rejected",
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("unknown field on create rule: expected 400, got %d, body=%v", status, body)
	}
	if errMsg, _ := body["error"].(string); !strings.Contains(errMsg, "evil_field") {
		t.Fatalf("error message must mention 'evil_field', got: %q", errMsg)
	}
}

func TestRulesAPI_StrictJSON_RejectsUnknownField_UpdateRule(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	// Create a baseline rule.
	status, body := doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.aliceToken, map[string]interface{}{
		"name":            "baseline",
		"enabled":         true,
		"sort_order":      0,
		"stop_processing": false,
		"conditions_json": `[{"type":"subject_contains","value":"x"}]`,
		"actions_json":    `[{"type":"set_flag","set_flag":{"flagged":true}}]`,
	})
	if status != fiber.StatusCreated {
		t.Fatalf("baseline create: %d, %v", status, body)
	}
	idFloat, _ := body["id"].(float64)
	ruleID := uint(idFloat)
	path := "/api/v1/webmail/rules/" + uintToStr(ruleID)

	status, body = doJSON(t, e.router, "PUT", path, e.aliceToken, map[string]interface{}{
		"name":        "renamed",
		"bogus_field": 42,
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("unknown field on update rule: expected 400, got %d, body=%v", status, body)
	}
	if errMsg, _ := body["error"].(string); !strings.Contains(errMsg, "bogus_field") {
		t.Fatalf("error message must mention 'bogus_field', got: %q", errMsg)
	}
}

func TestVacationAPI_StrictJSON_RejectsUnknownField_PutVacation(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	status, body := doJSON(t, e.router, "PUT", "/api/v1/webmail/vacation", e.aliceToken, map[string]interface{}{
		"enabled":   true,
		"subject":   "Out of office",
		"body":      "I am out",
		"typoField": "should be rejected",
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("unknown field on vacation: expected 400, got %d, body=%v", status, body)
	}
	if errMsg, _ := body["error"].(string); !strings.Contains(errMsg, "typoField") {
		t.Fatalf("error message must mention 'typoField', got: %q", errMsg)
	}
}

func TestForwardingAPI_StrictJSON_RejectsUnknownField_PutForwarding(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	status, body := doJSON(t, e.router, "PUT", "/api/v1/webmail/forwarding", e.aliceToken, map[string]interface{}{
		"enabled":     true,
		"forward_to":  "fwd@elsewhere.test",
		"keep_copy":   true,
		"surpriseKey": "should be rejected",
	})
	if status != fiber.StatusBadRequest {
		t.Fatalf("unknown field on forwarding: expected 400, got %d, body=%v", status, body)
	}
	if errMsg, _ := body["error"].(string); !strings.Contains(errMsg, "surpriseKey") {
		t.Fatalf("error message must mention 'surpriseKey', got: %q", errMsg)
	}
}

// TestRulesAPI_StrictJSON_ValidRequestStillPasses is the
// positive control — strict decoding must not reject a
// well-formed payload.

func TestRulesAPI_StrictJSON_ValidRequestStillPasses(t *testing.T) {
	e := buildRulesOwnershipEnv(t)

	// Create rule (no unknown fields).
	status, body := doJSON(t, e.router, "POST", "/api/v1/webmail/rules", e.aliceToken, map[string]interface{}{
		"name":            "valid",
		"enabled":         true,
		"sort_order":      0,
		"stop_processing": false,
		"conditions_json": `[{"type":"subject_contains","value":"x"}]`,
		"actions_json":    `[{"type":"set_flag","set_flag":{"flagged":true}}]`,
	})
	if status != fiber.StatusCreated {
		t.Fatalf("valid create rule: expected 201, got %d, body=%v", status, body)
	}

	// Valid vacation PUT.
	status, body = doJSON(t, e.router, "PUT", "/api/v1/webmail/vacation", e.aliceToken, map[string]interface{}{
		"enabled":                true,
		"subject":                "Out of office",
		"body":                   "I am out",
		"reply_interval_seconds": 3600,
	})
	if status != fiber.StatusOK {
		t.Fatalf("valid vacation PUT: expected 200, got %d, body=%v", status, body)
	}

	// Valid forwarding PUT.
	status, body = doJSON(t, e.router, "PUT", "/api/v1/webmail/forwarding", e.aliceToken, map[string]interface{}{
		"enabled":    true,
		"forward_to": "fwd@elsewhere.test",
		"keep_copy":  true,
	})
	if status != fiber.StatusOK {
		t.Fatalf("valid forwarding PUT: expected 200, got %d, body=%v", status, body)
	}
}

// ── Helper ───────────────────────────────────────────────────

func uintToStr(v uint) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[v%10]
		v /= 10
	}
	return string(buf[i:])
}
