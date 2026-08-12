package handlers_test

// Mail-Control Phase B acceptance for the queue attribution + action
// surface. Exercises the real router/middleware stack against the
// real queue engine, proving:
//   - PSA access, tenant denial, CSRF enforcement;
//   - typed confirmation for bounce/cancel;
//   - state-aware actions (retry only for supported states);
//   - safe real attribution (tenant/domain/relay host/suppression/
//     policy categories) with redacted internals;
//   - strict JSON for bulk actions;
//   - stable error codes for invalid transitions.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func queuePhaseBRequest(t *testing.T, e *queueTestEnv, method, path, body, token, csrf, confirm string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("Cookie", "csrf_token="+csrf)
		req.Header.Set("X-CSRF-Token", csrf)
	}
	if confirm != "" {
		req.Header.Set("X-Confirm", confirm)
	}
	resp, err := e.router.App().Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, data
}

func TestPlatformQueuePhaseBAcceptance(t *testing.T) {
	e := buildQueueTestEnv(t)

	t.Run("psa_access_and_tenant_denial", func(t *testing.T) {
		resp, body := queuePhaseBRequest(t, e, "GET", "/api/v1/admin/queue/messages", "", e.adminToken, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("psa list: %d %s", resp.StatusCode, body)
		}
		resp, _ = queuePhaseBRequest(t, e, "GET", "/api/v1/admin/queue/messages", "", e.userToken, "", "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant list must be 403, got %d", resp.StatusCode)
		}
		resp, _ = queuePhaseBRequest(t, e, "POST", "/api/v1/admin/queue/messages/1/retry", "", e.userToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("tenant retry must be 403, got %d", resp.StatusCode)
		}
	})

	t.Run("csrf_required_for_actions", func(t *testing.T) {
		id := seedQueueEntry(t, e, "pending", "csrf@test.com", "csrf-to@test.com")
		resp, _ := queuePhaseBRequest(t, e, "POST",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10)+"/cancel",
			"", e.adminToken, "", "CANCEL-QUEUE-"+strconv.FormatUint(uint64(id), 10))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cancel without csrf must be 403, got %d", resp.StatusCode)
		}
	})

	t.Run("typed_confirmation_for_bounce_and_cancel", func(t *testing.T) {
		id := seedQueueEntry(t, e, "pending", "confirm@test.com", "confirm-to@test.com")
		base := "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id), 10)
		resp, _ := queuePhaseBRequest(t, e, "POST", base+"/bounce", `{"reason":"x"}`, e.adminToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusPreconditionRequired {
			t.Fatalf("bounce without confirmation must be 428, got %d", resp.StatusCode)
		}
		resp, _ = queuePhaseBRequest(t, e, "POST", base+"/bounce", `{"reason":"x"}`, e.adminToken, e.csrfToken, "BOUNCE-QUEUE-"+strconv.FormatUint(uint64(id), 10))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("bounce with confirmation must be 200, got %d", resp.StatusCode)
		}
		id2 := seedQueueEntry(t, e, "pending", "confirm2@test.com", "confirm2-to@test.com")
		base2 := "/api/v1/admin/queue/messages/" + strconv.FormatUint(uint64(id2), 10)
		resp, _ = queuePhaseBRequest(t, e, "POST", base2+"/cancel", "", e.adminToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusPreconditionRequired {
			t.Fatalf("cancel without confirmation must be 428, got %d", resp.StatusCode)
		}
		resp, _ = queuePhaseBRequest(t, e, "POST", base2+"/cancel", "", e.adminToken, e.csrfToken, "CANCEL-QUEUE-"+strconv.FormatUint(uint64(id2), 10))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cancel with confirmation must be 200, got %d", resp.StatusCode)
		}
	})

	t.Run("state_aware_actions", func(t *testing.T) {
		// Retry from deferred succeeds.
		def := seedQueueEntry(t, e, "deferred", "state1@test.com", "state1-to@test.com")
		resp, body := queuePhaseBRequest(t, e, "POST",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(def), 10)+"/retry", "", e.adminToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("retry deferred: %d %s", resp.StatusCode, body)
		}
		// Retry from delivered is rejected with the stable
		// invalid_state_transition code (delivered/cancelled terminal
		// messages never offer invalid retry).
		del := seedQueueEntry(t, e, "delivered", "state2@test.com", "state2-to@test.com")
		resp, body = queuePhaseBRequest(t, e, "POST",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(del), 10)+"/retry", "", e.adminToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("retry delivered must be 400, got %d %s", resp.StatusCode, body)
		}
		var errBody struct {
			Code string `json:"code"`
		}
		json.Unmarshal(body, &errBody)
		if errBody.Code != "invalid_state_transition" {
			t.Fatalf("expected invalid_state_transition code, got %q", errBody.Code)
		}
		// The delivered row is untouched.
		var status string
		e.sqlDB.QueryRow("SELECT status FROM coremail_queue WHERE id = ?", del).Scan(&status)
		if status != "delivered" {
			t.Fatalf("delivered row mutated to %s", status)
		}
		// Cancel of a delivered terminal message is rejected by the
		// state machine (after confirmation).
		resp, body = queuePhaseBRequest(t, e, "POST",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(del), 10)+"/cancel", "", e.adminToken, e.csrfToken,
			"CANCEL-QUEUE-"+strconv.FormatUint(uint64(del), 10))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("cancel delivered must be 400, got %d %s", resp.StatusCode, body)
		}
	})

	t.Run("strict_json_for_bulk_action", func(t *testing.T) {
		resp, _ := queuePhaseBRequest(t, e, "POST", "/api/v1/admin/queue/messages/bulk-action", `{"ids": [`, e.adminToken, e.csrfToken, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("malformed bulk JSON must be 400, got %d", resp.StatusCode)
		}
	})

	t.Run("safe_attribution_and_redaction", func(t *testing.T) {
		// Seed entries with real attribution evidence: tenant, domain,
		// relay host, suppression rejection, policy denial, bounce.
		now := time.Now().UTC()
		seed := func(tenantID, domainID uint, status, lastError, remoteHost string) uint {
			res, err := e.sqlDB.Exec(`INSERT INTO coremail_queue
				(tenant_id, domain_id, message_id, from_address, to_address, recipient_domain,
				 direction, status, priority, attempt_count, max_attempts, last_error, remote_host,
				 delivery_mode, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, 'remote.test', 'outbound', ?, 0, 1, 16, ?, ?, 'remote_smtp', ?, ?)`,
				tenantID, domainID, "msg-"+strings.ReplaceAll(status+lastError, " ", "-"), "sender@x.test", "to@remote.test",
				status, lastError, remoteHost, now, now)
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			qid, _ := res.LastInsertId()
			return uint(qid)
		}
		supID := seed(2, 5, "bounced", "recipient is suppressed", "")
		polID := seed(2, 5, "bounced", "domain outbound limit of 1000/hr reached", "")
		relayID := seed(2, 5, "deferred", "smtp error 451: try later", "smtp.provider-x.test")

		for _, id := range []uint{supID, polID, relayID} {
			resp, body := queuePhaseBRequest(t, e, "GET",
				"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10), "", e.adminToken, "", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("detail %d: %d %s", id, resp.StatusCode, body)
			}
			blob := string(body)
			// Safe attribution present.
			for _, field := range []string{`"tenant_id":2`, `"domain_id":5`, `"retryable"`, `"failure_category"`} {
				if !strings.Contains(blob, field) {
					t.Fatalf("detail %d missing %s: %s", id, field, blob)
				}
			}
			// Internals never exposed.
			for _, banned := range []string{"lease_owner", "lease_expires_at", "raw_message", `"body"`, "secret", "password", "message_id"} {
				if strings.Contains(blob, `"`+banned+`"`) {
					t.Fatalf("detail %d leaked %q: %s", id, banned, blob)
				}
			}
		}
		// Relay/provider attribution is present where the evidence
		// exists (remote_host on the relay-failed entry).
		resp, body := queuePhaseBRequest(t, e, "GET",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(relayID), 10), "", e.adminToken, "", "")
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"remote_host":"smtp.provider-x.test"`) {
			t.Fatalf("relay attribution missing: %d %s", resp.StatusCode, body)
		}
		// Category attribution matches the real evidence.
		checkCategory := func(id uint, want string) {
			resp, body := queuePhaseBRequest(t, e, "GET",
				"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(id), 10), "", e.adminToken, "", "")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("detail %d: %d", id, resp.StatusCode)
			}
			var dto struct {
				Message struct {
					FailureCategory string `json:"failure_category"`
				} `json:"message"`
			}
			json.Unmarshal(body, &dto)
			if dto.Message.FailureCategory != want {
				t.Fatalf("entry %d: expected failure_category=%q, got %q", id, want, dto.Message.FailureCategory)
			}
		}
		checkCategory(supID, "suppressed")
		checkCategory(polID, "policy_denied")
		checkCategory(relayID, "other")

		// Retryable reflects the state machine: deferred is retryable,
		// delivered is not.
		resp, body = queuePhaseBRequest(t, e, "GET",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(relayID), 10), "", e.adminToken, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail: %d", resp.StatusCode)
		}
		var dto struct {
			Message struct {
				Retryable bool `json:"retryable"`
			} `json:"message"`
		}
		json.Unmarshal(body, &dto)
		if !dto.Message.Retryable {
			t.Fatal("deferred entry must be retryable")
		}
		del := seed(3, 9, "delivered", "", "mx.remote.test")
		resp, body = queuePhaseBRequest(t, e, "GET",
			"/api/v1/admin/queue/messages/"+strconv.FormatUint(uint64(del), 10), "", e.adminToken, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("detail delivered: %d", resp.StatusCode)
		}
		json.Unmarshal(body, &dto)
		if dto.Message.Retryable {
			t.Fatal("delivered entry must NOT be retryable")
		}
	})

	t.Run("list_projection_redaction", func(t *testing.T) {
		seedQueueEntry(t, e, "pending", "listredact@test.com", "listredact-to@test.com")
		resp, body := queuePhaseBRequest(t, e, "GET", "/api/v1/admin/queue/messages?limit=10", "", e.adminToken, "", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list: %d", resp.StatusCode)
		}
		for _, banned := range []string{`"lease_owner"`, `"lease_expires_at"`, `"raw_message"`, `"body"`, `"secret"`, `"message_id"`} {
			if strings.Contains(string(body), banned) {
				t.Fatalf("list leaked %s: %s", banned, body)
			}
		}
	})
}
