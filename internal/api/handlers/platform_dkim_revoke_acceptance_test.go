package handlers_test

// Route-level acceptance tests for the platform domain DKIM revoke
// surface (POST /api/v1/platform/domains/:tenant_id/:id/dkim/revoke):
// real transactional revoke through the production domain service;
// never exposes key material; tenant-scoped; CSRF + PSA gate enforced.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPlatformDomainDKIMRevoke_ValidLifecycle(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Create a domain with DKIM in tenant 1.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "dkim-revoke.example.com", "status": "active", "dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	}, map[string]string{"Idempotency-Key": "dkim-revoke-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Domain struct {
			ID           uint   `json:"id"`
			DKIMEnabled  bool   `json:"dkim_enabled"`
			DKIMSelector string `json:"dkim_selector"`
		} `json:"domain"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Domain.ID == 0 || !created.Domain.DKIMEnabled {
		t.Fatalf("domain must be created with DKIM enabled: %+v", created.Domain)
	}

	// Revoke DKIM.
	path := "/api/v1/platform/domains/1/" + uiToStr(created.Domain.ID) + "/dkim/revoke"
	resp, raw = env.psaDo(t, "POST", path, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status %d: %s", resp.StatusCode, raw)
	}

	// Verify real persisted state: domain DKIM disabled, config row
	// disabled, history entry recorded.
	var dkimEnabled int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE id=? AND tenant_id=1 AND dkim_enabled=0 AND deleted_at IS NULL`, created.Domain.ID).Scan(&dkimEnabled)
	if dkimEnabled != 1 {
		t.Fatal("domain must have dkim_enabled=0 after revoke")
	}
	var disabledConfigs int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_config WHERE domain=? AND enabled=0`, "dkim-revoke.example.com").Scan(&disabledConfigs)
	if disabledConfigs != 1 {
		t.Fatal("dkim config row must be disabled after revoke")
	}
	var historyEntries int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_dkim_selector_history WHERE domain=? AND action='revoked'`, "dkim-revoke.example.com").Scan(&historyEntries)
	if historyEntries != 1 {
		t.Fatal("dkim history must record the revoke event")
	}
	// Audit evidence.
	var auditRows int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action='platform.domain.dkim.revoke'`).Scan(&auditRows)
	if auditRows != 1 {
		t.Fatal("platform revoke must be audited")
	}
}

func TestPlatformDomainDKIMRevoke_NotConfiguredConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	// Domain WITHOUT DKIM.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "no-dkim.example.com", "status": "active",
	}, map[string]string{"Idempotency-Key": "no-dkim-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Domain struct {
			ID uint `json:"id"`
		} `json:"domain"`
	}
	_ = json.Unmarshal(raw, &created)

	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/1/"+uiToStr(created.Domain.ID)+"/dkim/revoke", nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("revoking unconfigured DKIM must be 409, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformDomainDKIMRevoke_CrossTenantScoped(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	// Domain in tenant 1.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/domains/1", map[string]interface{}{
		"name": "revoke-scope.example.com", "status": "active", "dkim": map[string]interface{}{"generate": true, "selector": "mail"},
	}, map[string]string{"Idempotency-Key": "scope-create"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain: %d %s", resp.StatusCode, raw)
	}
	var created struct {
		Domain struct {
			ID uint `json:"id"`
		} `json:"domain"`
	}
	_ = json.Unmarshal(raw, &created)

	// Tenant 2's scope must not revoke tenant 1's domain.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/domains/2/"+uiToStr(created.Domain.ID)+"/dkim/revoke", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant revoke must be 404, got %d: %s", resp.StatusCode, raw)
	}
	var stillEnabled int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE id=? AND tenant_id=1 AND dkim_enabled=1`, created.Domain.ID).Scan(&stillEnabled)
	if stillEnabled != 1 {
		t.Fatal("cross-tenant revoke must not touch the domain")
	}
}

func TestPlatformDomainDKIMRevoke_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/domains/1/1/dkim/revoke", env.tenantAdm, nil, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied, got %d: %s", resp.StatusCode, raw)
	}
}
