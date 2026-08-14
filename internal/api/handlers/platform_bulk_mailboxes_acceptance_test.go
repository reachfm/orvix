package handlers_test

// Route-level acceptance tests for the platform bulk mailbox
// provisioning surface (feature/bulk-mailbox-provisioning-backend).
// These exercise the REAL router (api.NewRouter) and the REAL
// middleware chain — auth, RBAC, CSRF, idempotency — against the
// production wiring under /api/v1/platform/mailboxes/bulk/... Sibling
// route tests (platform_provisioning_acceptance_test.go) are NOT a
// substitute: every request below hits the actual bulk-mailbox
// handlers and the actual bulkprovision.Service/Repository.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
)

type bulkEnv struct {
	router     *api.Router
	db         *sql.DB
	psaToken   string
	psaCSRF    string
	t1AdmToken string
	t1AdmCSRF  string
	t1OpToken  string
	t2AdmToken string
	t2AdmCSRF  string
}

const (
	bulkPSAEmail   = "bulk-psa@platform.local"
	bulkPSAPass    = "BulkPlatformPass!2026"
	bulkT1AdmEmail = "bulk-admin@tenant1.local"
	bulkT1OpEmail  = "bulk-operator@tenant1.local"
	bulkT2AdmEmail = "bulk-admin@tenant2.local"
	bulkT2AdmPass  = "BulkTenant2Pass!2026"
)

func buildBulkMailboxEnv(t *testing.T) *bulkEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/bulk.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	cfg.Imports.StagingDir = t.TempDir()
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
	for _, stmt := range storage.Tables() {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("storage schema: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		_, _ = sqlDB.Exec(stmt)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	now := time.Now().UTC()
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (1, ?, ?, 'bulk-tenant-a', 'bulk-tenant-a', 'bt1.example', 'enterprise', 1)", now, now)
	exec("INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (2, ?, ?, 'bulk-tenant-b', 'bulk-tenant-b', 'bt2.example', 'enterprise', 1)", now, now)

	t1Adm := seedTenantAdmin(t, sqlDB, bulkT1AdmEmail, 1)
	t1Op := seedTenantOperator(t, sqlDB, bulkT1OpEmail, 1)
	t2Adm := seedTenantAdminWithPassword(t, sqlDB, bulkT2AdmEmail, 2, bulkT2AdmPass)
	psaHash, _ := authenticator.HashPassword(bulkPSAPass)
	exec("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, '"+bulkPSAEmail+"', ?, 'platform_super_admin', NULL, 1, 1)", now, now, psaHash)

	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		_ = router.App().Shutdown()
		_ = sqlDB.Close()
	})

	psaToken := importRouteLogin(t, router, bulkPSAEmail, bulkPSAPass)
	t1AdmToken := importRouteLogin(t, router, bulkT1AdmEmail, t1Adm.Password)
	t1OpToken := importRouteLogin(t, router, bulkT1OpEmail, t1Op.Password)
	t2AdmToken := importRouteLogin(t, router, bulkT2AdmEmail, t2Adm.Password)
	return &bulkEnv{
		router:     router,
		db:         sqlDB,
		psaToken:   psaToken,
		psaCSRF:    importRouteCSRF(t, router, psaToken),
		t1AdmToken: t1AdmToken,
		t1AdmCSRF:  importRouteCSRF(t, router, t1AdmToken),
		t1OpToken:  t1OpToken,
		t2AdmToken: t2AdmToken,
		t2AdmCSRF:  importRouteCSRF(t, router, t2AdmToken),
	}
}

func (e *bulkEnv) do(t *testing.T, method, path, token string, body io.Reader, contentType string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *bulkEnv) doJSON(t *testing.T, method, path, token, csrf string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	h := map[string]string{}
	if csrf != "" {
		h["Cookie"] = "csrf_token=" + csrf
		h["X-CSRF-Token"] = csrf
	}
	for k, v := range headers {
		h[k] = v
	}
	return e.do(t, method, path, token, rdr, "application/json", h)
}

// psaStage uploads csvBody as tenant-1's PSA bulk stage request and
// returns the decoded response.
func (e *bulkEnv) psaStage(t *testing.T, tenantID uint, filename string, csvBody []byte, idemKey string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(csvBody); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	h := map[string]string{"Cookie": "csrf_token=" + e.psaCSRF, "X-CSRF-Token": e.psaCSRF}
	if idemKey != "" {
		h["Idempotency-Key"] = idemKey
	}
	return e.do(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/bulk/%d/stage", tenantID), e.psaToken, &buf, mw.FormDataContentType(), h)
}

// createDomain creates a domain for tenantID via the real platform
// domain-provisioning route (already-proven wiring from PR #71) and
// returns its ID.
func (e *bulkEnv) createDomain(t *testing.T, tenantID uint, name string) uint {
	t.Helper()
	resp, raw := e.doJSON(t, "POST", fmt.Sprintf("/api/v1/platform/domains/%d", tenantID), e.psaToken, e.psaCSRF,
		map[string]any{"name": name, "status": "active"}, map[string]string{"Idempotency-Key": "domain-" + name})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create domain %s: %d %s", name, resp.StatusCode, raw)
	}
	var out struct {
		Domain struct {
			ID uint `json:"id"`
		} `json:"domain"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode domain: %v", err)
	}
	return out.Domain.ID
}

const bulkTestCSV = "email,name,quota_mb\nalice@%s,Alice,500\nbob@%s,Bob,500\n"

// ── template ──────────────────────────────────────────────────────

func TestBulkMailbox_TemplateDownload_PSA(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	resp, raw := env.do(t, "GET", "/api/v1/platform/mailboxes/bulk/template?format=csv", env.psaToken, nil, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("template status %d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "email") {
		t.Fatalf("expected a CSV header row, got: %s", raw)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("template response must never be cached")
	}
}

func TestBulkMailbox_TemplateDownload_Unauthenticated_Denied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	resp, _ := env.do(t, "GET", "/api/v1/platform/mailboxes/bulk/template", "", nil, "", nil)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 401/403 for unauthenticated template download, got %d", resp.StatusCode)
	}
}

// ── authorization ─────────────────────────────────────────────────

func TestBulkMailbox_Stage_TenantAdminDenied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.csv")
	fw.Write([]byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")))
	mw.Close()
	resp, raw := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", env.t1AdmToken, &buf, mw.FormDataContentType(),
		map[string]string{"Cookie": "csrf_token=" + env.t1AdmCSRF, "X-CSRF-Token": env.t1AdmCSRF, "Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tenant admin must be denied platform bulk stage, got %d: %s", resp.StatusCode, raw)
	}
}

func TestBulkMailbox_Stage_TenantOperatorDenied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.csv")
	fw.Write([]byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")))
	mw.Close()
	resp, raw := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", env.t1OpToken, &buf, mw.FormDataContentType(),
		map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tenant operator must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestBulkMailbox_Stage_Unauthenticated_Denied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.csv")
	fw.Write([]byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")))
	mw.Close()
	resp, _ := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", "", &buf, mw.FormDataContentType(), map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthenticated, got %d", resp.StatusCode)
	}
}

func TestBulkMailbox_Stage_MissingCSRF_Denied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "x.csv")
	fw.Write([]byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")))
	mw.Close()
	resp, raw := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", env.psaToken, &buf, mw.FormDataContentType(), map[string]string{"Idempotency-Key": "k1"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF must be denied, got %d: %s", resp.StatusCode, raw)
	}
}

func TestBulkMailbox_Stage_MissingIdempotencyKey_Denied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	resp, raw := env.psaStage(t, 1, "x.csv", []byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")), "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key must be 400, got %d: %s", resp.StatusCode, raw)
	}
}

// ── strict input / upload security ───────────────────────────────

func TestBulkMailbox_Stage_FormulaInjectionRejected(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	csv := "email,name\n=cmd|'/c calc'!A1@t1.example,Evil\n"
	resp, raw := env.psaStage(t, 1, "evil.csv", []byte(csv), "k-formula")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("formula-injection CSV must be rejected, got %d: %s", resp.StatusCode, raw)
	}
}

func TestBulkMailbox_Stage_UnknownHeaderRejected(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	csv := "email,not_a_real_column\nalice@t1.example,x\n"
	resp, raw := env.psaStage(t, 1, "bad.csv", []byte(csv), "k-unknown")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown header must be rejected, got %d: %s", resp.StatusCode, raw)
	}
}

func TestBulkMailbox_Validate_UnknownJSONFieldRejected(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	domainID := env.createDomain(t, 1, "t1.example")
	stResp, stRaw := env.psaStage(t, 1, "x.csv", []byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")), "k-stage-strict")
	if stResp.StatusCode != http.StatusCreated {
		t.Fatalf("stage: %d %s", stResp.StatusCode, stRaw)
	}
	var staged struct {
		StagingID  string `json:"staging_id"`
		SourceHash string `json:"source_hash"`
		Format     string `json:"format"`
	}
	json.Unmarshal(stRaw, &staged)
	resp, raw := env.doJSON(t, "POST", "/api/v1/platform/mailboxes/bulk/1/validate", env.psaToken, env.psaCSRF, map[string]any{
		"staging_id": staged.StagingID, "source_hash": staged.SourceHash, "format": staged.Format, "domain_id": domainID,
		"not_a_real_field": true,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field must be rejected, got %d: %s", resp.StatusCode, raw)
	}
}

// ── tenant isolation ─────────────────────────────────────────────

func TestBulkMailbox_Job_CrossTenantAccessDenied(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	domainID := env.createDomain(t, 1, "t1.example")
	stResp, stRaw := env.psaStage(t, 1, "x.csv", []byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example")), "k-stage-xten")
	if stResp.StatusCode != http.StatusCreated {
		t.Fatalf("stage: %d %s", stResp.StatusCode, stRaw)
	}
	var staged struct {
		StagingID  string `json:"staging_id"`
		SourceHash string `json:"source_hash"`
		Format     string `json:"format"`
	}
	json.Unmarshal(stRaw, &staged)
	cjResp, cjRaw := env.doJSON(t, "POST", "/api/v1/platform/mailboxes/bulk/1/jobs", env.psaToken, env.psaCSRF, map[string]any{
		"staging_id": staged.StagingID, "source_hash": staged.SourceHash, "format": staged.Format, "domain_id": domainID,
	}, map[string]string{"Idempotency-Key": "k-job-xten"})
	if cjResp.StatusCode != http.StatusCreated {
		t.Fatalf("create job: %d %s", cjResp.StatusCode, cjRaw)
	}
	var jobOut struct {
		Job struct {
			ID uint `json:"id"`
		} `json:"job"`
	}
	json.Unmarshal(cjRaw, &jobOut)

	// The SAME job ID requested under tenant 2's path must not be found —
	// it must never leak tenant 1's job to a tenant-2-scoped request,
	// even from the PSA (the path's explicit tenant_id must always gate
	// the lookup, never be bypassed by an ambient credential).
	resp, raw := env.do(t, "GET", fmt.Sprintf("/api/v1/platform/mailboxes/bulk/2/jobs/%d", jobOut.Job.ID), env.psaToken, nil, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant job lookup must be 404 (never leak existence), got %d: %s", resp.StatusCode, raw)
	}
	if strings.Contains(string(raw), "t1.example") || strings.Contains(string(raw), "alice@") {
		t.Fatalf("cross-tenant response must not reveal any foreign job content: %s", raw)
	}
}

// ── idempotency ───────────────────────────────────────────────────

func TestBulkMailbox_Stage_SameKeyReplay(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	csv := []byte(fmt.Sprintf(bulkTestCSV, "t1.example", "t1.example"))

	// Idempotency is keyed on the EXACT request bytes (kernel.RequestHash
	// of c.Body()), matching a real HTTP client retry that resends the
	// identical wire body — not a logically-equivalent-but-freshly-
	// reconstructed multipart body with a new random boundary. Build the
	// multipart body ONCE and resend those exact bytes twice.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "x.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(csv); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rawBody := buf.Bytes()
	contentType := mw.FormDataContentType()
	h := map[string]string{"Cookie": "csrf_token=" + env.psaCSRF, "X-CSRF-Token": env.psaCSRF, "Idempotency-Key": "replay-key-1"}

	resp1, raw1 := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", env.psaToken, bytes.NewReader(rawBody), contentType, h)
	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("first stage: %d %s", resp1.StatusCode, raw1)
	}
	resp2, raw2 := env.do(t, "POST", "/api/v1/platform/mailboxes/bulk/1/stage", env.psaToken, bytes.NewReader(rawBody), contentType, h)
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("replay stage: %d %s", resp2.StatusCode, raw2)
	}
	if resp2.Header.Get("X-Idempotency-Replay") != "true" {
		t.Fatal("replay must carry X-Idempotency-Replay: true")
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("replay body must equal the original:\n%s\nvs\n%s", raw1, raw2)
	}
}

// ── dry run / execute / redaction / end-to-end ──────────────────────

func TestBulkMailbox_EndToEnd_DryRunThenExecute(t *testing.T) {
	env := buildBulkMailboxEnv(t)
	domainID := env.createDomain(t, 1, "e2e.example")
	csv := []byte(fmt.Sprintf(bulkTestCSV, "e2e.example", "e2e.example"))

	stResp, stRaw := env.psaStage(t, 1, "e2e.csv", csv, "k-e2e-stage")
	if stResp.StatusCode != http.StatusCreated {
		t.Fatalf("stage: %d %s", stResp.StatusCode, stRaw)
	}
	var staged struct {
		StagingID  string `json:"staging_id"`
		SourceHash string `json:"source_hash"`
		Format     string `json:"format"`
	}
	json.Unmarshal(stRaw, &staged)

	// Dry run (validate): must create NOTHING.
	vResp, vRaw := env.doJSON(t, "POST", "/api/v1/platform/mailboxes/bulk/1/validate", env.psaToken, env.psaCSRF, map[string]any{
		"staging_id": staged.StagingID, "source_hash": staged.SourceHash, "format": staged.Format, "domain_id": domainID,
	}, nil)
	if vResp.StatusCode != http.StatusOK {
		t.Fatalf("validate: %d %s", vResp.StatusCode, vRaw)
	}
	countMailboxesForDomain := func() int {
		t.Helper()
		var n int
		if err := env.db.QueryRow(`SELECT COUNT(*) FROM mailboxes m JOIN coremail_domains d ON m.domain_id = d.id WHERE d.name = 'e2e.example'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	mboxCountAfterValidate := countMailboxesForDomain()
	if mboxCountAfterValidate != 0 {
		t.Fatalf("dry run (validate) must create zero mailboxes, found %d", mboxCountAfterValidate)
	}

	// Create the durable job — still zero mailbox mutation.
	cjResp, cjRaw := env.doJSON(t, "POST", "/api/v1/platform/mailboxes/bulk/1/jobs", env.psaToken, env.psaCSRF, map[string]any{
		"staging_id": staged.StagingID, "source_hash": staged.SourceHash, "format": staged.Format, "domain_id": domainID,
	}, map[string]string{"Idempotency-Key": "k-e2e-job"})
	if cjResp.StatusCode != http.StatusCreated {
		t.Fatalf("create job: %d %s", cjResp.StatusCode, cjRaw)
	}
	var jobOut struct {
		Job struct {
			ID uint `json:"id"`
		} `json:"job"`
	}
	json.Unmarshal(cjRaw, &jobOut)
	if mboxCountAfterCreateJob := countMailboxesForDomain(); mboxCountAfterCreateJob != 0 {
		t.Fatalf("job creation must create zero mailboxes, found %d", mboxCountAfterCreateJob)
	}

	// Execute: must return 202 with a durable job reference, and the
	// response must never leak secret material.
	exResp, exRaw := env.doJSON(t, "POST", fmt.Sprintf("/api/v1/platform/mailboxes/bulk/1/jobs/%d/execute", jobOut.Job.ID), env.psaToken, env.psaCSRF, nil,
		map[string]string{"Idempotency-Key": "k-e2e-exec"})
	if exResp.StatusCode != http.StatusAccepted {
		t.Fatalf("execute: %d %s", exResp.StatusCode, exRaw)
	}
	assertNoSecretLeak(t, exRaw)

	// The job/rows report must also never leak secret material.
	jResp, jRaw := env.do(t, "GET", fmt.Sprintf("/api/v1/platform/mailboxes/bulk/1/jobs/%d", jobOut.Job.ID), env.psaToken, nil, "", nil)
	if jResp.StatusCode != http.StatusOK {
		t.Fatalf("get job: %d %s", jResp.StatusCode, jRaw)
	}
	assertNoSecretLeak(t, jRaw)

	rResp, rRaw := env.do(t, "GET", fmt.Sprintf("/api/v1/platform/mailboxes/bulk/1/jobs/%d/rows", jobOut.Job.ID), env.psaToken, nil, "", nil)
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("get rows: %d %s", rResp.StatusCode, rRaw)
	}
	assertNoSecretLeak(t, rRaw)
}

func assertNoSecretLeak(t *testing.T, raw []byte) {
	t.Helper()
	lower := strings.ToLower(string(raw))
	forbidden := []string{
		"password", "password_hash", "setup_token", "reset_token",
		"staging_id", "/tmp/", "\\appdata\\", "lease_token", "fencing_token",
		"authorization: bearer", "set-cookie", "-----begin",
	}
	for _, f := range forbidden {
		if strings.Contains(lower, f) {
			t.Fatalf("response leaked forbidden term %q: %s", f, raw)
		}
	}
}
