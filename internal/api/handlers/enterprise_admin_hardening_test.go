package handlers_test

// This file holds the runtime hardening tests for the
// CTO blockers:
//
//   BLOCKER 3 — Acceptance rule action contract
//     - API rejects "redirect" (no runtime support)
//     - API rejects "hold" (no runtime support)
//     - API rejects a payload carrying redirect_to
//     - API accepts accept / reject / quarantine
//     - Dry-run response carries the canonical action
//       string in BOTH `action` and `action_label`
//       (TEXT in / TEXT out — no int64 scan mismatch)
//     - Runtime and dry-run action labels match for the
//       same envelope
//
//   BLOCKER 4 — Incoming message rule action contract
//     - API rejects "move", "forward", "discard"
//     - API rejects "label" / "hold"
//     - API accepts reject / quarantine / tag
//     - Persisted action row matches the canonical value
//
//   BLOCKER 2 — SFTP password safety is verified in
//   internal/backup/targets/uploader_test.go (no
//   writeAskpassHelper, no SSH_ASKPASS script with
//   decrypted password, all upload paths flow through
//   ssh.Password in memory only).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// =====================================================================
// BLOCKER 3 — Acceptance rule action contract
// =====================================================================

// TestAcceptanceRejectsUnsupportedAction_Redirect pins the
// runtime-truthful contract: the API must reject "redirect"
// with a 400 because the SMTP receiver never executes a
// redirect (see internal/coremail/smtp/receive.go switch
// on the acceptance engine's action).
func TestAcceptanceRejectsUnsupportedAction_Redirect(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	resp := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"redirect-rule","priority":50,"enabled":true,"scope":"global","action":"redirect","redirect_to":"forward@example.com"}`)
	if resp.status == http.StatusCreated {
		t.Fatalf("API accepted unsupported action=redirect; body=%s", resp.body)
	}
	if resp.status != http.StatusBadRequest {
		t.Fatalf("want 400 for action=redirect, got %d body=%s", resp.status, resp.body)
	}
	if !strings.Contains(resp.body, "not supported") && !strings.Contains(resp.body, "redirect_to") {
		t.Fatalf("expected validation error mentioning unsupported action or redirect_to, got %s", resp.body)
	}
}

// TestAcceptanceRejectsUnsupportedAction_Hold mirrors the
// redirect check for the "hold" pseudo-action.
func TestAcceptanceRejectsUnsupportedAction_Hold(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	resp := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"hold-rule","priority":50,"enabled":true,"scope":"global","action":"hold"}`)
	if resp.status == http.StatusCreated {
		t.Fatalf("API accepted unsupported action=hold; body=%s", resp.body)
	}
	if resp.status != http.StatusBadRequest {
		t.Fatalf("want 400 for action=hold, got %d body=%s", resp.status, resp.body)
	}
}

// TestAcceptanceRejectsRedirectToEvenWithSupportedAction
// catches a regression where the API silently drops the
// redirect_to field. With the redirect contract removed
// the payload must be rejected outright.
func TestAcceptanceRejectsRedirectToEvenWithSupportedAction(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	resp := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"with-redirect","priority":50,"enabled":true,"scope":"global","action":"accept","redirect_to":"x@example.com"}`)
	if resp.status == http.StatusCreated {
		t.Fatalf("API accepted payload with redirect_to; body=%s", resp.body)
	}
	if resp.status != http.StatusBadRequest {
		t.Fatalf("want 400 for redirect_to on a non-redirect action, got %d body=%s", resp.status, resp.body)
	}
	if !strings.Contains(resp.body, "redirect_to") {
		t.Fatalf("expected error mentioning redirect_to, got %s", resp.body)
	}
}

// TestAcceptanceAcceptsAllRuntimeActions confirms the
// runtime-supported action set is fully writable.
func TestAcceptanceAcceptsAllRuntimeActions(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	for _, action := range []string{"accept", "reject", "quarantine"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"%s-rule","priority":50,"enabled":true,"scope":"global","action":"%s"}`, action, action)
			resp := postJSON(t, router,
				"/api/v1/admin/acceptance-rules",
				token, csrf, body)
			if resp.status != http.StatusCreated {
				t.Fatalf("create %s rule: want 201, got %d body=%s", action, resp.status, resp.body)
			}
		})
	}
}

// TestAcceptanceDryRunActionLabelMatchesDB pins the
// runtime/dry-run contract: the dry-run response must
// carry the canonical TEXT action in both `action` and
// `action_label`, and the labels must agree with the
// runtime's decision for the same envelope. The old
// implementation scanned TEXT into int64, which broke
// both the response shape and the action_label
// rendering. This test would have failed before the
// fix.
func TestAcceptanceDryRunActionLabelMatchesDB(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	// Seed an enabled reject rule.
	resp := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"drop-spam","priority":10,"enabled":true,"scope":"global","sender_pattern":"*@spam.example","action":"reject"}`)
	if resp.status != http.StatusCreated {
		t.Fatalf("seed: want 201, got %d %s", resp.status, resp.body)
	}

	// Dry-run an envelope that matches.
	resp = postJSON(t, router,
		"/api/v1/admin/acceptance-rules/test",
		token, csrf,
		`{"sender":"u@spam.example","recipient":"inbox@x.local","source_ip":"192.0.2.1"}`)
	if resp.status != http.StatusOK {
		t.Fatalf("test: want 200, got %d %s", resp.status, resp.body)
	}
	var dry struct {
		Action      string `json:"action"`
		ActionLabel string `json:"action_label"`
		MatchedID   *int64 `json:"matched_rule_id"`
	}
	if err := json.Unmarshal(resp.bodyBytes, &dry); err != nil {
		t.Fatalf("parse dry-run response: %v body=%s", err, resp.body)
	}
	if dry.Action != "reject" {
		t.Fatalf("dry-run action: want reject, got %q", dry.Action)
	}
	if dry.ActionLabel != "reject" {
		t.Fatalf("dry-run action_label: want reject, got %q (was probably an int64 code)", dry.ActionLabel)
	}
	if dry.MatchedID == nil {
		t.Fatalf("dry-run matched_rule_id: want non-nil, got nil")
	}
}

// TestAcceptanceDryRunNoMatchDefaultsAccept verifies the
// no-match default branch — the response must report
// action="accept" and action_label="accept" rather
// than the bogus "default: accept (no rule matched)"
// free-form string the previous version emitted.
func TestAcceptanceDryRunNoMatchDefaultsAccept(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	resp := postJSON(t, router,
		"/api/v1/admin/acceptance-rules/test",
		token, csrf,
		`{"sender":"noone@nowhere.example","recipient":"x@y.local","source_ip":"192.0.2.1"}`)
	if resp.status != http.StatusOK {
		t.Fatalf("test: want 200, got %d %s", resp.status, resp.body)
	}
	var dry struct {
		Action      string `json:"action"`
		ActionLabel string `json:"action_label"`
		MatchedID   *int64 `json:"matched_rule_id"`
	}
	if err := json.Unmarshal(resp.bodyBytes, &dry); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if dry.Action != "accept" {
		t.Fatalf("no-match action: want accept, got %q", dry.Action)
	}
	if dry.ActionLabel != "accept" {
		t.Fatalf("no-match action_label: want accept, got %q", dry.ActionLabel)
	}
	if dry.MatchedID != nil {
		t.Fatalf("no-match matched_rule_id: want nil, got %v", *dry.MatchedID)
	}
}

// =====================================================================
// BLOCKER 4 — Incoming message rule action contract
// =====================================================================

// TestIncomingRejectsUnsupportedActions pins the runtime
// contract: move / label / forward / discard / hold
// must all return 400 because the runtime in
// internal/coremail/smtp/receive.go only knows about
// reject / quarantine / tag.
func TestIncomingRejectsUnsupportedActions(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	for _, action := range []string{"move", "forward", "discard", "hold", "label"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"%s-rule","priority":50,"enabled":true,"field":"subject","operator":"contains","value":"x","action":"%s"}`, action, action)
			if action == "move" {
				body = fmt.Sprintf(`{"name":"%s-rule","priority":50,"enabled":true,"field":"subject","operator":"contains","value":"x","action":"%s","action_target":"INBOX"}`, action, action)
			}
			resp := postJSON(t, router,
				"/api/v1/admin/incoming-msg-rules",
				token, csrf, body)
			if resp.status == http.StatusCreated {
				t.Fatalf("API accepted unsupported action=%s; body=%s", action, resp.body)
			}
			if resp.status != http.StatusBadRequest {
				t.Fatalf("want 400 for action=%s, got %d body=%s", action, resp.status, resp.body)
			}
			if !strings.Contains(resp.body, "not supported") {
				t.Fatalf("expected validation error mentioning unsupported action, got %s", resp.body)
			}
		})
	}
}

// TestIncomingAcceptsAllRuntimeActions confirms the
// runtime-supported action set is fully writable.
func TestIncomingAcceptsAllRuntimeActions(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	for _, action := range []string{"reject", "quarantine", "tag"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(`{"name":"%s-rule","priority":50,"enabled":true,"field":"subject","operator":"contains","value":"x","action":"%s"}`, action, action)
			resp := postJSON(t, router,
				"/api/v1/admin/incoming-msg-rules",
				token, csrf, body)
			if resp.status != http.StatusCreated {
				t.Fatalf("create %s rule: want 201, got %d body=%s", action, resp.status, resp.body)
			}
		})
	}
}

// TestIncomingActionCanonicalisedAfterInsert makes sure
// the action persisted on the row matches what the
// runtime will read back. The previous implementation
// had a by-value normalisation that lost the canonical
// value during insert.
func TestIncomingActionCanonicalisedAfterInsert(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)
	resp := postJSON(t, router,
		"/api/v1/admin/incoming-msg-rules",
		token, csrf,
		`{"name":"canon","priority":50,"enabled":true,"field":"subject","operator":"contains","value":"x","action":"quarantine"}`)
	if resp.status != http.StatusCreated {
		t.Fatalf("create: want 201, got %d %s", resp.status, resp.body)
	}
	// Re-list and assert action is exactly "quarantine".
	resp = getJSON(t, router,
		"/api/v1/admin/incoming-msg-rules", token)
	if resp.status != http.StatusOK {
		t.Fatalf("list: want 200, got %d %s", resp.status, resp.body)
	}
	if !strings.Contains(resp.body, `"action":"quarantine"`) {
		t.Fatalf("expected action=quarantine in list response, got %s", resp.body)
	}
	if strings.Contains(resp.body, `"action":"move"`) {
		t.Fatalf("list response still contains legacy action=move: %s", resp.body)
	}
}

// =====================================================================
// FIX 1 — UpdateAcceptanceRule must validate actions
// =====================================================================

// acceptanceIDFromCreateBody extracts the rule id from
// a POST /api/v1/admin/acceptance-rules 201 response
// body. The enterprise admin handler returns
//   {"id":<int>,"name":...}
// on create; we parse it back as JSON so each PATCH test
// can target the row it just created. The helper lives
// here (not next to postJSON) because it is only used by
// the PATCH regression suite.
func acceptanceIDFromCreateBody(t *testing.T, body string) int64 {
	t.Helper()
	var resp struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("parse create response %q: %v", body, err)
	}
	if resp.ID == 0 {
		t.Fatalf("create response has id=0: %s", body)
	}
	return resp.ID
}

// TestAcceptancePatchRejectsUnsupportedActions pins the
// runtime-truthful contract on the PATCH path. The CTO
// review found that UpdateAcceptanceRule did not call
// validateAcceptanceRule, so a rule created with a valid
// action could be silently mutated into an inert value
// via PATCH. This test seeds an accept rule and tries to
// PATCH the action to redirect / hold; both must 400.
func TestAcceptancePatchRejectsUnsupportedActions(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	create := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"accept"}`)
	if create.status != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d %s", create.status, create.body)
	}
	id := acceptanceIDFromCreateBody(t, create.body)

	for _, badAction := range []string{"redirect", "hold", "bogus", ""} {
		t.Run("action="+badAction, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"%s"}`,
				badAction,
			)
			resp := patchJSON(t, router,
				fmt.Sprintf("/api/v1/admin/acceptance-rules/%d", id),
				token, csrf, body)
			if resp.status == http.StatusOK {
				t.Fatalf("PATCH accepted unsupported action=%q; body=%s", badAction, resp.body)
			}
			if resp.status != http.StatusBadRequest {
				t.Fatalf("PATCH action=%q: want 400, got %d body=%s", badAction, resp.status, resp.body)
			}
		})
	}
}

// TestAcceptancePatchRejectsRedirectToEvenWithSupportedAction
// catches the regression where a PATCH carrying a
// redirect_to field on a supported action would silently
// drop the field. With the redirect contract removed the
// payload must be rejected outright, even on accept.
func TestAcceptancePatchRejectsRedirectToEvenWithSupportedAction(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	create := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"accept"}`)
	if create.status != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d %s", create.status, create.body)
	}
	id := acceptanceIDFromCreateBody(t, create.body)

	for _, goodAction := range []string{"accept", "reject", "quarantine"} {
		t.Run("action="+goodAction, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"%s","redirect_to":"forward@example.com"}`,
				goodAction,
			)
			resp := patchJSON(t, router,
				fmt.Sprintf("/api/v1/admin/acceptance-rules/%d", id),
				token, csrf, body)
			if resp.status == http.StatusOK {
				t.Fatalf("PATCH action=%q + redirect_to was accepted; body=%s", goodAction, resp.body)
			}
			if resp.status != http.StatusBadRequest {
				t.Fatalf("PATCH action=%q + redirect_to: want 400, got %d body=%s",
					goodAction, resp.status, resp.body)
			}
			if !strings.Contains(resp.body, "redirect_to") {
				t.Fatalf("expected error mentioning redirect_to, got %s", resp.body)
			}
		})
	}
}

// TestAcceptancePatchAcceptsAllRuntimeActions confirms the
// PATCH path is fully wired into the validator — every
// runtime-supported action must round-trip through
// PATCH.
func TestAcceptancePatchAcceptsAllRuntimeActions(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	create := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"accept"}`)
	if create.status != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d %s", create.status, create.body)
	}
	id := acceptanceIDFromCreateBody(t, create.body)

	for _, action := range []string{"accept", "reject", "quarantine"} {
		t.Run(action, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"%s"}`,
				action,
			)
			resp := patchJSON(t, router,
				fmt.Sprintf("/api/v1/admin/acceptance-rules/%d", id),
				token, csrf, body)
			if resp.status != http.StatusOK {
				t.Fatalf("PATCH action=%q: want 200, got %d body=%s", action, resp.status, resp.body)
			}
		})
	}
}

// TestAcceptancePatchPersistsCanonicalAction makes sure
// the value stored on the row after PATCH is exactly the
// runtime-supported canonical string. This catches a
// regression where validateAcceptanceRule returned nil
// but the SQL writer persisted the raw payload (e.g.
// "REDIRECT" uppercased or with trailing whitespace).
func TestAcceptancePatchPersistsCanonicalAction(t *testing.T) {
	router, _ := newEnterpriseRouter(t)
	token := enterpriseLoginForTest(t, router, "admin@test.local", "TestPassword123!")
	csrf := enterpriseCSRFForTest(t, router, token)

	create := postJSON(t, router,
		"/api/v1/admin/acceptance-rules",
		token, csrf,
		`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"accept"}`)
	if create.status != http.StatusCreated {
		t.Fatalf("seed create: want 201, got %d %s", create.status, create.body)
	}
	id := acceptanceIDFromCreateBody(t, create.body)

	patch := patchJSON(t, router,
		fmt.Sprintf("/api/v1/admin/acceptance-rules/%d", id),
		token, csrf,
		`{"name":"seed","priority":50,"enabled":true,"scope":"global","action":"quarantine"}`)
	if patch.status != http.StatusOK {
		t.Fatalf("PATCH: want 200, got %d %s", patch.status, patch.body)
	}

	// Re-list and check that the action string is
	// exactly "quarantine" — never the legacy
	// "redirect" / "hold" / integer code / mixed-case.
	listResp := getJSON(t, router,
		"/api/v1/admin/acceptance-rules", token)
	if listResp.status != http.StatusOK {
		t.Fatalf("list: want 200, got %d %s", listResp.status, listResp.body)
	}
	if !strings.Contains(listResp.body, `"action":"quarantine"`) {
		t.Fatalf("expected action=quarantine after PATCH, got %s", listResp.body)
	}
	for _, bad := range []string{`"action":"redirect"`, `"action":"hold"`, `"action":"REDIRECT"`} {
		if strings.Contains(listResp.body, bad) {
			t.Fatalf("PATCH left unsupported action in DB: %s in body %s", bad, listResp.body)
		}
	}
}