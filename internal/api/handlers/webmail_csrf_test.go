package handlers_test

// H-1 regression suite: webmail mutation surface CSRF protection.
//
// Before this fix every state-changing /api/v1/webmail/* route was mounted on
// the bare `protected` group with NO CSRF middleware, while the session
// cookies were issued SameSite=None — so any attacker page could drive a
// victim's mailbox (delete/archive/move/send/forwarding) with an ordinary
// cross-site request. See ORVIX_FINAL_SECURITY_AUDIT_REPORT H-1.
//
// These tests drive the REAL router, the REAL CSRF middleware and the REAL
// content-type guard. Nothing is stubbed or bypassed.

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// webmailMutationRoutes is the concrete mutation surface. Paths use real ids
// where the handler needs one; the CSRF/content-type rejection must happen
// BEFORE any handler logic, so a non-existent id is fine for negative tests.
var webmailMutationRoutes = []struct {
	method string
	path   string
	body   string
}{
	{"PATCH", "/api/v1/webmail/messages/1", `{"seen":true}`},
	{"POST", "/api/v1/webmail/messages/1/archive", ""},
	{"POST", "/api/v1/webmail/messages/1/delete", ""},
	{"POST", "/api/v1/webmail/messages/1/move", `{"target_folder_id":1}`},
	{"POST", "/api/v1/webmail/messages/batch", `{"action":"archive","ids":[1]}`},
	{"POST", "/api/v1/webmail/folders/1/read-all", ""},
	{"POST", "/api/v1/webmail/send", `{"to":"x@example.com","subject":"s","body":"b"}`},
	{"POST", "/api/v1/webmail/drafts", `{"subject":"s"}`},
	{"PUT", "/api/v1/webmail/drafts/1", `{"subject":"s"}`},
	{"DELETE", "/api/v1/webmail/drafts/1", ""},
	{"POST", "/api/v1/webmail/push/subscribe", `{"endpoint":"https://fcm.googleapis.com/x"}`},
	{"POST", "/api/v1/webmail/push/unsubscribe", `{"endpoint":"https://fcm.googleapis.com/x"}`},
	{"POST", "/api/v1/webmail/push/test", ""},
	{"PUT", "/api/v1/webmail/settings", `{"display_name":"x"}`},
	{"POST", "/api/v1/webmail/rules", `{"name":"r"}`},
	{"PUT", "/api/v1/webmail/rules/1", `{"name":"r"}`},
	{"DELETE", "/api/v1/webmail/rules/1", ""},
	{"PUT", "/api/v1/webmail/vacation", `{"enabled":false}`},
	{"PUT", "/api/v1/webmail/forwarding", `{"enabled":false}`},
	{"POST", "/api/v1/webmail/logout", ""},
	{"POST", "/api/v1/webmail/password/change", `{"current_password":"a","new_password":"b"}`},
}

// TestWebmailCSRF_EveryMutationRejectsMissingToken is the core route-matrix
// regression: a cookie-authenticated request with NO CSRF material must be
// refused on every single mutation route.
func TestWebmailCSRF_EveryMutationRejectsMissingToken(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)

	for _, rt := range webmailMutationRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			var body io.Reader
			if rt.body != "" {
				body = bytes.NewReader([]byte(rt.body))
			}
			req := httptest.NewRequest(rt.method, rt.path, body)
			req.Header.Set("Cookie", "access_token="+tok)
			if rt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 403 {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s %s: expected 403 without a CSRF token, got %d: %s",
					rt.method, rt.path, resp.StatusCode, string(b))
			}
		})
	}
}

// TestWebmailCSRF_CrossSiteFormPostCannotTriggerBodyOptionalMutation is the
// decisive proof required for H-1: a plain cross-site HTML <form> submission
// — the exact shape that needs no CORS preflight and no JavaScript — must not
// be able to drive a body-optional mutation such as delete/archive.
//
// The victim's session cookie is deliberately attached here, i.e. the test
// assumes the WORST case where SameSite did not stop the cookie. Protection
// must still hold on the strength of CSRF + content-type alone.
func TestWebmailCSRF_CrossSiteFormPostCannotTriggerBodyOptionalMutation(t *testing.T) {
	e := buildWebmailTestEnv(t)
	mailboxID := mustMailboxIDForTest(t, e, e.email)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)
	id := e.injectMessage(t, "Do not delete me", "body")

	// A genuine multipart/form-data body, exactly as a browser would encode
	// <form enctype="multipart/form-data">. Built properly so the request
	// reaches the server and is rejected by the guard, rather than failing
	// in the transport's multipart parser.
	var multipartBody bytes.Buffer
	mw := multipart.NewWriter(&multipartBody)
	_ = mw.WriteField("dummy", "1")
	mw.Close()

	// Exactly what <form action=".../delete" method="POST"> produces.
	for _, form := range []struct {
		contentType string
		body        string
	}{
		{"application/x-www-form-urlencoded", "dummy=1"},
		{mw.FormDataContentType(), multipartBody.String()},
		{"text/plain", "dummy=1"},
	} {
		formCT := form.contentType
		t.Run(formCT, func(t *testing.T) {
			req := httptest.NewRequest("POST",
				fmt.Sprintf("/api/v1/webmail/messages/%d/delete", id),
				strings.NewReader(form.body))
			req.Header.Set("Content-Type", formCT)
			req.Header.Set("Origin", "https://attacker.example")
			req.Header.Set("Cookie", "access_token="+tok)
			resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 403 && resp.StatusCode != 415 {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("cross-site form POST (%s) was NOT rejected: got %d: %s",
					formCT, resp.StatusCode, string(b))
			}
		})
	}

	// And prove the message really is still there — the mutation did not run.
	status, list := e.webmailRequest(t, "GET",
		"/api/v1/webmail/messages?folder=INBOX&limit=200", tok, nil)
	if status != 200 {
		t.Fatalf("list inbox: %d", status)
	}
	msgs, _ := list["messages"].([]interface{})
	found := false
	for _, m := range msgs {
		mm, _ := m.(map[string]interface{})
		if mm != nil && mm["subject"] == "Do not delete me" {
			found = true
		}
	}
	if !found {
		t.Fatal("cross-site form POST actually deleted the message — CSRF defence failed")
	}
}

// TestWebmailCSRF_TextPlainJSONSmugglingRejected pins the specific Fiber
// behaviour the audit flagged: Bind().JSON() parses the body regardless of
// Content-Type, so a `text/plain` CORS "simple request" carrying a JSON body
// would otherwise reach the handler with no preflight.
func TestWebmailCSRF_TextPlainJSONSmugglingRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)

	req := httptest.NewRequest("PUT", "/api/v1/webmail/forwarding",
		bytes.NewReader([]byte(`{"enabled":true,"forward_to":"attacker@evil.example"}`)))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Cookie", "access_token="+tok)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 && resp.StatusCode != 415 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("text/plain JSON smuggling was NOT rejected: got %d: %s", resp.StatusCode, string(b))
	}
}

// TestWebmailCSRF_MismatchedTokenRejected proves the double-submit comparison
// is real: a syntactically valid token in the header that does not equal the
// cookie must fail.
func TestWebmailCSRF_MismatchedTokenRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)
	good := mintWebmailCSRF(t, e.router, tok)
	other := mintWebmailCSRF(t, e.router, tok)
	if good == other {
		t.Fatal("expected two distinct CSRF tokens")
	}

	req := httptest.NewRequest("PUT", "/api/v1/webmail/settings",
		bytes.NewReader([]byte(`{"display_name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "access_token="+tok+"; csrf_token="+good)
	req.Header.Set("X-CSRF-Token", other) // mismatch
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 on CSRF cookie/header mismatch, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestWebmailCSRF_ForgedTokenRejected proves the server-side token record is
// consulted — an attacker cannot simply set matching cookie and header values
// of their own choosing.
func TestWebmailCSRF_ForgedTokenRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)

	forged := "totally-made-up-token-value"
	req := httptest.NewRequest("PUT", "/api/v1/webmail/settings",
		bytes.NewReader([]byte(`{"display_name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "access_token="+tok+"; csrf_token="+forged)
	req.Header.Set("X-CSRF-Token", forged)
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 403 for a forged (unissued) CSRF token, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestWebmailCSRF_ValidTokenSucceeds is the positive control: the exact same
// request shape the real SPA sends must still work.
func TestWebmailCSRF_ValidTokenSucceeds(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)

	status, body := e.webmailRequest(t, "PUT", "/api/v1/webmail/settings", tok,
		map[string]interface{}{"display_name": "Valid CSRF"})
	if status != 200 {
		t.Fatalf("expected 200 with valid CSRF material, got %d: %v", status, body)
	}
}

// TestWebmailCSRF_ReadOnlyGETsUnaffected pins that the fix did not start
// demanding tokens on safe methods.
func TestWebmailCSRF_ReadOnlyGETsUnaffected(t *testing.T) {
	e := buildWebmailTestEnv(t)
	tok := e.loginAdmin(t)

	for _, path := range []string{
		"/api/v1/webmail/session",
		"/api/v1/webmail/folders",
		"/api/v1/webmail/settings",
		"/api/v1/webmail/rules",
		"/api/v1/webmail/vacation",
		"/api/v1/webmail/forwarding",
	} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Cookie", "access_token="+tok)
		resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: expected 200 without any CSRF token, got %d", path, resp.StatusCode)
		}
	}
}

// TestWebmailCSRF_UnauthenticatedStillRejected proves CSRF was layered on top
// of authentication, not in place of it: no session means 401, and the CSRF
// layer must not accidentally turn that into a 403 that leaks route existence.
func TestWebmailCSRF_UnauthenticatedStillRejected(t *testing.T) {
	e := buildWebmailTestEnv(t)

	req := httptest.NewRequest("PUT", "/api/v1/webmail/settings",
		bytes.NewReader([]byte(`{"display_name":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 401 for an unauthenticated mutation, got %d: %s", resp.StatusCode, string(b))
	}
}

// TestWebmailCSRF_SessionCookiesAreNotSameSiteNone pins the cookie decision.
// SameSite is evaluated against the registrable domain, so admin.<parent> and
// webmail.<parent> remain same-site under Lax — None was never needed for SSO
// and was the enabling condition for the whole H-1 attack.
func TestWebmailCSRF_SessionCookiesAreNotSameSiteNone(t *testing.T) {
	e := buildWebmailTestEnv(t)
	_ = e.loginAdmin(t)

	req := httptest.NewRequest("POST", "/api/v1/auth/login",
		bytes.NewReader([]byte(fmt.Sprintf(`{"username":%q,"password":%q}`, e.email, e.password))))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()

	sawSession := false
	for _, raw := range resp.Header.Values("Set-Cookie") {
		lower := strings.ToLower(raw)
		if !strings.HasPrefix(lower, "access_token=") && !strings.HasPrefix(lower, "refresh_token=") {
			continue
		}
		sawSession = true
		if strings.Contains(lower, "samesite=none") {
			t.Fatalf("session cookie must not be SameSite=None: %s", raw)
		}
		if !strings.Contains(lower, "secure") {
			t.Fatalf("session cookie must remain Secure: %s", raw)
		}
	}
	if !sawSession {
		t.Fatal("no session cookie observed on login response")
	}
}
