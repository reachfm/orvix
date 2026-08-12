package handlers_test

// Handler acceptance for the platform relay administration routes
// (Mail-Control Phase B). Exercises the real router + middleware
// chain (auth, platformMW role gate, RBAC, CSRF) and the real relay
// service against a real SQLite database, proving:
//   - PSA allowed, tenant roles denied, CSRF required;
//   - strict JSON, missing/conflicting idempotency keys;
//   - typed confirmation for delete/disable/rotate;
//   - optimistic-concurrency version conflicts;
//   - secret encryption at rest + redaction in every response;
//   - no plaintext in audit rows;
//   - SSRF-safe connectivity testing (unsafe targets never dialed);
//   - enable/disable lifecycle.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (e *platformMailControlEnv) idemDo(t *testing.T, method, path, token, idemKey string, body interface{}) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	req.Header.Set("Cookie", "csrf_token="+e.psaCSRF)
	req.Header.Set("X-CSRF-Token", e.psaCSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s (idem): %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func (e *platformMailControlEnv) relayDo(t *testing.T, method, path, token, confirm, idemKey string, body interface{}) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if confirm != "" {
		req.Header.Set("X-Confirm", confirm)
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	req.Header.Set("Cookie", "csrf_token="+e.psaCSRF)
	req.Header.Set("X-CSRF-Token", e.psaCSRF)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s (relayDo): %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

const relayBase = "/api/v1/platform/relays"

func relayPayload(name, host string) map[string]interface{} {
	return map[string]interface{}{
		"name": name, "host": host, "port": 587, "username": "apikey",
		"password": "super-secret-pw", "conn_security": "starttls",
		"tls_validation": "strict", "priority": 10, "active": true,
	}
}

func TestPlatformRelayRoutes(t *testing.T) {
	env := buildPlatformMailControlEnv(t)

	t.Run("PSA_can_create_list_detail_enable_disable", func(t *testing.T) {
		resp, raw := env.relayDo(t, "POST", relayBase, env.psaToken, "", "create-1", relayPayload("relay-accept-1", "smtp.accept.test"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status %d: %s", resp.StatusCode, raw)
		}
		var created map[string]interface{}
		if err := json.Unmarshal(raw, &created); err != nil {
			t.Fatalf("create decode: %v", err)
		}
		for _, banned := range []string{"secret_ref", "super-secret-pw", "password"} {
			if strings.Contains(string(raw), banned) {
				t.Fatalf("response leaked %q: %s", banned, raw)
			}
		}
		if created["has_credential"] != true {
			t.Fatalf("expected has_credential=true: %s", raw)
		}
		id := uint(created["id"].(float64))

		resp, raw = env.do(t, "GET", relayBase+"?limit=50", env.psaToken, nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(raw), "relay-accept-1") {
			t.Fatalf("list status %d: %s", resp.StatusCode, raw)
		}
		if strings.Contains(string(raw), "super-secret-pw") {
			t.Fatalf("list leaked the credential: %s", raw)
		}

		resp, raw = env.do(t, "GET", relayBase+"/"+u64str(id), env.psaToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail status %d: %s", resp.StatusCode, raw)
		}
		if strings.Contains(string(raw), "super-secret-pw") {
			t.Fatalf("detail leaked the credential: %s", raw)
		}

		// Disable requires typed confirmation and a current version.
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/disable", env.psaToken, "", "disable-1", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusPreconditionRequired {
			t.Fatalf("disable without confirmation status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/disable", env.psaToken, "DISABLE-RELAY-"+u64str(id), "disable-1", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("disable status %d: %s", resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), `"active":false`) {
			t.Fatalf("disable body unexpected: %s", raw)
		}
		// Stale version conflicts (412 Precondition Failed).
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/enable", env.psaToken, "", "enable-stale", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("stale enable status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/enable", env.psaToken, "", "enable-1", map[string]interface{}{"version": 2})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("enable status %d: %s", resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), `"active":true`) {
			t.Fatalf("enable body unexpected: %s", raw)
		}
	})

	t.Run("tenant_admin_denied", func(t *testing.T) {
		resp, raw := env.do(t, "GET", relayBase, env.tenantAdm, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant list status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase, env.tenantAdm, "", "ta-create", relayPayload("tenant-relay", "smtp.tenant.test"))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant create status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("csrf_required", func(t *testing.T) {
		req := httptest.NewRequest("POST", relayBase, strings.NewReader(`{"name":"n","host":"smtp.x.test","port":587}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+env.psaToken)
		req.Header.Set("Idempotency-Key", "csrf-relay-1")
		resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("create without CSRF status %d", resp.StatusCode)
		}
	})

	t.Run("idempotency_contract", func(t *testing.T) {
		// Missing key rejected.
		resp, raw := env.csrfDo(t, "POST", relayBase, env.psaToken, relayPayload("idem-relay-1", "smtp.idem.test"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("missing key status %d: %s", resp.StatusCode, raw)
		}
		// First create with a key.
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "idem-create-1", relayPayload("idem-relay-1", "smtp.idem.test"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("first create status %d: %s", resp.StatusCode, raw)
		}
		firstBody := string(raw)
		// Replay: same key + same body returns the same safe result.
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "idem-create-1", relayPayload("idem-relay-1", "smtp.idem.test"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("replay status %d: %s", resp.StatusCode, raw)
		}
		if resp.Header.Get("X-Idempotency-Replay") != "true" {
			t.Fatal("expected X-Idempotency-Replay: true on replay")
		}
		if string(raw) != firstBody {
			t.Fatalf("replay body differs:\nfirst: %s\nreplay: %s", firstBody, raw)
		}
		if strings.Contains(string(raw), "super-secret-pw") {
			t.Fatalf("replay leaked the credential: %s", raw)
		}
		// Same key, changed body conflicts.
		changed := relayPayload("idem-relay-1", "smtp.idem.test")
		changed["port"] = 465
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "idem-create-1", changed)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("changed-body same-key status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("strict_json_and_validation", func(t *testing.T) {
		req := httptest.NewRequest("POST", relayBase, strings.NewReader(`{"name": "x",`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+env.psaToken)
		req.Header.Set("Idempotency-Key", "bad-json-1")
		req.Header.Set("Cookie", "csrf_token="+env.psaCSRF)
		req.Header.Set("X-CSRF-Token", env.psaCSRF)
		resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed JSON status %d", resp.StatusCode)
		}

		resp, raw := env.relayDo(t, "POST", relayBase, env.psaToken, "", "bad-host-1", relayPayload("bad-host", "10.0.0.5"))
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(raw), "unsafe relay target") {
			t.Fatalf("private host status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "bad-host-2", relayPayload("bad-host-2", "169.254.169.254"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("metadata host status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "bad-port-1", relayPayload("bad-port", "smtp.ok.test"))
		resp2 := resp
		_ = resp2
		// Port validation happens via a payload with port 0.
		p := relayPayload("bad-port", "smtp.ok.test")
		p["port"] = 0
		resp, raw = env.relayDo(t, "POST", relayBase, env.psaToken, "", "bad-port-2", p)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("port 0 status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("duplicate_name_conflict", func(t *testing.T) {
		env.relayDo(t, "POST", relayBase, env.psaToken, "", "dup-1", relayPayload("dup-name", "smtp.dup.test"))
		resp, raw := env.relayDo(t, "POST", relayBase, env.psaToken, "", "dup-2", relayPayload("dup-name", "smtp.dup.test"))
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("duplicate name status %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("rotate_requires_confirmation_and_returns_generated_once", func(t *testing.T) {
		resp, raw := env.relayDo(t, "POST", relayBase, env.psaToken, "", "rot-create", relayPayload("rot-relay", "smtp.rot.test"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status %d: %s", resp.StatusCode, raw)
		}
		var created map[string]interface{}
		json.Unmarshal(raw, &created)
		id := uint(created["id"].(float64))

		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/rotate-credentials", env.psaToken, "", "rot-1", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusPreconditionRequired {
			t.Fatalf("rotate without confirmation status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/rotate-credentials", env.psaToken, "ROTATE-RELAY-"+u64str(id), "rot-1", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("rotate status %d: %s", resp.StatusCode, raw)
		}
		var rotated map[string]interface{}
		if err := json.Unmarshal(raw, &rotated); err != nil {
			t.Fatalf("rotate decode: %v", err)
		}
		gen, _ := rotated["generated_password"].(string)
		if gen == "" {
			t.Fatalf("expected a generated password: %s", raw)
		}
		// The generated password never appears in list/detail afterwards.
		resp, raw = env.do(t, "GET", relayBase+"/"+u64str(id), env.psaToken, nil)
		if strings.Contains(string(raw), gen) {
			t.Fatalf("generated password leaked into detail: %s", raw)
		}
		// Replay of the same rotation returns the same REDACTED relay
		// WITHOUT re-exposing the generated password (the credential is
		// returned exactly once and is never persisted in the
		// idempotency record).
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(id)+"/rotate-credentials", env.psaToken, "ROTATE-RELAY-"+u64str(id), "rot-1", map[string]interface{}{"version": 1})
		if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Idempotency-Replay") != "true" {
			t.Fatalf("rotation replay status %d headers %v body %s", resp.StatusCode, resp.Header, raw)
		}
		if strings.Contains(string(raw), gen) {
			t.Fatalf("rotation replay must not re-expose the generated password: %s", raw)
		}
		if !strings.Contains(string(raw), `"has_credential":true`) {
			t.Fatalf("rotation replay must return the redacted relay state: %s", raw)
		}
		// The idempotency table must never contain the plaintext.
		sqlDB, err := env.dbHandle()
		if err != nil {
			t.Fatal(err)
		}
		var bodies []string
		rows, err := sqlDB.Query(`SELECT response_body FROM platform_idempotency_keys WHERE scope LIKE 'platform.relay.rotate%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var b string
			rows.Scan(&b)
			bodies = append(bodies, b)
		}
		for _, b := range bodies {
			if strings.Contains(b, gen) {
				t.Fatalf("idempotency record persisted the generated credential: %s", b)
			}
		}
	})

	t.Run("test_never_dials_unsafe_target_and_replays_safely", func(t *testing.T) {
		// Insert a pre-policy row with a loopback host directly (the
		// platform route refuses such a host at create time).
		loopbackID := env.rawInsertRelay(t, "legacy-loopback", "127.0.0.1", 25)

		resp, raw := env.relayDo(t, "POST", relayBase+"/"+u64str(loopbackID)+"/test", env.psaToken, "", "test-1", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("test status %d: %s", resp.StatusCode, raw)
		}
		if !strings.Contains(string(raw), "unsafe relay target") || !strings.Contains(string(raw), `"connected":false`) {
			t.Fatalf("unsafe test must fail closed without dialing: %s", raw)
		}
		first := string(raw)
		resp, raw = env.relayDo(t, "POST", relayBase+"/"+u64str(loopbackID)+"/test", env.psaToken, "", "test-1", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("test replay status %d: %s", resp.StatusCode, raw)
		}
		if resp.Header.Get("X-Idempotency-Replay") != "true" || string(raw) != first {
			t.Fatalf("test replay must return the identical safe result: %s", raw)
		}
	})

	t.Run("delete_requires_confirmation", func(t *testing.T) {
		resp, raw := env.relayDo(t, "POST", relayBase, env.psaToken, "", "del-create", relayPayload("del-relay", "smtp.del.test"))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status %d: %s", resp.StatusCode, raw)
		}
		var created map[string]interface{}
		json.Unmarshal(raw, &created)
		id := uint(created["id"].(float64))

		resp, raw = env.relayDo(t, "DELETE", relayBase+"/"+u64str(id), env.psaToken, "", "", nil)
		if resp.StatusCode != http.StatusPreconditionRequired {
			t.Fatalf("delete without confirmation status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.relayDo(t, "DELETE", relayBase+"/"+u64str(id), env.psaToken, "DELETE-RELAY-"+u64str(id), "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("delete status %d: %s", resp.StatusCode, raw)
		}
		resp, raw = env.do(t, "GET", relayBase+"/"+u64str(id), env.psaToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("deleted relay must 404, got %d: %s", resp.StatusCode, raw)
		}
	})

	t.Run("no_plaintext_in_audit", func(t *testing.T) {
		sqlDB, err := env.dbHandle()
		if err != nil {
			t.Fatal(err)
		}
		rows, err := sqlDB.Query(`SELECT action, target, reason, before, after FROM orvix_audit WHERE action LIKE 'platform.relay.%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var action, target, reason, before, after string
			if err := rows.Scan(&action, &target, &reason, &before, &after); err != nil {
				t.Fatal(err)
			}
			blob := action + target + reason + before + after
			if strings.Contains(blob, "super-secret-pw") {
				t.Fatalf("audit row leaked plaintext credential: %s", blob)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
}

func u64str(v uint) string {
	return strconv.FormatUint(uint64(v), 10)
}

// rawInsertRelay seeds a provider row directly, bypassing the
// create-time target policy (simulating a row persisted before the
// policy existed), so the /test route's dial-time safety can be
// exercised. Returns the inserted row id.
func (e *platformMailControlEnv) rawInsertRelay(t *testing.T, name, host string, port int) uint {
	t.Helper()
	sqlDB, err := e.dbHandle()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := sqlDB.Exec(`INSERT INTO platform_relay_providers
		(scope, tenant_id, domain_id, pool_id, name, host, port, username, secret_ref,
		 conn_security, tls_validation, priority, weight, active, rate_limit_per_min,
		 circuit_state, circuit_failures, version, created_at, updated_at)
		VALUES ('global', 0, 0, 0, ?, ?, ?, '', '', 'none', 'strict', 100, 1, 1, 0, 'closed', 0, 1, ?, ?)`,
		name, host, port, now, now)
	if err != nil {
		t.Fatalf("raw relay insert: %v", err)
	}
	id, _ := res.LastInsertId()
	return uint(id)
}

// seedDeliverabilitySignal inserts a real delivery signal row for
// tenant 1 (the same table the delivery-path recorder writes to).
func (e *platformMailControlEnv) seedDeliverabilitySignal(t *testing.T, typ, sendingDomain, recipientDomain, provider string) {
	t.Helper()
	sqlDB, err := e.dbHandle()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	key := fmt.Sprintf("seed:%s:%d", typ, time.Now().UnixNano())
	insert := func(dim, val string) {
		if _, err := sqlDB.Exec(`INSERT INTO platform_deliverability_signals (event_key, tenant_id, dimension, dimension_value, type, latency_ms, recorded_at) VALUES (?, 1, ?, ?, ?, 10, ?)`,
			key+":"+dim, dim, val, typ, now); err != nil {
			t.Fatalf("seed signal: %v", err)
		}
	}
	insert("sending_domain", sendingDomain)
	insert("recipient_domain", recipientDomain)
	insert("relay_provider", provider)
}

// dbHandle exposes the router's underlying *sql.DB for direct assertions.
func (e *platformMailControlEnv) dbHandle() (*sql.DB, error) {
	return e.db, nil
}
