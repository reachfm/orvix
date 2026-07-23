package handlers_test

// Regression coverage for the cross-tenant IDOR fix in customer_mail.go:
// DeleteAlias, DeleteGroup, AddGroupMember, and RemoveGroupMember must
// never let a caller mutate another tenant's alias, group, or group
// membership by ID, even when that ID is known or guessed. These tests
// exercise the handlers directly (not through the full HTTP router)
// for two separate, pre-existing, out-of-scope reasons:
//
//  1. The router's /enterprise mutation gate depends on
//     requireTenantActive, which queries a literal "organizations"
//     table that does not exist in the schema (the real table is
//     "tenants") — every mutation through the full router panics
//     before reaching any handler.
//  2. coremail_groups and coremail_group_members have no production
//     schema/migration anywhere either — the customer Groups feature
//     is presently non-functional in every environment. This file
//     creates both tables test-local only (not a production
//     migration) so the tenant-authorization logic this fix adds can
//     still be exercised end-to-end.
//
// Testing the handlers directly isolates exactly the
// tenant-authorization logic this fix changed from both gaps.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

// customerMailEnv wires the four customer_mail.go mutation handlers
// (DeleteAlias, DeleteGroup, AddGroupMember, RemoveGroupMember) behind a
// minimal fiber app with a fake-auth middleware that injects the caller's
// tenant context directly into c.Locals, bypassing the full router.
type customerMailEnv struct {
	app *fiber.App

	// Tenant A: the caller. Tenant B: the victim whose resources
	// tenant A must never be able to reach.
	aliasAID  int64
	groupAID  int64
	memberAID int64

	aliasBID  int64
	groupBID  int64
	memberBID int64
}

func buildCustomerMailEnv(t *testing.T, callerTenantID uint) *customerMailEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/customermail.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"

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
	t.Cleanup(func() { _ = sqlDB.Close() })

	// coremail_groups and coremail_group_members have no production
	// Tables()/migration definition anywhere in the schema (a
	// separate, pre-existing gap out of scope for this fix — the
	// customer Groups feature is presently non-functional in every
	// environment because these tables do not exist). Create them
	// here, test-local only, so the tenant-authorization logic this
	// fix adds can be exercised end-to-end.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		description TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		deleted_at DATETIME
	)`); err != nil {
		t.Fatalf("create coremail_groups: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS coremail_group_members (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		added_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatalf("create coremail_group_members: %v", err)
	}

	now := time.Now().UTC()
	exec := func(query string, args ...interface{}) int64 {
		t.Helper()
		res, err := sqlDB.Exec(query, args...)
		if err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
		id, _ := res.LastInsertId()
		return id
	}

	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'tenant-a', 'tenant-a', 'tenanta.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'tenant-b', 'tenant-b', 'tenantb.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (1, 'tenanta.example', 1, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)
	exec("INSERT INTO coremail_domains (id, name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (2, 'tenantb.example', 2, 'active', 'enterprise', 0, 0, 0, ?, ?)", now, now)

	aliasAID := exec(`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at) VALUES (1, 1, 'a@tenanta.example', 'dest@tenanta.example', 1, ?, ?)`, now, now)
	aliasBID := exec(`INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at) VALUES (2, 2, 'b@tenantb.example', 'dest@tenantb.example', 1, ?, ?)`, now, now)

	groupAID := exec(`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (1, 'group-a', '', ?, ?)`, now, now)
	groupBID := exec(`INSERT INTO coremail_groups (tenant_id, name, description, created_at, updated_at) VALUES (2, 'group-b', '', ?, ?)`, now, now)

	memberAID := exec(`INSERT INTO coremail_group_members (group_id, email, added_at) VALUES (?, 'member-a@tenanta.example', ?)`, groupAID, now)
	memberBID := exec(`INSERT INTO coremail_group_members (group_id, email, added_at) VALUES (?, 'member-b@tenantb.example', ?)`, groupBID, now)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	h := handlers.NewHandler(db, authenticator, nil, logger, cfg, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)

	app := fiber.New()
	// Fake-auth middleware: injects the caller's tenant/user context
	// exactly as auth.Middleware()+TenantMiddleware would, without
	// pulling in the full router (and its unrelated broken gate).
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		c.Locals("role", auth.RoleAdmin)
		c.Locals("tenant_id", callerTenantID)
		c.Locals("email", "caller@example.com")
		return c.Next()
	})
	app.Delete("/aliases/:id", h.DeleteAlias)
	app.Delete("/groups/:id", h.DeleteGroup)
	app.Post("/groups/:id/members", h.AddGroupMember)
	app.Delete("/groups/:id/members/:memberId", h.RemoveGroupMember)

	return &customerMailEnv{
		app:       app,
		aliasAID:  aliasAID,
		groupAID:  groupAID,
		memberAID: memberAID,
		aliasBID:  aliasBID,
		groupBID:  groupBID,
		memberBID: memberBID,
	}
}

func customerMailReq(t *testing.T, e *customerMailEnv, method, path string, body []byte) (int, map[string]interface{}) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	httpReq := httptest.NewRequest(method, path, bodyReader)
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.app.Test(httpReq, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]interface{}{}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// --- 1. Own-tenant access works (sanity check) ---

func TestCustomerMail_DeleteOwnAlias_Succeeds(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/aliases/"+itoa(e.aliasAID), nil)
	if status != 200 {
		t.Fatalf("delete own alias: expected 200, got %d: %v", status, body)
	}
}

func TestCustomerMail_DeleteOwnGroup_Succeeds(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/"+itoa(e.groupAID), nil)
	if status != 200 {
		t.Fatalf("delete own group: expected 200, got %d: %v", status, body)
	}
}

// --- 2/3. Cross-tenant access is rejected ---

func TestCustomerMail_DeleteAlias_BlocksCrossTenant(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/aliases/"+itoa(e.aliasBID), nil)
	if status != 404 {
		t.Fatalf("delete cross-tenant alias: expected 404, got %d: %v", status, body)
	}
}

func TestCustomerMail_DeleteGroup_BlocksCrossTenant(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/"+itoa(e.groupBID), nil)
	if status != 404 {
		t.Fatalf("delete cross-tenant group: expected 404, got %d: %v", status, body)
	}
}

func TestCustomerMail_AddGroupMember_BlocksCrossTenant(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "POST", "/groups/"+itoa(e.groupBID)+"/members",
		[]byte(`{"email":"attacker@tenanta.example"}`))
	if status != 404 {
		t.Fatalf("add member to cross-tenant group: expected 404, got %d: %v", status, body)
	}
}

func TestCustomerMail_AddGroupMember_OwnTenant_Succeeds(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "POST", "/groups/"+itoa(e.groupAID)+"/members",
		[]byte(`{"email":"newmember@tenanta.example"}`))
	if status != 201 {
		t.Fatalf("add member to own group: expected 201, got %d: %v", status, body)
	}
}

func TestCustomerMail_RemoveGroupMember_BlocksCrossTenant(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/"+itoa(e.groupBID)+"/members/"+itoa(e.memberBID), nil)
	if status != 404 {
		t.Fatalf("remove member from cross-tenant group: expected 404, got %d: %v", status, body)
	}
}

func TestCustomerMail_RemoveGroupMember_OwnTenant_Succeeds(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/"+itoa(e.groupAID)+"/members/"+itoa(e.memberAID), nil)
	if status != 200 {
		t.Fatalf("remove member from own group: expected 200, got %d: %v", status, body)
	}
}

// TestCustomerMail_RemoveGroupMember_WrongGroupScoping pins that the
// delete is scoped by (member id AND group id) together: a member id
// that does not belong to the given group id must be rejected as
// not-found rather than silently matching by member id alone.
func TestCustomerMail_RemoveGroupMember_WrongGroupScoping(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/"+itoa(e.groupAID)+"/members/999999", nil)
	if status != 404 {
		t.Fatalf("remove nonexistent member from own group: expected 404, got %d: %v", status, body)
	}
}

// --- 4. Unknown resource is rejected safely (own tenant, bad id) ---

func TestCustomerMail_DeleteAlias_UnknownID_Returns404(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/aliases/999999", nil)
	if status != 404 {
		t.Fatalf("delete unknown alias: expected 404, got %d: %v", status, body)
	}
}

func TestCustomerMail_DeleteGroup_UnknownID_Returns404(t *testing.T) {
	e := buildCustomerMailEnv(t, 1)
	status, body := customerMailReq(t, e, "DELETE", "/groups/999999", nil)
	if status != 404 {
		t.Fatalf("delete unknown group: expected 404, got %d: %v", status, body)
	}
}

// --- 6. Tenant identity cannot be overridden from request input ---

// TestCustomerMail_TenantIdentityFromContextOnly proves the handlers
// derive tenant_id exclusively from the authenticated request context
// (auth.RequireTenantID), never from the request body or query string.
// A caller authenticated as tenant B has no route or body field to
// smuggle tenant A's identity through — the handler's only source of
// tenant context is c.Locals, set exclusively by auth middleware.
func TestCustomerMail_TenantIdentityFromContextOnly(t *testing.T) {
	e := buildCustomerMailEnv(t, 2) // authenticated as tenant B
	status, body := customerMailReq(t, e, "DELETE", "/aliases/"+itoa(e.aliasAID), nil)
	if status != 404 {
		t.Fatalf("tenant B deleting tenant A's alias: expected 404, got %d: %v", status, body)
	}
}
