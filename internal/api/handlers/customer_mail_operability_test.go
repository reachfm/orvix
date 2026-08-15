package handlers_test

// Phase 8 C2: domain operability enforcement for alias creation.
// Reuses buildEnterpriseSmokeEnv (enterprise_mutation_smoke_test.go) —
// tenant A owns domain id 1 ("tenanta.example", active), tenant B owns
// domain id 2 ("tenantb.example", active).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func (e *enterpriseSmokeEnv) createAliasReq(domainID int, from, to string) *http.Response {
	buf, _ := json.Marshal(map[string]interface{}{
		"domain_id": domainID, "from_addr": from, "to_addr": to,
	})
	req := httptest.NewRequest("POST", "/api/v1/enterprise/aliases", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", strings.Join([]string{
		"access_token=" + e.tenantAToken,
		"csrf_token=" + e.tenantACSRF,
	}, "; "))
	req.Header.Set("X-CSRF-Token", e.tenantACSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		panic(err)
	}
	return resp
}

func TestCreateAlias_ActiveDomainSucceeds(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	resp := e.createAliasReq(1, "op-active@tenanta.example", "dest@tenanta.example")
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201 for an active domain, got %d: %s", resp.StatusCode, string(raw))
	}
}

func TestCreateAlias_DisabledDomainRejected(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	if _, err := e.sqlDB.Exec("UPDATE coremail_domains SET status = 'disabled' WHERE id = 1"); err != nil {
		t.Fatalf("disable domain: %v", err)
	}
	resp := e.createAliasReq(1, "op-disabled@tenanta.example", "dest@tenanta.example")
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a disabled domain, got %d", resp.StatusCode)
	}
	var count int
	if err := e.sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_aliases WHERE from_addr = ?", "op-disabled@tenanta.example").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("no alias may survive a rejected domain check, got %d rows", count)
	}
}

func TestCreateAlias_SuspendedDomainRejected(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	if _, err := e.sqlDB.Exec("UPDATE coremail_domains SET status = 'suspended' WHERE id = 1"); err != nil {
		t.Fatalf("suspend domain: %v", err)
	}
	resp := e.createAliasReq(1, "op-suspended@tenanta.example", "dest@tenanta.example")
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a suspended domain, got %d", resp.StatusCode)
	}
}

func TestCreateAlias_LockedDomainRejected(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	if _, err := e.sqlDB.Exec("UPDATE coremail_domains SET status = 'locked' WHERE id = 1"); err != nil {
		t.Fatalf("lock domain: %v", err)
	}
	resp := e.createAliasReq(1, "op-locked@tenanta.example", "dest@tenanta.example")
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("expected 409 for a locked domain, got %d", resp.StatusCode)
	}
}

func TestCreateAlias_CrossTenantDomainDenied(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	// Tenant A's authenticated session targets domain id 2, which
	// belongs to tenant B — must resolve to the same not-found
	// contract as a genuinely missing domain, never a distinguishable
	// "forbidden" that would confirm domain 2 exists.
	resp := e.createAliasReq(2, "op-crosstenant@tenanta.example", "dest@tenantb.example")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for a cross-tenant domain_id, got %d", resp.StatusCode)
	}
	var count int
	if err := e.sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_aliases WHERE from_addr = ?", "op-crosstenant@tenanta.example").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("no alias may be created against another tenant's domain, got %d rows", count)
	}
}

func TestCreateAlias_UnknownDomainIsNotFound(t *testing.T) {
	e := buildEnterpriseSmokeEnv(t)
	resp := e.createAliasReq(999999, "op-unknown@tenanta.example", "dest@tenanta.example")
	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("expected 404 for an unknown domain_id, got %d", resp.StatusCode)
	}
}
