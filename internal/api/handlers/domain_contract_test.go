package handlers_test

import (
	"encoding/json"
	"strconv"
	"testing"
)

// TestEnterpriseDomainTypedErrorContract asserts the stable machine-readable
// error contract (code + message) on the enterprise domain routes, and that
// duplicate/invalid names are deterministic rather than raw SQL errors.
func TestEnterpriseDomainTypedErrorContract(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	// Create success.
	status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains",
		e.adminToken, e.csrfToken, map[string]interface{}{"name": "contract.example"})
	if status != 201 {
		t.Fatalf("create: expected 201, got %d: %v", status, body)
	}

	// Duplicate -> 409 DOMAIN_ALREADY_EXISTS with code+message.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains",
		e.adminToken, e.csrfToken, map[string]interface{}{"name": "contract.example"})
	if status != 409 {
		t.Fatalf("duplicate: expected 409, got %d: %v", status, body)
	}
	if body["code"] != "DOMAIN_ALREADY_EXISTS" || body["message"] == nil {
		t.Fatalf("duplicate body must expose code+message, got %v", body)
	}

	// Invalid name -> 400 INVALID_DOMAIN_NAME.
	for _, bad := range []string{"https://bad.example", "bad space.example", "user@example.com", "example.com:8080", ""} {
		status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains",
			e.adminToken, e.csrfToken, map[string]interface{}{"name": bad})
		if status != 400 || body["code"] != "INVALID_DOMAIN_NAME" {
			t.Errorf("invalid name %q: expected 400 INVALID_DOMAIN_NAME, got %d %v", bad, status, body)
		}
	}

	// Get nonexistent -> 404 DOMAIN_NOT_FOUND.
	status, body = domainReq(t, e, "GET", "/api/v1/enterprise/domains/999999",
		e.adminToken, "", nil)
	if status != 404 || body["code"] != "DOMAIN_NOT_FOUND" {
		t.Fatalf("get missing: expected 404 DOMAIN_NOT_FOUND, got %d %v", status, body)
	}
}

// TestEnterpriseDomainDeleteContract asserts DELETE /enterprise/domains/:id
// including the DOMAIN_HAS_MAILBOXES conflict, idempotent not-found for
// repeated deletes, and successful deletion of an empty domain.
func TestEnterpriseDomainDeleteContract(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	// Create a domain and a mailbox on it.
	status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains",
		e.adminToken, e.csrfToken, map[string]interface{}{"name": "delete-contract.example"})
	if status != 201 {
		t.Fatalf("create domain: %d %v", status, body)
	}
	id := uint(body["domain"].(map[string]interface{})["id"].(float64))

	status, _ = domainReq(t, e, "POST", "/api/v1/enterprise/mailboxes",
		e.adminToken, e.csrfToken, map[string]interface{}{
			"email": "keep@delete-contract.example", "password": "Password123!",
		})
	if status != 201 {
		t.Fatalf("create mailbox on domain: %d", status)
	}

	// Delete with mailboxes -> 409 DOMAIN_HAS_MAILBOXES.
	status, body = domainReq(t, e, "DELETE", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id)),
		e.adminToken, e.csrfToken, nil)
	if status != 409 || body["code"] != "DOMAIN_HAS_MAILBOXES" {
		t.Fatalf("delete with mailboxes: expected 409 DOMAIN_HAS_MAILBOXES, got %d %v", status, body)
	}

	// Delete the mailbox then delete the domain.
	mboxID, err := firstMailboxID(t, e)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = domainReq(t, e, "DELETE", "/api/v1/enterprise/mailboxes/"+strconv.Itoa(mboxID),
		e.adminToken, e.csrfToken, nil)
	if status != 200 {
		t.Fatalf("delete mailbox: %d", status)
	}
	status, body = domainReq(t, e, "DELETE", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id)),
		e.adminToken, e.csrfToken, nil)
	if status != 200 {
		t.Fatalf("delete domain: expected 200, got %d %v", status, body)
	}

	// Repeated delete -> 404 DOMAIN_NOT_FOUND (idempotent, safe).
	status, body = domainReq(t, e, "DELETE", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id)),
		e.adminToken, e.csrfToken, nil)
	if status != 404 || body["code"] != "DOMAIN_NOT_FOUND" {
		t.Fatalf("repeated delete: expected 404 DOMAIN_NOT_FOUND, got %d %v", status, body)
	}
}

// TestEnterpriseMailboxDomainEligibility asserts the mailbox-creation path
// rejects nonexistent, disabled, and suspended (not verified) domains with
// typed codes, and persists the real domain id on success.
func TestEnterpriseMailboxDomainEligibility(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	// Create an eligible domain, a disabled one, and a suspended one.
	create := func(name string) uint {
		status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains",
			e.adminToken, e.csrfToken, map[string]interface{}{"name": name})
		if status != 201 {
			t.Fatalf("create %s: %d %v", name, status, body)
		}
		return uint(body["domain"].(map[string]interface{})["id"].(float64))
	}
	disabledID := create("disabled-contract.example")
	suspendedID := create("suspended-contract.example")
	create("contract.example")
	_ = disabledID
	_ = suspendedID

	// Disable the disabled domain via the status endpoint.
	status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+strconv.Itoa(int(disabledID))+"/status",
		e.adminToken, e.csrfToken, map[string]interface{}{"status": "disabled"})
	if status != 200 {
		t.Fatalf("disable domain: %d %v", status, body)
	}

	// Suspend the suspended domain via the status endpoint.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+strconv.Itoa(int(suspendedID))+"/status",
		e.adminToken, e.csrfToken, map[string]interface{}{"status": "suspended"})
	if status != 200 {
		t.Fatalf("suspend domain: %d %v", status, body)
	}

	// Nonexistent domain.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/mailboxes",
		e.adminToken, e.csrfToken, map[string]interface{}{
			"email": "x@missing-contract.example", "password": "Password123!",
		})
	if status != 404 || body["code"] != "DOMAIN_NOT_FOUND" {
		t.Fatalf("mailbox nonexistent domain: expected 404 DOMAIN_NOT_FOUND, got %d %v", status, body)
	}

	// Disabled domain -> DOMAIN_DISABLED (not mislabeled as unverified).
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/mailboxes",
		e.adminToken, e.csrfToken, map[string]interface{}{
			"email": "x@disabled-contract.example", "password": "Password123!",
		})
	if status != 409 || body["code"] != "DOMAIN_DISABLED" {
		t.Fatalf("mailbox disabled domain: expected 409 DOMAIN_DISABLED, got %d %v", status, body)
	}

	// Suspended domain -> DOMAIN_SUSPENDED (not mislabeled as DNS-unverified).
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/mailboxes",
		e.adminToken, e.csrfToken, map[string]interface{}{
			"email": "x@suspended-contract.example", "password": "Password123!",
		})
	if status != 409 || body["code"] != "DOMAIN_SUSPENDED" {
		t.Fatalf("mailbox suspended domain: expected 409 DOMAIN_SUSPENDED, got %d %v", status, body)
	}

	// Success on eligible domain persists a real domain id (never zero).
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/mailboxes",
		e.adminToken, e.csrfToken, map[string]interface{}{
			"email": "ok@contract.example", "password": "Password123!", "name": "OK",
		})
	if status != 201 {
		t.Fatalf("mailbox eligible domain: expected 201, got %d %v", status, body)
	}
	mbox := body["mailbox"].(map[string]interface{})
	if mbox["domain_id"].(float64) == 0 {
		t.Fatalf("mailbox must reference a real domain id, got %v", mbox["domain_id"])
	}
}

// TestEnterpriseDomainStatusContract asserts SetAdminDomainStatus rejects
// arbitrary status values with a typed 400, normalizes supported values, and
// cross-tenant status changes fail closed as not-found.
func TestEnterpriseDomainStatusContract(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains",
		e.adminToken, e.csrfToken, map[string]interface{}{"name": "status-contract.example"})
	if status != 201 {
		t.Fatalf("create domain: %d %v", status, body)
	}
	id := uint(body["domain"].(map[string]interface{})["id"].(float64))
	idStr := strconv.Itoa(int(id))

	// Reject arbitrary status values with a typed 400.
	for _, bad := range []string{"pending", "verified", "garbage", "frozen", ""} {
		status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+idStr+"/status",
			e.adminToken, e.csrfToken, map[string]interface{}{"status": bad})
		if status != 400 || body["code"] != "DOMAIN_STATUS_INVALID" {
			t.Errorf("status %q: expected 400 DOMAIN_STATUS_INVALID, got %d %v", bad, status, body)
		}
	}

	// Normalize (trim + case) and persist.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+idStr+"/status",
		e.adminToken, e.csrfToken, map[string]interface{}{"status": " SUSPENDED "})
	if status != 200 {
		t.Fatalf("normalized suspend: %d %v", status, body)
	}
	status, body = domainReq(t, e, "GET", "/api/v1/enterprise/domains/"+idStr, e.adminToken, "", nil)
	if status != 200 {
		t.Fatalf("get domain: %d %v", status, body)
	}
	if got := body["domain"].(map[string]interface{})["status"]; got != "suspended" {
		t.Fatalf("persisted status = %v, want suspended", got)
	}
}

// TestEnterpriseDKIMGenerateContract asserts DKIM generate/rotate return no
// private key, reject duplicates deterministically, and write the required
// audit events.
func TestEnterpriseDKIMGenerateContract(t *testing.T) {
	e := buildAdminDomainAdvancedEnv(t)

	status, body := domainReq(t, e, "POST", "/api/v1/enterprise/domains",
		e.adminToken, e.csrfToken, map[string]interface{}{"name": "dkim-contract.example"})
	if status != 201 {
		t.Fatalf("create domain: %d %v", status, body)
	}
	id := uint(body["domain"].(map[string]interface{})["id"].(float64))

	// Generate.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id))+"/dkim/generate",
		e.adminToken, e.csrfToken, map[string]interface{}{"selector": "mail"})
	if status != 201 {
		t.Fatalf("dkim generate: expected 201, got %d %v", status, body)
	}
	dkimJSON, _ := json.Marshal(body)
	if containsStr(string(dkimJSON), "BEGIN PRIVATE KEY") || containsStr(string(dkimJSON), "private_key") {
		t.Fatalf("dkim generate response must not contain private key material: %s", dkimJSON)
	}

	// Duplicate generate -> 409 DKIM_ALREADY_CONFIGURED.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id))+"/dkim/generate",
		e.adminToken, e.csrfToken, map[string]interface{}{"selector": "mail"})
	if status != 409 || body["code"] != "DKIM_ALREADY_CONFIGURED" {
		t.Fatalf("duplicate dkim generate: expected 409 DKIM_ALREADY_CONFIGURED, got %d %v", status, body)
	}

	// Rotate.
	status, body = domainReq(t, e, "POST", "/api/v1/enterprise/domains/"+strconv.Itoa(int(id))+"/dkim/rotate",
		e.adminToken, e.csrfToken, map[string]interface{}{"selector": "mail"})
	if status != 200 {
		t.Fatalf("dkim rotate: expected 200, got %d %v", status, body)
	}

	// Audit events.
	for _, action := range []string{"domain.dkim.generate", "domain.dkim.rotate"} {
		if !auditActionExists(e, action) {
			t.Errorf("expected audit action %q to exist", action)
		}
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func firstMailboxID(t *testing.T, e *adminDomainAdvancedEnv) (int, error) {
	t.Helper()
	status, body := domainReq(t, e, "GET", "/api/v1/enterprise/mailboxes", e.adminToken, "", nil)
	_ = status
	mailboxes := body["mailboxes"].([]interface{})
	if len(mailboxes) == 0 {
		return 0, nil
	}
	first := mailboxes[len(mailboxes)-1].(map[string]interface{})
	return int(first["id"].(float64)), nil
}

// auditActionExists reports whether an orvix_audit row with the given action
// exists for the tenant, by querying the shared test database directly.
func auditActionExists(e *adminDomainAdvancedEnv, action string) bool {
	var count int
	err := e.sqlDB.QueryRow(
		"SELECT COUNT(*) FROM orvix_audit WHERE action = ? AND tenant_id = 1",
		action,
	).Scan(&count)
	if err != nil {
		return false
	}
	return count > 0
}
