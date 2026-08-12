package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/api/publicv1"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

func newPublicAPITestRouter(t *testing.T) (*Router, string, string) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "public-api.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatal(err)
	}
	authn, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(cfg, authn, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.Shutdown()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	writeKey, _, err := router.apikeys.Generate("writer", 10, 1, string(auth.RoleTenantAdmin), []string{publicv1.ScopeGroupsRead, publicv1.ScopeGroupsWrite, publicv1.ScopeAliasesRead, publicv1.ScopeAliasesWrite}, 1)
	if err != nil {
		t.Fatal(err)
	}
	readKey, _, err := router.apikeys.Generate("reader", 11, 1, string(auth.RoleTenantReadOnly), []string{publicv1.ScopeGroupsRead}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return router, writeKey, readKey
}

func publicRequest(t *testing.T, router *Router, method, path, key, idem, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := router.App().Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPublicAPIRealRouterIdempotencyAndScopes(t *testing.T) {
	router, writeKey, readKey := newPublicAPITestRouter(t)
	first := publicRequest(t, router, "POST", "/api/v1/public/groups", writeKey, "group-create", `{"name":"ops","description":"Operations"}`)
	second := publicRequest(t, router, "POST", "/api/v1/public/groups", writeKey, "group-create", `{ "description":"Operations", "name":"ops" }`)
	if first.StatusCode != 201 || second.StatusCode != 201 || second.Header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay statuses=%d/%d replay=%q", first.StatusCode, second.StatusCode, second.Header.Get("Idempotency-Replayed"))
	}
	sqlDB, _ := router.db.DB()
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_groups WHERE tenant_id=1 AND name='ops'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("group count=%d err=%v", count, err)
	}
	if got := publicRequest(t, router, "POST", "/api/v1/public/groups", writeKey, "group-create", `{"name":"different"}`); got.StatusCode != 409 {
		t.Fatalf("changed body status=%d", got.StatusCode)
	}
	if got := publicRequest(t, router, "POST", "/api/v1/public/groups", readKey, "read-cannot-write", `{"name":"denied"}`); got.StatusCode != 403 {
		t.Fatalf("read scope write status=%d", got.StatusCode)
	}
	if got := publicRequest(t, router, "POST", "/api/v1/public/groups", writeKey, "", `{"name":"missing-idem"}`); got.StatusCode != 400 {
		t.Fatalf("missing idempotency status=%d", got.StatusCode)
	}
	if got := publicRequest(t, router, "GET", "/api/v1/platform/organizations", writeKey, "", ""); got.StatusCode >= 200 && got.StatusCode < 300 {
		t.Fatalf("tenant public key accessed platform route: status=%d", got.StatusCode)
	}
}

func TestPublicAPIRealRouterPaginationFilteringAndTenantIsolation(t *testing.T) {
	router, _, readKey := newPublicAPITestRouter(t)
	sqlDB, _ := router.db.DB()
	now := time.Now().UTC()
	for _, row := range []struct {
		tenant int
		name   string
	}{{1, "beta"}, {1, "alpha"}, {1, "gamma"}, {2, "private"}} {
		if _, err := sqlDB.Exec("INSERT INTO coremail_groups (tenant_id,name,description,created_at,updated_at) VALUES (?,?,?,?,?)", row.tenant, row.name, "", now, now); err != nil {
			t.Fatal(err)
		}
	}
	resp := publicRequest(t, router, "GET", "/api/v1/public/groups?page=1&page_size=2", readKey, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body publicv1.GroupList
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0].Name != "alpha" || body.Data[1].Name != "beta" || body.Page.TotalCount != 3 || body.Page.TotalPages != 2 {
		t.Fatalf("unexpected page: %+v", body)
	}
	resp = publicRequest(t, router, "GET", "/api/v1/public/groups?page=2&page_size=2", readKey, "", "")
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Name != "gamma" {
		t.Fatalf("unexpected second page: %+v", body.Data)
	}
	if got := publicRequest(t, router, "GET", "/api/v1/public/groups?page=0", readKey, "", ""); got.StatusCode != 400 {
		t.Fatalf("invalid page status=%d", got.StatusCode)
	}
	resp = publicRequest(t, router, "GET", "/api/v1/public/groups?search=nomatch", readKey, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("empty page status=%d", resp.StatusCode)
	}
	var empty publicv1.GroupList
	if err := json.NewDecoder(resp.Body).Decode(&empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Data) != 0 || empty.Page.TotalCount != 0 {
		t.Fatalf("empty page=%+v", empty)
	}
}

func TestPublicAPIAliasMoveRespectsTargetDomainCap(t *testing.T) {
	router, writeKey, _ := newPublicAPITestRouter(t)
	sqlDB, _ := router.db.DB()
	now := time.Now().UTC()
	for _, statement := range []string{
		"INSERT INTO coremail_domains (id,name,tenant_id,status,plan,max_mailboxes,max_aliases,max_quota_mb,created_at,updated_at) VALUES (101,'source.example',1,'active','enterprise',10,10,1024,?,?)",
		"INSERT INTO coremail_domains (id,name,tenant_id,status,plan,max_mailboxes,max_aliases,max_quota_mb,created_at,updated_at) VALUES (102,'target.example',1,'active','enterprise',10,1,1024,?,?)",
	} {
		if _, err := sqlDB.Exec(statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlDB.Exec("INSERT INTO coremail_aliases (id,domain_id,tenant_id,from_addr,to_addr,active,created_at,updated_at) VALUES (201,101,1,'move@source.example','dest@source.example',1,?,?)", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec("INSERT INTO coremail_aliases (id,domain_id,tenant_id,from_addr,to_addr,active,created_at,updated_at) VALUES (202,102,1,'full@target.example','dest@target.example',1,?,?)", now, now); err != nil {
		t.Fatal(err)
	}
	resp := publicRequest(t, router, "PATCH", "/api/v1/public/aliases/201", writeKey, "alias-move", `{"domain_id":102,"source":"move@target.example","destination":"dest@target.example"}`)
	if resp.StatusCode != 409 {
		t.Fatalf("alias move status=%d, want 409", resp.StatusCode)
	}
	var domainID uint
	if err := sqlDB.QueryRow("SELECT domain_id FROM coremail_aliases WHERE id=201").Scan(&domainID); err != nil || domainID != 101 {
		t.Fatalf("alias moved despite cap: domain=%d err=%v", domainID, err)
	}
}
