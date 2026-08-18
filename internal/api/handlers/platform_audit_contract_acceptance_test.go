package handlers_test

// Route-level acceptance tests for the platform Audit Log surface
// contract (GET /api/v1/audit/logs). The console expects
// {entries, total, limit, offset} with rich extended entries and real
// filters — this pins the fix that replaced the old bare-array,
// filter-less, fixed-limit-100 response.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func seedAuditRows(t *testing.T, env *provEnv) {
	t.Helper()
	// Write extended audit rows the way mutation services do.
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := env.db.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(`INSERT INTO orvix_audit (actor, actor_id, actor_role, tenant_id, action, target, result, ip, user_agent, timestamp) VALUES ('user:10', 10, 'platform_super_admin', 0, 'organization.create', 'tenant:1', 'success', '127.0.0.1', 'test-agent', datetime('now'))`)
	exec(`INSERT INTO orvix_audit (actor, actor_id, actor_role, tenant_id, action, target, result, ip, user_agent, timestamp) VALUES ('user:11', 11, 'tenant_admin', 1, 'mailbox.create', 'mailbox:5', 'success', '127.0.0.1', 'test-agent', datetime('now'))`)
	exec(`INSERT INTO orvix_audit (actor, actor_id, actor_role, tenant_id, action, target, result, ip, user_agent, timestamp) VALUES ('user:12', 12, 'tenant_admin', 1, 'mailbox.delete', 'mailbox:6', 'success', '127.0.0.1', 'test-agent', datetime('now'))`)
}

func TestAuditLogList_EnvelopeContractWithPagination(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)

	resp, raw := env.psaDo(t, "GET", "/api/v1/audit/logs?limit=2&offset=1", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Entries []map[string]interface{} `json:"entries"`
		Total   int                      `json:"total"`
		Limit   int                      `json:"limit"`
		Offset  int                      `json:"offset"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total < 3 {
		t.Fatalf("total must reflect all matching rows, got %d", out.Total)
	}
	if out.Limit != 2 || out.Offset != 1 {
		t.Fatalf("limit/offset must echo the request: %d/%d", out.Limit, out.Offset)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("pagination must return exactly limit rows, got %d", len(out.Entries))
	}
	// Rich extended entry fields must be present (the contract the
	// console's AuditEntry type expects).
	first := out.Entries[0]
	for _, key := range []string{"id", "actor", "actor_id", "actor_role", "tenant_id", "action", "result", "ip", "user_agent", "timestamp"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("entry must carry %q, got %+v", key, first)
		}
	}
}

func TestAuditLogList_Filters(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)

	// Filter by action.
	resp, raw := env.psaDo(t, "GET", "/api/v1/audit/logs?action=mailbox.create", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Entries []map[string]interface{} `json:"entries"`
		Total   int                      `json:"total"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Total != 1 || len(out.Entries) != 1 {
		t.Fatalf("action filter must return exactly the matching row, got total=%d len=%d", out.Total, len(out.Entries))
	}
	if out.Entries[0]["action"] != "mailbox.create" {
		t.Fatalf("wrong entry: %+v", out.Entries[0])
	}

	// Filter by tenant_id.
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs?tenant_id=1", nil, nil)
	_ = json.Unmarshal(raw, &out)
	if out.Total != 2 {
		t.Fatalf("tenant filter must return the two tenant-1 rows, got total=%d", out.Total)
	}

	// Filter by actor string ("user:11").
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs?actor=user:11", nil, nil)
	_ = json.Unmarshal(raw, &out)
	if out.Total != 1 || out.Entries[0]["actor"] != "user:11" {
		t.Fatalf("actor filter must return the matching row, got total=%d entries=%+v", out.Total, out.Entries)
	}

	// page/page_size aliases.
	resp, raw = env.psaDo(t, "GET", "/api/v1/audit/logs?page=1&page_size=2", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page aliases: %d %s", resp.StatusCode, raw)
	}
	var paged struct {
		Entries []map[string]interface{} `json:"entries"`
		Limit   int                      `json:"limit"`
		Offset  int                      `json:"offset"`
	}
	_ = json.Unmarshal(raw, &paged)
	if paged.Limit != 2 || paged.Offset != 0 || len(paged.Entries) != 2 {
		t.Fatalf("page=1&page_size=2 must map to limit=2 offset=0, got %+v", paged)
	}
}

func TestAuditLogList_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	// The platform audit route is platformMW-gated.
	resp, raw := env.do(t, "GET", "/api/v1/audit/logs", env.tenantAdm, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied the platform audit log, got %d: %s", resp.StatusCode, raw)
	}
}

func TestAuditLogDetail_ExtendedEntry(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	seedAuditRows(t, env)

	var id int64
	_ = env.db.QueryRow(`SELECT id FROM orvix_audit WHERE action='organization.create' ORDER BY id DESC LIMIT 1`).Scan(&id)

	resp, raw := env.psaDo(t, "GET", "/api/v1/audit/logs/"+i64ToStr(id), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status %d: %s", resp.StatusCode, raw)
	}
	var entry map[string]interface{}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"id", "actor", "actor_id", "actor_role", "tenant_id", "action", "result", "ip", "user_agent", "timestamp"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("detail must carry %q, got %+v", key, entry)
		}
	}
	if entry["action"] != "organization.create" {
		t.Fatalf("wrong detail entry: %+v", entry)
	}

	// Missing id → 404.
	resp, _ = env.psaDo(t, "GET", "/api/v1/audit/logs/99999999", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing entry must be 404, got %d", resp.StatusCode)
	}
}

func i64ToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
