package handlers_test

// Phase 3A response-secrecy acceptance for the relay endpoints.
//
// The acceptance boundary is what an operator can read in DevTools/F12: the
// response body, the headers, and the error envelope. Nothing on that surface
// may carry a credential, a secret reference, encryption material, an SMTP
// AUTH payload, a DSN, SQL text, a filesystem path, or a stack trace.
//
// This suite drives the REAL router and middleware chain, and asserts on the
// raw bytes of the response rather than on a decoded struct, so a leak through
// an unexpected field, an error envelope, or a header is still caught.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// forbiddenSubstrings are the tokens that must never appear in any relay
// response. They cover the credential itself, its stored forms, the shapes an
// SMTP AUTH payload takes, and the internal-detail markers that indicate a
// driver or runtime error has been serialised to the client.
func forbiddenSubstrings(password string) map[string]string {
	return map[string]string{
		password:            "the plaintext relay credential",
		"secret_ref":        "the secret-reference field name",
		"secretRef":         "the secret-reference field name",
		"SecretRef":         "the secret-reference field name",
		"AUTH PLAIN":        "an SMTP AUTH payload",
		"AUTH LOGIN":        "an SMTP AUTH payload",
		"postgres://":       "a database DSN",
		"postgresql://":     "a database DSN",
		"sslmode=":          "a database DSN",
		"SELECT ":           "raw SQL",
		"INSERT INTO":       "raw SQL",
		"UPDATE platform_":  "raw SQL",
		"SQLSTATE":          "a driver error code",
		"goroutine ":        "a stack trace",
		".go:":              "a source path and line",
		"C:\\\\":            "a filesystem path",
		"/usr/local/go":     "a filesystem path",
		"D:/":               "a filesystem path",
		"panic:":            "a runtime panic",
		"platform_relay_pr": "an internal table name",
	}
}

// assertNoSecrets fails if any forbidden token appears in the status line,
// headers, or body of a response.
func assertNoSecrets(t *testing.T, label string, resp *http.Response, body []byte, password string) {
	t.Helper()
	var sb strings.Builder
	sb.Write(body)
	sb.WriteByte('\n')
	for k, vals := range resp.Header {
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(strings.Join(vals, ","))
		sb.WriteByte('\n')
	}
	surface := sb.String()

	for token, why := range forbiddenSubstrings(password) {
		if token == "" {
			continue
		}
		if strings.Contains(surface, token) {
			t.Errorf("%s leaked %s (%q) on the F12-visible surface:\n%s", label, why, token, truncate(surface, 800))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// decodeRelayIdentity reads the relay id and version from a response whether
// the handler returns the relay at the top level or wrapped under "relay".
func decodeRelayIdentity(t *testing.T, body []byte) (uint, int) {
	t.Helper()
	var wrapped struct {
		Relay *struct {
			ID      uint `json:"id"`
			Version int  `json:"version"`
		} `json:"relay"`
		ID      uint `json:"id"`
		Version int  `json:"version"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		t.Fatalf("decode relay response: %v (body %s)", err, body)
	}
	if wrapped.Relay != nil && wrapped.Relay.ID != 0 {
		return wrapped.Relay.ID, wrapped.Relay.Version
	}
	return wrapped.ID, wrapped.Version
}

// relayNoCSRF issues a mutation WITHOUT the CSRF header/cookie pair so the
// rejection path can be inspected for leakage.
func (e *platformMailControlEnv) relayNoCSRF(t *testing.T, method, path, token string, body interface{}) (*http.Response, []byte) {
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
	req.Header.Set("Idempotency-Key", "secrecy-nocsrf-1")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s (no csrf): %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// TestPlatformRelay_ResponseSecrecyAcrossOutcomes walks every relay endpoint
// through success, validation failure, forbidden, CSRF failure, version
// conflict, and missing-resource outcomes, asserting the secrecy invariant on
// each response.
func TestPlatformRelay_ResponseSecrecyAcrossOutcomes(t *testing.T) {
	env := buildPlatformMailControlEnv(t)
	const password = "super-secret-pw"

	// ── Success: create ──────────────────────────────────────────────────
	resp, body := env.relayDo(t, http.MethodPost, relayBase, env.psaToken, "", "secrecy-create-1",
		relayPayload("secrecy-relay", "smtp.secrecy.example.com"))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create relay: status %d body %s", resp.StatusCode, body)
	}
	assertNoSecrets(t, "POST /platform/relays (success)", resp, body, password)

	relayID, relayVersion := decodeRelayIdentity(t, body)
	if relayID == 0 {
		t.Fatalf("expected a real relay id, got body %s", body)
	}
	_ = relayVersion
	idStr := strconv.FormatUint(uint64(relayID), 10)

	// ── Success: get and list (the redaction path) ───────────────────────
	resp, body = env.relayDo(t, http.MethodGet, relayBase+"/"+idStr, env.psaToken, "", "", nil)
	assertNoSecrets(t, "GET /platform/relays/:id", resp, body, password)

	resp, body = env.relayDo(t, http.MethodGet, relayBase, env.psaToken, "", "", nil)
	assertNoSecrets(t, "GET /platform/relays", resp, body, password)

	// ── Validation failure ───────────────────────────────────────────────
	resp, body = env.relayDo(t, http.MethodPost, relayBase, env.psaToken, "", "secrecy-invalid-1",
		map[string]interface{}{"name": "", "host": "", "port": 0})
	assertNoSecrets(t, "POST /platform/relays (validation failure)", resp, body, password)

	// ── Unsafe target (typed refusal) ────────────────────────────────────
	resp, body = env.relayDo(t, http.MethodPost, relayBase, env.psaToken, "", "secrecy-unsafe-1",
		relayPayload("secrecy-unsafe", "127.0.0.1"))
	assertNoSecrets(t, "POST /platform/relays (unsafe target)", resp, body, password)

	// ── Missing resource ─────────────────────────────────────────────────
	resp, body = env.relayDo(t, http.MethodGet, relayBase+"/99999999", env.psaToken, "", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Logf("GET missing relay returned %d (body %s)", resp.StatusCode, body)
	}
	assertNoSecrets(t, "GET /platform/relays/:id (missing)", resp, body, password)

	// ── Version conflict ─────────────────────────────────────────────────
	resp, body = env.relayDo(t, http.MethodPatch, relayBase+"/"+idStr, env.psaToken, "", "secrecy-conflict-1",
		map[string]interface{}{"version": 9999, "priority": 42})
	assertNoSecrets(t, "PATCH /platform/relays/:id (version conflict)", resp, body, password)

	// ── Forbidden (a tenant role must not reach a platform endpoint) ─────
	if env.tenantAdm != "" {
		resp, body = env.relayDo(t, http.MethodGet, relayBase, env.tenantAdm, "", "", nil)
		if resp.StatusCode == http.StatusOK {
			t.Error("a tenant role must not read the platform relay list")
		}
		assertNoSecrets(t, "GET /platform/relays (forbidden)", resp, body, password)
	}

	// ── CSRF failure ─────────────────────────────────────────────────────
	resp, body = env.relayNoCSRF(t, http.MethodPost, relayBase, env.psaToken,
		relayPayload("secrecy-csrf", "smtp.csrf.example.com"))
	if resp.StatusCode == http.StatusCreated {
		t.Error("a mutation without CSRF must be refused")
	}
	assertNoSecrets(t, "POST /platform/relays (CSRF failure)", resp, body, password)

	// ── Connection test against an unreachable host ──────────────────────
	resp, body = env.relayDo(t, http.MethodPost, relayBase+"/"+idStr+"/test", env.psaToken, "", "secrecy-test-1", nil)
	assertNoSecrets(t, "POST /platform/relays/:id/test", resp, body, password)

	// ── Delete ───────────────────────────────────────────────────────────
	resp, body = env.relayDo(t, http.MethodDelete, relayBase+"/"+idStr, env.psaToken,
		"DELETE-RELAY-"+idStr, "secrecy-delete-1", nil)
	assertNoSecrets(t, "DELETE /platform/relays/:id", resp, body, password)
}

// TestPlatformRelay_GeneratedCredentialShownExactlyOnce pins the one
// intentional exception to the secrecy rule, and its boundaries.
func TestPlatformRelay_GeneratedCredentialShownExactlyOnce(t *testing.T) {
	env := buildPlatformMailControlEnv(t)

	resp, body := env.relayDo(t, http.MethodPost, relayBase, env.psaToken, "", "rotate-secrecy-create",
		relayPayload("rotate-secrecy", "smtp.rotate.example.com"))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	relayID, relayVersion := decodeRelayIdentity(t, body)
	idStr := strconv.FormatUint(uint64(relayID), 10)

	// Rotate with no supplied password: the service generates one and returns
	// it EXACTLY once.
	resp, body = env.relayDo(t, http.MethodPost, relayBase+"/"+idStr+"/rotate-credentials",
		env.psaToken, "ROTATE-RELAY-"+idStr, "rotate-secrecy-1",
		map[string]interface{}{"version": relayVersion})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate: %d %s", resp.StatusCode, body)
	}
	var rotated struct {
		GeneratedPassword string `json:"generated_password"`
		ShowOnce          bool   `json:"show_once"`
	}
	if err := json.Unmarshal(body, &rotated); err != nil {
		t.Fatalf("decode rotate: %v", err)
	}
	if rotated.GeneratedPassword == "" {
		t.Fatalf("a generated rotation must return the credential once, got %s", body)
	}
	if !rotated.ShowOnce {
		t.Error("the one-time credential must be flagged show_once")
	}
	generated := rotated.GeneratedPassword

	// The response carrying a one-time secret must not be cached.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("a one-time credential response must set Cache-Control: no-store, got %q", cc)
	}

	// REPLAY: the same idempotency key must NOT re-expose the credential.
	resp, body = env.relayDo(t, http.MethodPost, relayBase+"/"+idStr+"/rotate-credentials",
		env.psaToken, "ROTATE-RELAY-"+idStr, "rotate-secrecy-1",
		map[string]interface{}{"version": relayVersion})
	if strings.Contains(string(body), generated) {
		t.Errorf("an idempotent replay must never re-expose the generated credential, got %s", body)
	}

	// SUBSEQUENT READS must not contain it anywhere.
	for _, path := range []string{relayBase + "/" + idStr, relayBase} {
		resp, body = env.relayDo(t, http.MethodGet, path, env.psaToken, "", "", nil)
		if strings.Contains(string(body), generated) {
			t.Errorf("GET %s exposed the generated credential: %s", path, body)
		}
		assertNoSecrets(t, "GET "+path+" (after rotation)", resp, body, generated)
	}

	// A FAILED rotation (bad version) must not return any credential.
	resp, body = env.relayDo(t, http.MethodPost, relayBase+"/"+idStr+"/rotate-credentials",
		env.psaToken, "ROTATE-RELAY-"+idStr, "rotate-secrecy-conflict",
		map[string]interface{}{"version": 9999})
	if resp.StatusCode == http.StatusOK {
		t.Error("a rotation with a stale version must not succeed")
	}
	var failed struct {
		GeneratedPassword string `json:"generated_password"`
	}
	_ = json.Unmarshal(body, &failed)
	if failed.GeneratedPassword != "" {
		t.Errorf("a failed rotation must never return a credential, got %s", body)
	}
	assertNoSecrets(t, "POST rotate-credentials (version conflict)", resp, body, generated)
}
