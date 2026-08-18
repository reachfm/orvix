package handlers_test

// Route-level acceptance tests for the platform groups CRUD surface
// (POST/DELETE /platform/groups/:tenant_id[/:id[/members...]]),
// exercised through the REAL router and middleware chain:
//
//   - PSA-only gate (tenant roles denied);
//   - CSRF enforcement on mutations;
//   - tenant-scoped mutations (cross-tenant ids can never be reached);
//   - duplicate group name / member email â†’ stable conflicts;
//   - destructive delete requires typed X-Confirm;
//   - no cross-tenant leakage between two tenants' groups.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPlatformGroupCRUD_PSAValidLifecycle(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Create group in tenant 1.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/groups/1", map[string]interface{}{
		"name": "Platform Ops", "description": "platform-created group",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group status %d: %s", resp.StatusCode, raw)
	}
	var g struct {
		ID        uint   `json:"id"`
		TenantID  uint   `json:"tenant_id"`
		Name      string `json:"name"`
		MemberCnt int    `json:"member_count"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if g.ID == 0 || g.TenantID != 1 || g.Name != "Platform Ops" {
		t.Fatalf("unexpected group: %+v", g)
	}

	// Add a member.
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", map[string]interface{}{
		"email": "member@t1.example",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add member status %d: %s", resp.StatusCode, raw)
	}

	// List members.
	resp, raw = env.psaDo(t, "GET", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list members status %d: %s", resp.StatusCode, raw)
	}
	var members struct {
		Members []string `json:"members"`
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members.Members) != 1 || members.Members[0] != "member@t1.example" {
		t.Fatalf("unexpected members: %+v", members.Members)
	}

	// Find the member id, then remove it.
	var memberID int
	_ = env.db.QueryRow(`SELECT id FROM coremail_group_members WHERE group_id=? AND email='member@t1.example'`, g.ID).Scan(&memberID)
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members/"+uiToStr(uint(memberID)), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove member status %d: %s", resp.StatusCode, raw)
	}

	// Delete requires typed confirmation.
	resp, _ = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID), nil, nil)
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("delete without confirmation must be 428, got %d", resp.StatusCode)
	}
	// Wrong confirmation is also rejected.
	resp, _ = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID), nil, map[string]string{"X-Confirm": "DELETE-GROUP-999"})
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("wrong confirmation must be 428, got %d", resp.StatusCode)
	}
	// Correct typed confirmation deletes (soft-delete tombstone).
	resp, raw = env.psaDo(t, "DELETE", "/api/v1/platform/groups/1/"+uiToStr(g.ID), nil, map[string]string{"X-Confirm": "DELETE-GROUP-" + uiToStr(g.ID)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed delete status %d: %s", resp.StatusCode, raw)
	}
	var deleted int
	_ = env.db.QueryRow(`SELECT COUNT(*) FROM coremail_groups WHERE id=? AND deleted_at IS NOT NULL`, g.ID).Scan(&deleted)
	if deleted != 1 {
		t.Fatal("group must be soft-deleted (deleted_at tombstone)")
	}
}

func TestPlatformGroupCRUD_TenantAdminDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/groups/1", env.tenantAdm, map[string]interface{}{
		"name": "Denied Group",
	}, map[string]string{"Cookie": "csrf_token=" + env.tenantCSRF, "X-CSRF-Token": env.tenantCSRF})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformGroupCRUD_MissingCSRFDenied(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.do(t, "POST", "/api/v1/platform/groups/1", env.psaToken, map[string]interface{}{
		"name": "NoCSRF Group",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformGroupCRUD_DuplicateNameConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	body := map[string]interface{}{"name": "Dup Group"}
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/groups/1", body, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: %d %s", resp.StatusCode, raw)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1", body, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate name must be 409, got %d: %s", resp.StatusCode, raw)
	}
}

func TestPlatformGroupCRUD_NoCrossTenantLeakage(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)

	// Group in tenant 1.
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/groups/1", map[string]interface{}{"name": "TenantOne Group"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create t1 group: %d %s", resp.StatusCode, raw)
	}
	var g1 struct {
		ID uint `json:"id"`
	}
	_ = json.Unmarshal(raw, &g1)

	// A member added to tenant 1's group must never be reachable through
	// tenant 2's scope: listing tenant 2's groups must not include it,
	// and adding a member through tenant 2 against tenant 1's group id
	// must fail (group not found in tenant 2).
	resp, _ = env.psaDo(t, "POST", "/api/v1/platform/groups/2/"+uiToStr(g1.ID)+"/members", map[string]interface{}{"email": "x@t2.example"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant member add must be 404, got %d", resp.StatusCode)
	}
	resp, _ = env.psaDo(t, "GET", "/api/v1/platform/groups/2", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list t2 groups: %d", resp.StatusCode)
	}
	var list struct {
		Groups []map[string]interface{} `json:"groups"`
		Total  int                      `json:"total"`
	}
	_ = json.Unmarshal(raw, &list)
	if list.Total != 0 || len(list.Groups) != 0 {
		t.Fatalf("tenant 2 must not see tenant 1's group: %+v", list)
	}
}

func TestPlatformGroupCRUD_DuplicateMemberConflict(t *testing.T) {
	env := buildPlatformProvisioningEnv(t)
	resp, raw := env.psaDo(t, "POST", "/api/v1/platform/groups/1", map[string]interface{}{"name": "Member Group"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create group: %d %s", resp.StatusCode, raw)
	}
	var g struct {
		ID uint `json:"id"`
	}
	_ = json.Unmarshal(raw, &g)
	member := map[string]interface{}{"email": "same@t1.example"}
	resp, _ = env.psaDo(t, "POST", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", member, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first member add: %d", resp.StatusCode)
	}
	resp, raw = env.psaDo(t, "POST", "/api/v1/platform/groups/1/"+uiToStr(g.ID)+"/members", member, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate member must be 409, got %d: %s", resp.StatusCode, raw)
	}
}

func uiToStr(v uint) string {
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
