package handlers_test

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// settingsGet reads the caller's settings row. Returns (status, parsed JSON body).
func settingsGet(t *testing.T, e *webmailTestEnv, tok string) (int, map[string]interface{}) {
	t.Helper()
	return e.webmailRequest(t, "GET", "/api/v1/webmail/settings", tok, nil)
}

// settingsPut sends a partial settings patch. Returns (status, parsed JSON body).
func settingsPut(t *testing.T, e *webmailTestEnv, tok string, body map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	return e.webmailRequest(t, "PUT", "/api/v1/webmail/settings", tok, body)
}

// provisionSecondUser inserts a second user with a real bcrypt password
// and an active coremail_mailboxes row so loginAs can return a token that
// resolves to a different mailbox than the admin's. This is the only
// way to exercise per-mailbox settings isolation end-to-end through the
// real auth + handler stack.
func provisionSecondUser(t *testing.T, e *webmailTestEnv, email, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	now := time.Now().UTC()
	if _, err := e.mailbox.DB.Exec(
		"INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		now, now, email, string(hash), "user", 1, 1, 1,
	); err != nil {
		t.Fatalf("insert second users row: %v", err)
	}
	if _, err := e.mailbox.DB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, strings.SplitN(email, "@", 2)[0], email, "Second User", "x-bcrypt-placeholder", "bcrypt", "active", 1024, 0, now, now,
	); err != nil {
		t.Fatalf("insert second mailbox: %v", err)
	}
}

// mustSecondMailboxID returns the coremail_mailboxes.id for the supplied email.
func mustSecondMailboxID(t *testing.T, e *webmailTestEnv, email string) uint {
	t.Helper()
	row := e.mailbox.DB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email = ?", email)
	var id uint
	if err := row.Scan(&id); err != nil {
		t.Fatalf("scan mailbox id: %v", err)
	}
	return id
}

// ────────────────────────────────────────────────────────────
// Settings tests
// ────────────────────────────────────────────────────────────

func TestWebmailSettingsDefaultsForFreshUser(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	status, resp := settingsGet(t, e, tok)
	if status != 200 {
		t.Fatalf("GET /webmail/settings: expected 200, got %d: %v", status, resp)
	}
	if resp["mailbox_id"] == nil || resp["mailbox_id"].(float64) == 0 {
		t.Fatalf("settings missing mailbox_id: %v", resp)
	}
	// Defaults pin a small set of values the UI relies on.
	if got := resp["theme"]; got != "dark" {
		t.Errorf("theme default = %v, want dark", got)
	}
	if got := resp["density"]; got != "comfortable" {
		t.Errorf("density default = %v, want comfortable", got)
	}
	if got := resp["text_direction"]; got != "auto" {
		t.Errorf("text_direction default = %v, want auto", got)
	}
	if got := resp["language"]; got != "en" {
		t.Errorf("language default = %v, want en", got)
	}
	if got := resp["default_folder"]; got != "INBOX" {
		t.Errorf("default_folder default = %v, want INBOX", got)
	}
	if got := resp["signature_enabled"]; got != false {
		t.Errorf("signature_enabled default = %v, want false", got)
	}
	if got := resp["confirm_before_discard"]; got != true {
		t.Errorf("confirm_before_discard default = %v, want true", got)
	}
	if got := resp["preview_lines"]; got != float64(2) {
		t.Errorf("preview_lines default = %v, want 2", got)
	}
}

func TestWebmailSettingsUpdatePersists(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	patch := map[string]interface{}{
		"theme":             "light",
		"density":           "compact",
		"text_direction":    "rtl",
		"language":          "ar",
		"signature_enabled": true,
		"signature_text":    "--\nAhmed\nOrvix Engineering",
		"preview_lines":     4,
		"autosave_seconds":  10,
		"default_folder":    "INBOX",
	}
	status, resp := settingsPut(t, e, tok, patch)
	if status != 200 {
		t.Fatalf("PUT /webmail/settings: expected 200, got %d: %v", status, resp)
	}
	if resp["theme"] != "light" {
		t.Errorf("after PUT theme = %v, want light", resp["theme"])
	}
	if resp["density"] != "compact" {
		t.Errorf("after PUT density = %v, want compact", resp["density"])
	}
	if resp["text_direction"] != "rtl" {
		t.Errorf("after PUT text_direction = %v, want rtl", resp["text_direction"])
	}
	if resp["signature_text"] != "--\nAhmed\nOrvix Engineering" {
		t.Errorf("after PUT signature_text = %v, want the sent value", resp["signature_text"])
	}
	if resp["preview_lines"] != float64(4) {
		t.Errorf("after PUT preview_lines = %v, want 4", resp["preview_lines"])
	}
	if resp["autosave_seconds"] != float64(10) {
		t.Errorf("after PUT autosave_seconds = %v, want 10", resp["autosave_seconds"])
	}

	// Re-read: changes must persist.
	status2, resp2 := settingsGet(t, e, tok)
	if status2 != 200 {
		t.Fatalf("GET /webmail/settings: expected 200, got %d", status2)
	}
	if resp2["theme"] != "light" {
		t.Errorf("after re-GET theme = %v, want light", resp2["theme"])
	}
	if resp2["density"] != "compact" {
		t.Errorf("after re-GET density = %v, want compact", resp2["density"])
	}
	if resp2["text_direction"] != "rtl" {
		t.Errorf("after re-GET text_direction = %v, want rtl", resp2["text_direction"])
	}
}

func TestWebmailSettingsRejectsInvalidEnum(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	bad := []map[string]interface{}{
		{"theme": "neon"},
		{"density": "huge"},
		{"text_direction": "diagonal"},
		{"language": "klingon"},
		{"default_reply_mode": "broadcast"},
		{"sender_display": "raw_bits"},
		{"reading_pane": "everywhere"},
		{"date_format": "galactic"},
		{"time_format": "swatch"},
	}
	for _, b := range bad {
		status, _ := settingsPut(t, e, tok, b)
		if status != 400 {
			t.Errorf("expected 400 for %v, got %d", b, status)
		}
	}

	// Numeric out-of-range also rejected.
	badNums := []map[string]interface{}{
		{"preview_lines": -1},
		{"preview_lines": 999},
		{"autosave_seconds": 9999},
		{"mark_read_delay_seconds": -5},
	}
	for _, b := range badNums {
		status, _ := settingsPut(t, e, tok, b)
		if status != 400 {
			t.Errorf("expected 400 for %v, got %d", b, status)
		}
	}

	// Unknown field rejected.
	status, _ := settingsPut(t, e, tok, map[string]interface{}{"thme": "light"})
	if status != 400 {
		t.Errorf("expected 400 for unknown field, got %d", status)
	}
}

func TestWebmailSettingsRejectsWrongTypes(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	bad := []map[string]interface{}{
		{"theme": 42},
		{"preview_lines": "two"},
		{"signature_enabled": "yes"},
		{"autosave_seconds": true},
	}
	for _, b := range bad {
		status, _ := settingsPut(t, e, tok, b)
		if status != 400 {
			t.Errorf("expected 400 for %v, got %d", b, status)
		}
	}
}

func TestWebmailSettingsIsolatedPerMailbox(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tokA := e.loginAdmin(t)

	// User A flips theme to light.
	status, _ := settingsPut(t, e, tokA, map[string]interface{}{"theme": "light"})
	if status != 200 {
		t.Fatalf("PUT theme=light: expected 200, got %d", status)
	}

	// Provision a real second user (users row + coremail_mailboxes row) so
	// loginAs returns a token that maps to a different mailbox.
	provisionSecondUser(t, e, "bob@orvix.email", "BobPass!1")
	mailboxBID := mustSecondMailboxID(t, e, "bob@orvix.email")
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mailboxBID, nil); err != nil {
		t.Fatalf("ensure system folders B: %v", err)
	}
	tokB := loginAs(t, e, "bob@orvix.email", "BobPass!1")

	// User B's theme must still be the default ("dark").
	status, resp := settingsGet(t, e, tokB)
	if status != 200 {
		t.Fatalf("GET as B: expected 200, got %d: %v", status, resp)
	}
	if resp["theme"] != "dark" {
		t.Errorf("B theme = %v, want default dark (isolation failed)", resp["theme"])
	}

	// B flips to compact; A's theme must remain light.
	status, _ = settingsPut(t, e, tokB, map[string]interface{}{"density": "compact"})
	if status != 200 {
		t.Fatalf("PUT density=compact as B: expected 200, got %d", status)
	}
	status, respA := settingsGet(t, e, tokA)
	if status != 200 {
		t.Fatalf("GET as A: expected 200, got %d", status)
	}
	if respA["theme"] != "light" {
		t.Errorf("A theme after B edit = %v, want light", respA["theme"])
	}
	if respA["density"] != "comfortable" {
		t.Errorf("A density after B edit = %v, want default comfortable", respA["density"])
	}
	status, respB := settingsGet(t, e, tokB)
	if status != 200 {
		t.Fatalf("GET as B: expected 200, got %d", status)
	}
	if respB["density"] != "compact" {
		t.Errorf("B density = %v, want compact", respB["density"])
	}
	if respB["theme"] != "dark" {
		t.Errorf("B theme = %v, want default dark", respB["theme"])
	}
}

func TestWebmailSettingsRequiresAuth(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}

	status, _ := settingsGet(t, e, "")
	if status != 401 && status != 403 {
		t.Errorf("GET without auth: expected 401 or 403, got %d", status)
	}
	status, _ = settingsPut(t, e, "", map[string]interface{}{"theme": "light"})
	if status != 401 && status != 403 {
		t.Errorf("PUT without auth: expected 401 or 403, got %d", status)
	}
}

func TestWebmailSettingsPartialPatchDoesNotTouchOtherFields(t *testing.T) {
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	// Set initial values.
	settingsPut(t, e, tok, map[string]interface{}{
		"theme":          "light",
		"density":        "compact",
		"signature_text": "first",
	})

	// Patch only signature_text — the other fields must remain unchanged.
	status, resp := settingsPut(t, e, tok, map[string]interface{}{
		"signature_text": "second",
	})
	if status != 200 {
		t.Fatalf("PUT partial: expected 200, got %d: %v", status, resp)
	}
	if resp["theme"] != "light" {
		t.Errorf("theme drifted after partial PUT: %v", resp["theme"])
	}
	if resp["density"] != "compact" {
		t.Errorf("density drifted after partial PUT: %v", resp["density"])
	}
	if resp["signature_text"] != "second" {
		t.Errorf("signature_text = %v, want second", resp["signature_text"])
	}
}

func TestWebmailSettingsSignatureMultilineArabicPreserved(t *testing.T) {
	// UTF-8 / Arabic / multiline signature text must round-trip without
	// encoding damage (Storage layer is responsible for clamping length;
	// the handler must preserve byte-exact content within the cap).
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	sig := "-- \nأحمد منصور\nمدير هندسة المنتجات\nشركة أورفكس"
	status, resp := settingsPut(t, e, tok, map[string]interface{}{
		"signature_text":    sig,
		"signature_enabled": true,
		"text_direction":    "rtl",
	})
	if status != 200 {
		t.Fatalf("PUT signature: expected 200, got %d: %v", status, resp)
	}
	if got, _ := resp["signature_text"].(string); got != sig {
		t.Errorf("signature round-trip mismatch:\n got %q\nwant %q", got, sig)
	}
	// Re-read via GET.
	status, resp2 := settingsGet(t, e, tok)
	if status != 200 {
		t.Fatalf("GET: expected 200, got %d", status)
	}
	if got, _ := resp2["signature_text"].(string); got != sig {
		t.Errorf("signature GET mismatch:\n got %q\nwant %q", got, sig)
	}
	if resp2["text_direction"] != "rtl" {
		t.Errorf("text_direction = %v, want rtl", resp2["text_direction"])
	}
	// Sanity: the Arabic string really is non-ASCII so a broken decoder
	// would show up as mojibake or as a length 0 / wrong-length value.
	if !strings.Contains(sig, "أحمد") {
		t.Fatal("test bug: signature must contain Arabic to be meaningful")
	}
}

func TestWebmailSettingsDoesNotExposeSensitiveFields(t *testing.T) {
	// The settings row has no secrets; this test pins that contract.
	// If a future patch accidentally adds e.g. an oauth_token column,
	// the response must not leak it.
	e := buildWebmailTestEnv(t)
	if err := e.mailbox.Folders.EnsureSystemFolders(t.Context(), mustMailboxIDForTest(t, e, e.email), nil); err != nil {
		t.Fatalf("ensure system folders: %v", err)
	}
	tok := e.loginAdmin(t)

	status, resp := settingsGet(t, e, tok)
	if status != 200 {
		t.Fatalf("GET: expected 200, got %d", status)
	}
	forbidden := []string{
		"password", "password_hash", "token", "secret",
		"oauth_token", "refresh_token", "access_token",
		"session_id", "session_secret",
	}
	for _, f := range forbidden {
		if _, ok := resp[f]; ok {
			t.Errorf("settings response leaked %q field: %v", f, resp[f])
		}
	}
}
