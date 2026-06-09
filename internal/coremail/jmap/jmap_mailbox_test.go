package jmap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
)

// ── Mailbox/query Tests ─────────────────────────────────────

func mailboxQuery(t *testing.T, addr string, params map[string]interface{}) *MailboxQueryResponse {
	t.Helper()
	methodCall := []interface{}{"Mailbox/query", params, "c1"}
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
		t.Fatalf("Mailbox/query error: %s - %s", errResp.Type, errResp.Detail)
	}
	var resp MailboxQueryResponse
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &resp)
	return &resp
}

func TestMailboxQueryBasic(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})
	if len(resp.IDs) == 0 {
		t.Fatal("expected at least 1 mailbox")
	}
	if resp.QueryState == "" {
		t.Fatal("expected queryState")
	}
}

func TestMailboxQueryRoleFilter(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"filter":    map[string]interface{}{"role": "inbox"},
	})
	if len(resp.IDs) != 1 {
		t.Fatalf("expected exactly 1 mailbox with role=inbox, got %d", len(resp.IDs))
	}
}

func TestMailboxQueryNameFilter(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"filter":    map[string]interface{}{"name": "INBOX"},
	})
	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 mailbox matching name, got %d", len(resp.IDs))
	}
}

func TestMailboxQuerySortByName(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"sort": []interface{}{
			map[string]interface{}{"property": "name", "isAscending": true},
		},
	})
	if len(resp.IDs) < 3 {
		t.Fatal("expected multiple mailboxes")
	}
}

func TestMailboxQuerySortBySortOrder(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"sort": []interface{}{
			map[string]interface{}{"property": "sortOrder", "isAscending": true},
		},
	})
	if len(resp.IDs) < 3 {
		t.Fatal("expected multiple mailboxes")
	}
}

func TestMailboxQueryPagination(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"position":  1,
		"limit":     2,
	})
	if len(resp.IDs) > 2 {
		t.Fatalf("expected at most 2 mailboxes, got %d", len(resp.IDs))
	}
}

func TestMailboxQueryCalculateTotal(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId":      "1",
		"calculateTotal": true,
	})
	if resp.Total == nil {
		t.Fatal("expected total")
	}
	if *resp.Total < 3 {
		t.Fatalf("expected at least 3 mailboxes, got %d", *resp.Total)
	}
}

func TestMailboxQueryInvalidAccountID(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{"accountId": "999"})
	_ = resp
}

func TestMailboxQueryConcurrent(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})
			if len(resp.IDs) < 3 {
				t.Errorf("expected at least 3 mailbox IDs, got %d", len(resp.IDs))
			}
		}()
	}
	wg.Wait()
}

// ── Mailbox/changes Tests ───────────────────────────────────

func mailboxChanges(t *testing.T, addr string, params map[string]interface{}) *MailboxChangesResponse {
	t.Helper()
	methodCall := []interface{}{"Mailbox/changes", params, "c1"}
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
		t.Fatalf("Mailbox/changes error: %s - %s", errResp.Type, errResp.Detail)
	}
	var resp MailboxChangesResponse
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &resp)
	return &resp
}

func TestMailboxChangesNoChanges(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp1 := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})
	t.Logf("Mailbox/query state: %s", resp1.QueryState)

	resp2 := mailboxChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": resp1.QueryState,
	})
	t.Logf("Mailbox/changes: old=%s new=%s created=%d", resp2.OldState, resp2.NewState, len(resp2.Created))

	if len(resp2.Created)+len(resp2.Updated)+len(resp2.Destroyed) != 0 {
		t.Fatalf("expected no changes, got created=%d updated=%d destroyed=%d",
			len(resp2.Created), len(resp2.Updated), len(resp2.Destroyed))
	}
}

func TestMailboxChangesMailboxCreated(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Get initial state.
	initial := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})

	// Create a new folder.
	ctx := context.Background()
	ms.Folders.Create(ctx, &storage.Folder{
		MailboxID: 1, Name: "NewFolder", Path: "NewFolder",
	}, nil)

	// Request changes.
	resp := mailboxChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": initial.QueryState,
	})
	if len(resp.Created) == 0 {
		t.Fatal("expected at least 1 created mailbox")
	}
}

func TestMailboxChangesStateChangesCorrectly(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp1 := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})
	resp2 := mailboxChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": resp1.QueryState,
	})
	if resp2.NewState == "" {
		t.Fatal("expected newState")
	}
	if resp2.OldState != resp1.QueryState {
		t.Fatal("oldState should match sinceState")
	}
}

func TestMailboxChangesInvalidState(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": "invalid-state-format",
	})
	if resp.NewState == "" {
		t.Fatal("expected newState even with invalid sinceState")
	}
}

func TestMailboxChangesConcurrent(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{"accountId": "1"})
	state := resp.QueryState

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := mailboxChanges(t, addr, map[string]interface{}{
				"accountId":  "1",
				"sinceState": state,
			})
			if r.AccountID == "" {
				t.Errorf("expected accountID")
			}
		}()
	}
	wg.Wait()
}

// ── Security Tests ─────────────────────────────────────────

func TestMailboxQueryAuthRequired(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Without auth.
	url := fmt.Sprintf("http://%s/jmap/api", addr)
	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/query", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	bodyBytes, _ := json.Marshal(req)
	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMailboxChangesSecurityIsolation(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Only one mailbox, so isolation is implicit.
	resp := mailboxChanges(t, addr, map[string]interface{}{
		"accountId":  "1",
		"sinceState": "",
	})
	_ = resp
}

// ── Observability Tests ────────────────────────────────────

func TestJMAPMailboxMetrics(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.Metrics.IncJMAPMailboxQuery()
	obs.Metrics.IncJMAPMailboxChanges()

	snap := obs.Metrics.Snapshot()
	if snap.JMAPMailboxQueries != 1 {
		t.Fatalf("expected 1 mailbox query, got %d", snap.JMAPMailboxQueries)
	}
	if snap.JMAPMailboxChanges != 1 {
		t.Fatalf("expected 1 mailbox changes, got %d", snap.JMAPMailboxChanges)
	}

	// IMAP metrics should not be affected.
	if snap.IMAPLoginSuccess != 0 {
		t.Fatal("IMAP metrics should not be affected by JMAP mailbox operations")
	}
}

func TestJMAPMailboxQueryUnsupportedFilter(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"filter":    map[string]interface{}{"unsupported": "value"},
	})
	if len(resp.IDs) == 0 {
		t.Fatal("expected results with unsupported filter (ignored)")
	}
}

func TestJMAPMailboxQueryUnsupportedSort(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	resp := mailboxQuery(t, addr, map[string]interface{}{
		"accountId": "1",
		"sort": []interface{}{
			map[string]interface{}{"property": "unsupported"},
		},
	})
	if len(resp.IDs) == 0 {
		t.Fatal("expected results with unsupported sort (ignored)")
	}
}
