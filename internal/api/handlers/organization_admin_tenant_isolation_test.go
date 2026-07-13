package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/auth"

	_ "modernc.org/sqlite"
)

// TestEnterpriseGetOrganizationTenantIsolation verifies that the
// tenant-scoped /enterprise/organizations/:id endpoint cannot be used
// to read another tenant's organization by id (cross-tenant IDOR). A
// caller in tenant 1 must get 404 for organization 2, and 200 for its
// own organization 1.
func TestEnterpriseGetOrganizationTenantIsolation(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT, slug TEXT, domain TEXT, plan TEXT,
		max_domains INTEGER, max_mailboxes INTEGER,
		logo_url TEXT, primary_color TEXT, active INTEGER,
		created_at DATETIME, updated_at DATETIME, deleted_at DATETIME
	)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, o := range []struct {
		name, slug, domain string
	}{
		{"Tenant A", "tenant-a", "tenant-a.test"},
		{"Tenant B", "tenant-b", "tenant-b.test"},
	} {
		if _, err := db.Exec(
			"INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (?, ?, ?, 'smb', 10, 500, 1, ?, ?)",
			o.name, o.slug, o.domain, now, now); err != nil {
			t.Fatal(err)
		}
	}

	svc := organization.NewService(organization.NewOrganizationRepo(db), nil, nil)
	h := &Handler{orgAdminSvc: svc, logger: zap.NewNop()}

	app := fiber.New()
	// Simulate an authenticated operator in tenant 1.
	app.Use(func(c fiber.Ctx) error {
		c.Locals("user_id", uint(7))
		c.Locals("role", auth.RoleOperator)
		c.Locals("tenant_id", uint(1))
		return c.Next()
	})
	app.Get("/enterprise/organizations/:id", h.GetOrganization)

	// Cross-tenant read must be denied with 404 (no existence leak).
	resp, err := app.Test(httptest.NewRequest("GET", "/enterprise/organizations/2", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("cross-tenant GET org 2 as tenant 1: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// Same-tenant read must succeed and return the caller's own org.
	resp, err = app.Test(httptest.NewRequest("GET", "/enterprise/organizations/1", nil))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("same-tenant GET org 1 as tenant 1: status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Organization organization.Organization `json:"organization"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if body.Organization.Slug != "tenant-a" {
		t.Fatalf("same-tenant read returned slug %q, want tenant-a", body.Organization.Slug)
	}
}
