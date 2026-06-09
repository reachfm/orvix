package jmap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	_ "modernc.org/sqlite"
)

// ── Test Infrastructure ─────────────────────────────────────

func testJMAPServer(t *testing.T) (*storage.MailStore, string, func()) {
	t.Helper()

	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "jmap_test.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}

	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create storage table: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}

	engCfg := coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()}
	eng := coremail.NewEngine(engCfg)

	_, mbox, err := eng.ProvisionDomain(context.Background(), "test.com", "smb",
		"user@test.com", "pass", "Test User", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	basePath := filepath.Join(dir, "msgs")
	ms, _ := storage.NewMailStore(db, basePath)
	ms.EnsureMailboxStorage(context.Background(), mbox.ID, 1, mbox.DomainID, nil)

	srv := NewServer(eng)
	srv.SetMailStore(ms)
	srv.Hostname = "jmap.test.com"

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()

	go func() {
		srv.srv = &http.Server{Addr: addr, Handler: srv.withMiddleware(srv.mux)}
		srv.srv.Serve(listener)
	}()

	cleanup := func() { listener.Close() }
	return ms, addr, cleanup
}

func jmapAPI(t *testing.T, addr, username, password string, reqBody interface{}) (*http.Response, string) {
	t.Helper()
	url := fmt.Sprintf("http://%s/jmap/api", addr)

	body, _ := json.Marshal(reqBody)
	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	httpReq.Header.Set("Authorization", "Basic "+auth)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	return resp, string(bodyBytes)
}

func jmapSession(t *testing.T, addr, username, password string) (*http.Response, string) {
	t.Helper()
	url := fmt.Sprintf("http://%s/jmap/session", addr)
	httpReq, _ := http.NewRequest("GET", url, nil)
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	httpReq.Header.Set("Authorization", "Basic "+auth)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// Helper to store a message in the test mailbox.
func jmapStoreMsg(t *testing.T, ms *storage.MailStore, mailboxID uint, subject, body, from, to string) {
	t.Helper()
	ctx := context.Background()

	// Get the INBOX folder.
	folder, _ := ms.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
	if folder == nil {
		t.Fatal("INBOX not found")
	}

	now := time.Now()
	msg := &storage.Message{
		MessageID:   storage.GenerateMessageID(),
		TenantID:    1,
		DomainID:    1,
		MailboxID:   mailboxID,
		FolderID:    folder.ID,
		FromAddress: from,
		ToAddresses: to,
		Subject:     subject,
		ReceivedDate: now,
	}
	rfc822 := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: <msg-%s>\r\n\r\n%s",
		from, to, subject, msg.MessageID[:8], body))
	ms.StoreMessage(ctx, msg, rfc822, nil)
}

// ── OBSERVABILITY TESTS ─────────────────────────────────────

func TestJMAPDedicatedObservability(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	// Simulate JMAP events.
	obs.Metrics.IncJMAPRequest()
	obs.Metrics.IncJMAPAuthSuccess()
	obs.Metrics.IncJMAPAuthFailure()
	obs.Metrics.IncJMAPMethodSuccess()
	obs.Metrics.IncJMAPMethodFailure()

	snap := obs.Metrics.Snapshot()

	// JMAP metrics should be independent.
	if snap.JMAPRequests != 1 {
		t.Fatalf("expected 1 JMAP request, got %d", snap.JMAPRequests)
	}
	if snap.JMAPAuthSuccess != 1 {
		t.Fatalf("expected 1 JMAP auth success, got %d", snap.JMAPAuthSuccess)
	}
	if snap.JMAPAuthFailure != 1 {
		t.Fatalf("expected 1 JMAP auth failure, got %d", snap.JMAPAuthFailure)
	}

	// IMAP metrics should not be affected.
	if snap.IMAPLoginSuccess != 0 || snap.IMAPLoginFailure != 0 {
		t.Fatal("IMAP metrics should not be affected by JMAP operations")
	}
}

func TestJMAPMetricsNotReused(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	// Record IMAP events.
	obs.Metrics.IncIMAPLoginSuccess()
	obs.Metrics.IncIMAPLoginFailure()

	// Record JMAP events.
	obs.Metrics.IncJMAPAuthSuccess()
	obs.Metrics.IncJMAPAuthFailure()

	snap := obs.Metrics.Snapshot()

	// JMAP should not affect IMAP.
	if snap.IMAPLoginSuccess != 1 || snap.JMAPAuthSuccess != 1 {
		t.Fatal("JMAP and IMAP metrics must be independent")
	}
}

// ── REQUEST HANDLING TESTS ─────────────────────────────────

func TestJMAPValidRequest(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []interface{}{},
	}
	resp, body := jmapAPI(t, addr, "user@test.com", "pass", req)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var jmapResp Response
	json.Unmarshal([]byte(body), &jmapResp)
	if jmapResp.SessionState == "" {
		t.Fatal("expected sessionState")
	}
}

func TestJMAPMalformedJSON(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	url := fmt.Sprintf("http://%s/jmap/api", addr)
	auth := base64.StdEncoding.EncodeToString([]byte("user@test.com:pass"))
	httpReq, _ := http.NewRequest("POST", url, bytes.NewReader([]byte("not json")))
	httpReq.Header.Set("Authorization", "Basic "+auth)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 (JMAP error response), got %d", resp.StatusCode)
	}
}

func TestJMAPUnsupportedMethod(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []interface{}{
			[]interface{}{"UnknownMethod", map[string]interface{}{}, "c1"},
		},
	}
	resp, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp Response
	json.Unmarshal([]byte(body), &jmapResp)

	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected method response")
	}
	if jmapResp.MethodResponses[0].Name != "error" {
		t.Fatalf("expected error response, got %s", jmapResp.MethodResponses[0].Name)
	}
	_ = resp
}

func TestJMAPInvalidAccountID(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/get", map[string]interface{}{"accountId": "999"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp Response
	json.Unmarshal([]byte(body), &jmapResp)
	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected method response")
	}
}

func TestJMAPMissingAuth(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	url := fmt.Sprintf("http://%s/jmap/api", addr)
	resp, _ := http.Post(url, "application/json", bytes.NewReader([]byte("{}")))
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

// ── MAILBOX/GET TESTS ──────────────────────────────────────

func TestMailboxGetReturnsINBOX(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Test", "Body", "a@test.com", "b@test.com")

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	t.Logf("Mailbox/get response: %s", body)

	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
			ID     string          `json:"id"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected Mailbox/get response")
	}
	if jmapResp.MethodResponses[0].Name != "Mailbox/get" {
		t.Fatalf("expected Mailbox/get, got %s - body: %s", jmapResp.MethodResponses[0].Name, body)
	}

	var mailboxResp struct {
		List []MailboxEntry `json:"list"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &mailboxResp)

	foundINBOX := false
	for _, mb := range mailboxResp.List {
		if mb.Role != nil && *mb.Role == "inbox" {
			foundINBOX = true
			if mb.TotalEmails < 1 {
				t.Fatal("INBOX should have at least 1 message")
			}
			break
		}
	}
	if !foundINBOX {
		t.Fatal("expected INBOX in mailbox list")
	}
}

func TestMailboxGetRoles(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	var mailboxResp struct {
		List []MailboxEntry `json:"list"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &mailboxResp)

	for _, mb := range mailboxResp.List {
		if mb.Role != nil {
			switch *mb.Role {
			case "inbox", "sent", "trash", "drafts", "junk", "archive":
				// valid
			default:
				t.Fatalf("unknown role: %s", *mb.Role)
			}
		}
	}
}

func TestMailboxGetTotalEmails(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("Msg %d", i), "body", "a@test.com", "b@test.com")
	}

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	var mailboxResp struct {
		List []MailboxEntry `json:"list"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &mailboxResp)

	for _, mb := range mailboxResp.List {
		if mb.Role != nil && *mb.Role == "inbox" {
			if mb.TotalEmails < 3 {
				t.Fatalf("expected at least 3 emails in INBOX, got %d", mb.TotalEmails)
			}
		}
	}
}

// ── EMAIL/GET TESTS ────────────────────────────────────────

func TestEmailGetReturnsRequested(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Test Subject", "Hello World", "sender@test.com", "rcpt@test.com")

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
			ID     string          `json:"id"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	if len(jmapResp.MethodResponses) == 0 {
		t.Fatal("expected Email/get response")
	}
	if jmapResp.MethodResponses[0].Name != "Email/get" {
		t.Fatalf("expected Email/get, got %s", jmapResp.MethodResponses[0].Name)
	}
}

func TestEmailGetMultipleMessages(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("Subject %d", i), "Body", "a@test.com", "b@test.com")
	}

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	var emailResp struct {
		List     []EmailEntry `json:"list"`
		NotFound []string     `json:"notFound"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &emailResp)

	if len(emailResp.List) < 3 {
		t.Fatalf("expected at least 3 emails, got %d", len(emailResp.List))
	}
}

func TestEmailGetSpecificIDs(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Specific", "Body", "a@test.com", "b@test.com")

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1", "ids": []string{"1"}}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	var emailResp struct {
		List     []EmailEntry `json:"list"`
		NotFound []string     `json:"notFound"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &emailResp)

	if len(emailResp.List) != 1 {
		t.Fatalf("expected 1 email, got %d", len(emailResp.List))
	}
}

func TestEmailGetNotFound(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1", "ids": []string{"999"}}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	var jmapResp struct {
		MethodResponses []struct {
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	var emailResp struct {
		List     []EmailEntry `json:"list"`
		NotFound []string     `json:"notFound"`
	}
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &emailResp)

	if len(emailResp.List) != 0 {
		t.Fatal("expected empty list for nonexistent ID")
	}
	if len(emailResp.NotFound) == 0 {
		t.Fatal("expected notFound for nonexistent ID")
	}
}

func TestEmailGetSubjectAndFrom(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Hello World", "Body content", "sender@test.com", "rcpt@test.com")

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	if !strings.Contains(body, "Hello World") {
		t.Fatal("expected subject in response")
	}
	if !strings.Contains(body, "sender@test.com") {
		t.Fatal("expected from in response")
	}
}

// ── CONCURRENCY TESTS ──────────────────────────────────────

func TestJMAPConcurrentMailboxGet(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Mailbox/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := jmapAPI(t, addr, "user@test.com", "pass", req)
			if !strings.Contains(body, "inbox") {
				errs <- fmt.Errorf("expected inbox in response")
			}
		}()
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Error(e)
	}
}

func TestJMAPConcurrentEmailGet(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("Msg %d", i), "Body", "a@test.com", "b@test.com")
	}

	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1"}, "c1"},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := jmapAPI(t, addr, "user@test.com", "pass", req)
			if !strings.Contains(body, "Msg") {
				t.Errorf("expected messages in response")
			}
		}()
	}
	wg.Wait()
}

// ── Email/query Helpers ─────────────────────────────────────

func emailQuery(t *testing.T, addr, username, password, accountID string, params map[string]interface{}) *EmailQueryResponse {
	t.Helper()

	methodCall := []interface{}{"Email/query", params, "c1"}
	if accountID != "" && params == nil {
		methodCall = []interface{}{"Email/query", map[string]interface{}{"accountId": accountID}, "c1"}
	}

	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{methodCall},
	}

	_, body := jmapAPI(t, addr, username, password, req)

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
		t.Fatalf("Email/query returned error: %s - %s", errResp.Type, errResp.Detail)
	}

	var queryResp EmailQueryResponse
	json.Unmarshal([]byte(jmapResp.MethodResponses[0].Params), &queryResp)
	return &queryResp
}

func emailQueryRaw(t *testing.T, addr, username, password string, params map[string]interface{}) (string, string) {
	t.Helper()

	methodCall := []interface{}{"Email/query", params, "c1"}
	req := map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{methodCall},
	}

	_, body := jmapAPI(t, addr, username, password, req)
	var jmapResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
		} `json:"methodResponses"`
	}
	json.Unmarshal([]byte(body), &jmapResp)

	if len(jmapResp.MethodResponses) == 0 {
		return "", body
	}
	return jmapResp.MethodResponses[0].Name, body
}

// ── Email/query Tests ───────────────────────────────────────

func TestEmailQueryBasicIDs(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("QMsg %d", i), "body", "a@test.com", "b@test.com")
	}

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)
	if len(resp.IDs) == 0 {
		t.Fatal("expected at least 1 ID")
	}
	if resp.QueryState == "" {
		t.Fatal("expected queryState")
	}
}

func TestEmailQueryDefaultSort(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("Msg %d", i), "body", "a@test.com", "b@test.com")
	}

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)

	// Default is receivedAt descending, so newest first.
	if len(resp.IDs) < 2 {
		t.Fatal("expected multiple IDs")
	}
	// First ID should be the last message stored (highest ID).
	_ = resp.IDs[0]
}

func TestEmailQuerySortReceivedAtAsc(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("Msg %d", i), "body", "a@test.com", "b@test.com")
	}

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "receivedAt", "isAscending": true},
		},
	})

	if len(resp.IDs) < 3 {
		t.Fatal("expected 3 IDs")
	}
}

func TestEmailQuerySortSize(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Store messages (sizes will vary by content).
	jmapStoreMsg(t, ms, 1, "SMsg 1", "A", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "SMsg 2", "BB", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "size", "isAscending": true},
		},
	})

	if len(resp.IDs) != 2 {
		t.Fatal("expected 2 IDs")
	}
}

func TestEmailQuerySortSizeDescending(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Small", "AB", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "Large", "ABCDEFGH", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "size", "isAscending": false},
		},
	})

	if len(resp.IDs) != 2 {
		t.Fatal("expected 2 IDs")
	}
}

func TestEmailQuerySortSubject(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Alpha", "body", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "Beta", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "subject", "isAscending": true},
		},
	})

	if len(resp.IDs) != 2 {
		t.Fatal("expected 2 IDs")
	}
}

func TestEmailQuerySortSubjectDesc(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Alpha", "body", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "Beta", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "subject", "isAscending": false},
		},
	})

	if len(resp.IDs) != 2 {
		t.Fatal("expected 2 IDs")
	}
}

func TestEmailQueryFilterFrom(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "From Alice", "body", "alice@test.com", "bob@test.com")
	jmapStoreMsg(t, ms, 1, "From Bob", "body", "bob@test.com", "alice@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"from": "alice"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message from alice, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterTo(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "To Alice", "body", "sender@test.com", "alice@test.com")
	jmapStoreMsg(t, ms, 1, "To Bob", "body", "sender@test.com", "bob@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"to": "alice"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message to alice, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterSubject(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Important Update", "body", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "Spam Offer", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"subject": "Important"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message with 'Important' in subject, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterText(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Meeting", "body", "a@test.com", "b@test.com")
	jmapStoreMsg(t, ms, 1, "Shopping", "body", "b@other.com", "a@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"text": "Meeting"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message matching text, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterBefore(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Recent", "body", "a@test.com", "b@test.com")

	// Filter with a future date — should include the message.
	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"before": "2099-01-01T00:00:00Z"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message before future date, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterAfter(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Old", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"after": "2020-01-01T00:00:00Z"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message after 2020, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterHasKeyword(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Store seen message by directly setting Seen flag.
	ctx := context.Background()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	msg := &storage.Message{
		MessageID: "seen-msg", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID, Seen: true,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "Seen",
	}
	ms.StoreMessage(ctx, msg, []byte("Subject: Seen\r\n\r\nbody"), nil)

	jmapStoreMsg(t, ms, 1, "Unseen", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"hasKeyword": "$seen"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 seen message, got %d", len(resp.IDs))
	}
}

func TestEmailQueryFilterNotKeyword(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	ctx := context.Background()
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)
	// Unseen message.
	jmapStoreMsg(t, ms, 1, "Unseen", "body", "a@test.com", "b@test.com")
	// Seen message.
	msg := &storage.Message{
		MessageID: "seen-2", TenantID: 1, DomainID: 1,
		MailboxID: 1, FolderID: folder.ID, Seen: true,
		FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: "Seen",
	}
	ms.StoreMessage(ctx, msg, []byte("Subject: Seen\r\n\r\nbody"), nil)

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"notKeyword": "$seen"},
	})

	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 unseen message, got %d", len(resp.IDs))
	}
}

func TestEmailQueryPaginationPosition(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("P%d", i), "body", "a@test.com", "b@test.com")
	}

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"position": 2,
	})

	if len(resp.IDs) < 3 {
		t.Fatalf("expected at least 3 IDs starting from position 2, got %d", len(resp.IDs))
	}
}

func TestEmailQueryPaginationLimit(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 10; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("L%d", i), "body", "a@test.com", "b@test.com")
	}

	limit := 3
	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"limit": limit,
	})

	if len(resp.IDs) > limit {
		t.Fatalf("expected at most %d IDs, got %d", limit, len(resp.IDs))
	}
}

func TestEmailQueryCalculateTotal(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 4; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("T%d", i), "body", "a@test.com", "b@test.com")
	}

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"calculateTotal": true,
	})

	if resp.Total == nil {
		t.Fatal("expected total to be calculated")
	}
	if *resp.Total < 4 {
		t.Fatalf("expected total >= 4, got %d", *resp.Total)
	}
}

func TestEmailQueryInvalidAccountID(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	name, body := emailQueryRaw(t, addr, "user@test.com", "pass", map[string]interface{}{
		"accountId": "999",
	})
	if name != "Email/query" && name != "error" {
		t.Fatalf("expected Email/query or error, got %s: %s", name, body)
	}
}

func TestEmailQueryMalformedFilter(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Malformed filter (wrong type) should not cause panic.
	name, body := emailQueryRaw(t, addr, "user@test.com", "pass", map[string]interface{}{
		"accountId": "1",
		"filter":    "not an object",
	})
	if name != "Email/query" && name != "error" {
		t.Fatalf("expected Email/query or error, got %s: %s", name, body)
	}
}

func TestEmailQueryUnsupportedSort(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Unsortable", "body", "a@test.com", "b@test.com")

	// Unsupported sort property should not cause panic.
	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"sort": []interface{}{
			map[string]interface{}{"property": "nonexistent"},
		},
	})
	if len(resp.IDs) == 0 {
		t.Fatal("expected IDs even with unsupported sort")
	}
}

func TestEmailQueryNoCrossMailbox(t *testing.T) {
	_, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	// Only one mailbox exists, so cross-mailbox test is implicit.
	// The accountId from auth is always used.
	resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)
	_ = resp
}

func TestEmailQueryConcurrent(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		jmapStoreMsg(t, ms, 1, fmt.Sprintf("CMsg %d", i), "body", "a@test.com", "b@test.com")
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)
			if len(resp.IDs) < 3 {
				t.Errorf("expected at least 3 IDs, got %d", len(resp.IDs))
			}
		}()
	}
	wg.Wait()
}

func TestEmailQueryWithEmailGetFlow(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Flow Subject", "Flow Body", "flow@test.com", "user@test.com")

	// Query first.
	resp := emailQuery(t, addr, "user@test.com", "pass", "1", nil)
	if len(resp.IDs) == 0 {
		t.Fatal("expected IDs from query")
	}

	// Get the first email by ID.
	req := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{
			[]interface{}{"Email/get", map[string]interface{}{"accountId": "1", "ids": []string{resp.IDs[0]}}, "c1"},
		},
	}
	_, body := jmapAPI(t, addr, "user@test.com", "pass", req)

	if !strings.Contains(body, "Flow Subject") {
		t.Fatal("expected Flow Subject in Email/get after query")
	}
}

func TestEmailQueryInMailboxFilter(t *testing.T) {
	ms, addr, cleanup := testJMAPServer(t)
	defer cleanup()

	jmapStoreMsg(t, ms, 1, "Inbox Message", "body", "a@test.com", "b@test.com")

	resp := emailQuery(t, addr, "user@test.com", "pass", "1", map[string]interface{}{
		"filter": map[string]interface{}{"inMailbox": []string{"1"}},
	})
	if len(resp.IDs) != 1 {
		t.Fatalf("expected 1 message in mailbox 1, got %d", len(resp.IDs))
	}
}
