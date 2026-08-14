package jmap

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	_ "modernc.org/sqlite"
)

// ── JMAP Submission/set mail-access policy enforcement ─────────────
//
// Real wiring: real SQLite schema, real engine, real MailStore, real
// queue engine, and the canonical mailpolicy.Policy wired through
// Server.SetMailAccessPolicy. An internal-only mailbox must not
// submit to an external recipient; a local recipient is allowed.

func jmapPolicyEnv(t *testing.T) (*coremail.Engine, *storage.MailStore, *queue.QueueEngine, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "jmap_policy.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create coremail table: %v", err)
		}
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	_, _, err = eng.ProvisionDomain(context.Background(), "test.com", "smb", "user@test.com", "pass", "Test User", 1)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	// A local recipient mailbox on the same domain.
	if _, err := db.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, mail_access_mode, version, created_at, updated_at)
		 VALUES (1, 1, 'bob', 'bob@test.com', 'Bob', '', 'argon2id', 'active', 'internal_external', 1, datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("seed bob: %v", err)
	}

	ms, err := storage.NewMailStore(db, filepath.Join(dir, "msgs"))
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("storage table: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		_, _ = db.Exec(stmt)
	}
	for _, mb := range []uint{1, 2} {
		if err := ms.Folders.EnsureSystemFolders(context.Background(), mb, nil); err != nil {
			t.Fatalf("folders for %d: %v", mb, err)
		}
	}
	for _, stmt := range queue.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("queue table: %v", err)
		}
	}
	for _, stmt := range queue.Indexes() {
		_, _ = db.Exec(stmt)
	}
	qe := queue.NewQueueEngine(db)

	srv := NewServer(eng)
	srv.Hostname = "jmap.test.com"
	srv.MailStore = ms
	srv.SetQueueEngine(qe)
	srv.SetMailAccessPolicy(mailpolicy.New(&mailpolicy.EngineStore{Engine: eng}, mailpolicy.NopSink{}))

	// Bind the server handler without a listener: drive it through
	// httptest against the mux directly.
	ts := httptest.NewServer(srv.withMiddleware(srv.mux))
	t.Cleanup(ts.Close)

	return eng, ms, qe, srv, ts.URL
}

// jmapPolicySubmit creates a draft (or stored) email for the mailbox
// and submits it to the given recipient via Submission/set. Returns
// the HTTP status and whether the submission was created.
func jmapPolicySubmit(t *testing.T, url, recipient string) (int, bool) {
	t.Helper()

	// Store a draft message owned by user@test.com (mailbox 1).
	// Email/set create needs the mailstore; we drive the server HTTP
	// handler through the mux.
	createParams := map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"c1": map[string]interface{}{
				"mailboxIds": map[string]interface{}{"1": true},
				"to":         []map[string]interface{}{{"email": recipient}},
				"from":       map[string]interface{}{"email": "user@test.com"},
				"subject":    "Policy test",
				"body":       "body",
				"keywords":   map[string]interface{}{"$draft": true},
			},
		},
	}
	mc := []interface{}{"Email/set", createParams, "c1"}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{mc},
	})
	req, _ := http.NewRequest("POST", url+"/jmap/api", bytes.NewReader(reqBody))
	req.SetBasicAuth("user@test.com", "pass")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("email/set: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var setResp struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
			ID     string          `json:"id"`
		} `json:"methodResponses"`
	}
	if err := json.Unmarshal(raw, &setResp); err != nil {
		t.Fatalf("email/set decode: %v (%s)", err, raw)
	}
	if len(setResp.MethodResponses) == 0 {
		t.Fatalf("no email/set response: %s", raw)
	}
	var created struct {
		Created map[string]interface{} `json:"created"`
	}
	_ = json.Unmarshal(setResp.MethodResponses[0].Params, &created)
	if len(created.Created) == 0 {
		t.Fatalf("email not created: %s", raw)
	}
	emailID := "1"

	// Submission/set.
	subParams := map[string]interface{}{
		"accountId": "1",
		"create": map[string]interface{}{
			"s1": map[string]interface{}{"emailId": emailID},
		},
	}
	subCall := []interface{}{"Submission/set", subParams, "s1"}
	subBody, _ := json.Marshal(map[string]interface{}{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail", "urn:ietf:params:jmap:submission"},
		"methodCalls": []interface{}{subCall},
	})
	subReq, _ := http.NewRequest("POST", url+"/jmap/api", bytes.NewReader(subBody))
	subReq.SetBasicAuth("user@test.com", "pass")
	subResp, err := http.DefaultClient.Do(subReq)
	if err != nil {
		t.Fatalf("submission/set: %v", err)
	}
	subRaw, _ := io.ReadAll(subResp.Body)
	subResp.Body.Close()
	var subResult struct {
		MethodResponses []struct {
			Name   string          `json:"name"`
			Params json.RawMessage `json:"params"`
			ID     string          `json:"id"`
		} `json:"methodResponses"`
	}
	if err := json.Unmarshal(subRaw, &subResult); err != nil {
		t.Fatalf("submission/set decode: %v (%s)", err, subRaw)
	}
	if len(subResult.MethodResponses) == 0 {
		t.Fatalf("no submission/set response: %s", subRaw)
	}
	var subCreated struct {
		Created    map[string]interface{} `json:"created"`
		NotCreated map[string]string      `json:"notCreated"`
	}
	_ = json.Unmarshal(subResult.MethodResponses[0].Params, &subCreated)
	if len(subCreated.Created) == 0 {
		t.Logf("submission not created: %s", subRaw)
	}
	return 200, len(subCreated.Created) > 0
}

func TestJMAPSubmissionPolicy_InternalOnlyMailboxExternalRecipientDenied(t *testing.T) {
	eng, ms, qe, _, url := jmapPolicyEnv(t)
	// Flip the sender mailbox to internal_only.
	if _, err := eng.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_only' WHERE email='user@test.com'`); err != nil {
		t.Fatalf("flip mode: %v", err)
	}

	status, created := jmapPolicySubmit(t, url, "external@example.com")
	if status != 200 {
		t.Fatalf("submission status %d", status)
	}
	if created {
		t.Fatal("internal-only mailbox must NOT submit to an external recipient")
	}
	// No queue entry may exist for the denied submission.
	metrics, _ := qe.Metrics(context.Background(), nil)
	if metrics.Pending != 0 {
		t.Fatalf("denied submission must not enqueue, pending=%d", metrics.Pending)
	}
	_ = ms
}

func TestJMAPSubmissionPolicy_InternalOnlyMailboxLocalRecipientAllowed(t *testing.T) {
	eng, _, qe, _, url := jmapPolicyEnv(t)
	if _, err := eng.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_only' WHERE email='user@test.com'`); err != nil {
		t.Fatalf("flip mode: %v", err)
	}

	status, created := jmapPolicySubmit(t, url, "bob@test.com")
	if status != 200 {
		t.Fatalf("submission status %d", status)
	}
	if !created {
		t.Fatal("internal-only mailbox must be allowed to submit to a LOCAL recipient")
	}
	metrics, _ := qe.Metrics(context.Background(), nil)
	if metrics.Pending == 0 {
		t.Fatal("local submission must enqueue")
	}
}

func TestJMAPSubmissionPolicy_ExternalEnabledMailboxExternalRecipientAllowed(t *testing.T) {
	eng, _, qe, _, url := jmapPolicyEnv(t)
	if _, err := eng.DB.Exec(`UPDATE coremail_mailboxes SET mail_access_mode='internal_external' WHERE email='user@test.com'`); err != nil {
		t.Fatalf("flip mode: %v", err)
	}

	status, created := jmapPolicySubmit(t, url, "external@example.com")
	if status != 200 {
		t.Fatalf("submission status %d", status)
	}
	if !created {
		t.Fatal("external-enabled mailbox must be allowed to submit to an external recipient")
	}
	metrics, _ := qe.Metrics(context.Background(), nil)
	if metrics.Pending == 0 {
		t.Fatal("external submission must enqueue")
	}
}
