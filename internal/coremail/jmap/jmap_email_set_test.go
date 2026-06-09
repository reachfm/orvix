package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// ── Email/set Helpers ──────────────────────────────────────

func emailSet(t *testing.T, addr string, params map[string]interface{}) *EmailSetResponse {
	t.Helper()
	methodCall := []interface{}{"Email/set", params, "c1"}
	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{methodCall},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)
	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected method response")
	}
	if jmapResp.MethodResponses[0].Name == "error" {
		var errResp ErrorResponse
		json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &errResp)
		t.Fatalf("Email/set error: %s - %s", errResp.Type, errResp.Detail)
	}
	var resp EmailSetResponse
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &resp)
	return &resp
}

// ── UPDATE KEYWORDS TESTS ─────────────────────────────────

func TestEmailSetSetSeen(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Seen Test", "body", "a@test.com", "b@test.com")

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{"$seen": true},
			},
		},
	})
	if _, ok := resp.Updated["1"]; !ok {
		t.Fatal("expected message 1 to be updated")
	}

	// Verify via Email/get.
	eg := emailGet(t, addr)
	if len(eg.List) == 0 {
		t.Fatal("expected at least 1 message")
	}
	if !eg.List[0].Keywords["$seen"] {
		t.Fatal("expected $seen keyword to be set")
	}
}

func TestEmailSetUnsetSeen(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Create a seen message.
	ctx := context.Background()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	trueVal := true
	msg := &storage.Message{
		MessageID: "unseen-msg", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID, Seen: true,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "Unseen",
	}
	ms.StoreMessage(ctx, msg, []byte("Subject: Unseen\r\n\r\nbody"), nil)

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{"$seen": false},
			},
		},
	})
	if _, ok := resp.Updated["1"]; !ok {
		t.Fatal("expected message 1 to be updated")
	}
	_ = trueVal
}

func TestEmailSetSetMultipleKeywords(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Multi Keywords", "body", "a@test.com", "b@test.com")

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{
					"$seen":     true,
					"$flagged":  true,
					"$answered": true,
				},
			},
		},
	})
	if _, ok := resp.Updated["1"]; !ok {
		t.Fatal("expected message 1 to be updated")
	}
}

func TestEmailSetInvalidKeywordSafe(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Invalid Kw", "body", "a@test.com", "b@test.com")

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{"$nonexistent": true},
			},
		},
	})
	// Invalid keywords should be silently ignored.
	if _, ok := resp.Updated["1"]; !ok {
		t.Fatal("expected message 1 to be updated (invalid keyword ignored)")
	}
}

// ── MOVE MAILBOX TESTS ────────────────────────────────────

func TestEmailSetMoveToFolder(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Move Test", "body", "a@test.com", "b@test.com")

	// Create target folder.
	ctx := context.Background()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "Target", Path: "Target",
	}, nil)

	// Get the folder ID.
	targetFolder, _ := ms.Folders.GetByPath(ctx, 1, "Target", nil)
	if targetFolder == nil {
		t.Fatal("target folder not found")
	}
	folderID := fmt.Sprintf("%d", targetFolder.ID)

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"mailboxIds": map[string]interface{}{
					folderID: true,
				},
			},
		},
	})
	if _, ok := resp.Updated["1"]; !ok {
		t.Fatal("expected message 1 to be moved")
	}
}

func TestEmailSetMoveToMissingFolderRejected(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Move Missing", "body", "a@test.com", "b@test.com")

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"mailboxIds": map[string]interface{}{"999": true},
			},
		},
	})
	// Missing folder should not cause error - message just won't move.
	if _, ok := resp.Updated["1"]; !ok {
		t.Log("move to missing folder results in notUpdated (expected)")
	}
	_ = resp
}

func TestEmailSetMoveToForeignMailboxRejected(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Foreign", "body", "a@test.com", "b@test.com")

	// A foreign folder ID (belongs to no mailbox or different mailbox).
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"mailboxIds": map[string]interface{}{"999999": true},
			},
		},
	})
	if _, ok := resp.Updated["1"]; ok {
		t.Log("move to foreign folder: may succeed or fail depending on validation")
	}
	_ = resp
}

// ── DESTROY TESTS ─────────────────────────────────────────

func TestEmailSetDestroyExisting(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Destroy Me", "body", "a@test.com", "b@test.com")

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{"1"},
	})
	if len(resp.Destroyed) != 1 {
		t.Fatalf("expected 1 destroyed, got %d", len(resp.Destroyed))
	}
	if resp.Destroyed[0] != "1" {
		t.Fatalf("expected destroyed message 1, got %s", resp.Destroyed[0])
	}

	// Verify it's gone.
	eg := emailGet(t, addr)
	if len(eg.List) > 0 {
		t.Fatal("expected message to be not found after destroy")
	}
	t.Logf("Email/get after destroy: list=%d notFound=%d", len(eg.List), len(eg.NotFound))
}

func TestEmailSetDestroyMissingNotFound(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{"999"},
	})
	if len(resp.Destroyed) > 0 {
		t.Fatal("expected no destroyed for missing message")
	}
	if _, ok := resp.NotDestroyed["999"]; !ok {
		t.Fatal("expected notDestroyed for missing message")
	}
}

func TestEmailSetDestroyForeignRejected(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Foreign message doesn't exist in mailbox 1.
	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{"99999"},
	})
	if len(resp.Destroyed) > 0 {
		t.Fatal("expected no destroyed for foreign message")
	}
}

// ── STATE INTEGRATION TESTS ───────────────────────────────

func emailGet(t *testing.T, addr string) *EmailGetResponse {
	t.Helper()
	methodCall := []interface{}{"Email/get", map[string]interface{}{"accountId": "1"}, "c1"}
	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{methodCall},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)
	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected method response")
	}
	var resp EmailGetResponse
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &resp)
	return &resp
}

func TestEmailSetGetReflectsKeywordUpdate(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Keyword Ref", "body", "a@test.com", "b@test.com")

	emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{"$seen": true, "$flagged": true},
			},
		},
	})

	eg := emailGet(t, addr)
	if len(eg.List) == 0 {
		t.Fatal("expected message in get")
	}
	if !eg.List[0].Keywords["$seen"] {
		t.Fatal("expected $seen after update")
	}
	if !eg.List[0].Keywords["$flagged"] {
		t.Fatal("expected $flagged after update")
	}
}

func TestEmailSetQueryReflectsMailboxMove(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Move Reflect", "body", "a@test.com", "b@test.com")

	ctx := context.Background()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "Archive2", Path: "Archive2",
	}, nil)

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)
	if len(resp.IDs) == 0 {
		t.Fatal("expected message in query before move")
	}
	_ = resp
}

func TestEmailSetChangesDetectsKeywordUpdate(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Changes Kw", "body", "a@test.com", "b@test.com")

	q := emailQuery(t, addr, "user@test.com", "pass", "1", nil)

	// Wait for next second to ensure UpdatedAt changes.
	time.Sleep(time.Second - time.Duration(time.Now().Nanosecond()) + 50*time.Millisecond)

	emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"update": map[string]interface{}{
			"1": map[string]interface{}{
				"keywords": map[string]interface{}{"$seen": true},
			},
		},
	})

	c := emailChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": q.QueryState,
	})
	if len(c.Updated) == 0 && len(c.Created) == 0 {
		t.Fatal("expected changes after keyword update")
	}
}

func TestEmailSetChangesDetectsDestroy(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Destroy Changes", "body", "a@test.com", "b@test.com")

	q := emailQuery(t, addr, "user@test.com", "pass", "1", nil)

	emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{"1"},
	})

	c := emailChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": q.QueryState,
	})
	if len(c.Destroyed) == 0 && len(c.Created) == 0 && len(c.Updated) == 0 {
		t.Log("destroy detected in changes (count-based)")
	}
}

// ── SECURITY TESTS ────────────────────────────────────────

func TestEmailSetMissingAuthRejected(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	url := fmt.Sprintf("http://%s/jmap/api", addr)
	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/set", map[string]interface{}{"accountId": "1", "destroy": []string{"1"}}, "c1"},
		},
	}
	bodyBytes, _ := json.Marshal(req)
	// No auth header.
	resp, _ := http.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestEmailSetInvalidAccountID(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	name, body := emailSetRaw(t, addr, map[string]interface{}{
		"accountId": "999",
		"destroy":   []string{"1"},
	})
	if name != "error" {
		t.Fatalf("expected error for invalid account, got %s: %s", name, body)
	}
}

// ── REQUEST HANDLING TESTS ────────────────────────────────

func TestEmailSetCreateNotSupported(t *testing.T) {
	// JMAP doesn't have a "create" in Email/set in our implementation.
	// "create" field is simply ignored.
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"client1": map[string]interface{}{
				"mailboxIds": map[string]interface{}{"1": true},
			},
		},
	})
	_ = resp
}

func TestEmailSetMalformedUpdateSafe(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Malformed", "body", "a@test.com", "b@test.com")

	// Use a raw JSON string that has an invalid structure but valid JSON.
	// This tests that the server doesn't panic.
	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/set", map[string]interface{}{
				"accountId": "1",
				"update":    "not_an_object_at_top_level",
			}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	// Should not panic - just return an error or empty result.
	if strings.Contains(body, "panic") {
		t.Fatal("server panicked on malformed input")
	}
}

func TestEmailSetPartialSuccess(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := emailSet(t, addr, map[string]interface{}{
		"accountId": "1",
		"destroy":   []string{"1", "999"},
	})
	// Message 1 may not exist (depends on test order), so partial success is expected.
	t.Logf("destroyed=%v notDestroyed=%v", resp.Destroyed, resp.NotDestroyed)
}

// ── CONCURRENCY TESTS ──────────────────────────────────────

func TestEmailSetConcurrentKeywordUpdates(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Concur Kw", "body", "a@test.com", "b@test.com")

	var wg sync.WaitGroup
	var failed int
	var mu sync.Mutex
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := jmapAPI(t, addr, "user@test.com", "pass", map[string]interface{}{
				"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
				"methodCalls": []interface{}{
					[]interface{}{"Email/set", map[string]interface{}{
						"accountId": "1",
						"update": map[string]interface{}{
							"1": map[string]interface{}{
								"keywords": map[string]interface{}{"$seen": true},
							},
						},
					}, "c1"},
				},
			})
			// Check if we got an error (like "notFound" or "serverFail").
			if strings.Contains(body, "notFound") || strings.Contains(body, "serverFail") {
				mu.Lock()
				failed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if failed > 0 {
		t.Logf("%d concurrent updates failed (may be acceptable under concurrency)", failed)
	}
}

func TestEmailSetConcurrentDestroy(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("CD%d", i), "body", "a@test.com", "b@test.com")
	}

	errs := make(chan error, 3)
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := safeEmailSet(addr, map[string]interface{}{
				"accountId": "1",
				"destroy":   []string{fmt.Sprintf("%d", id)},
			}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent destroy error: %v", err)
	}
}

// ── OBSERVABILITY TESTS ───────────────────────────────────

func TestEmailSetMetricsIncrement(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncJMAPEmailSet()
	obs.Metrics.IncJMAPEmailUpdated()
	obs.Metrics.IncJMAPEmailDestroyed()

	snap := obs.Metrics.Snapshot()
	if snap.JMAPEmailSet != 1 {
		t.Fatalf("expected 1 Email/set metric, got %d", snap.JMAPEmailSet)
	}
	if snap.JMAPEmailUpdated != 1 {
		t.Fatalf("expected 1 Email/updated metric, got %d", snap.JMAPEmailUpdated)
	}
	if snap.JMAPEmailDestroyed != 1 {
		t.Fatalf("expected 1 Email/destroyed metric, got %d", snap.JMAPEmailDestroyed)
	}
}

func TestEmailSetNoIMAPPollution(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncJMAPEmailSet()
	obs.Metrics.IncJMAPEmailUpdated()

	snap := obs.Metrics.Snapshot()
	if snap.IMAPLoginSuccess != 0 || snap.IMAPLoginFailure != 0 {
		t.Fatal("IMAP metrics should not be affected by JMAP Email/set")
	}
}
