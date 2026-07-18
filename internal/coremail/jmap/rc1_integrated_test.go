package jmap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/imap"
	"github.com/orvix/orvix/internal/coremail/pop3"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/smtp"
	"github.com/orvix/orvix/internal/coremail/storage"
	_ "modernc.org/sqlite"
)

func TestRC1IntegratedSystemCertification(t *testing.T) {
	if os.Getenv("ORVIX_RC1_INTEGRATED") != "1" {
		t.Skip("set ORVIX_RC1_INTEGRATED=1 to run the RC1 integrated certification harness")
	}

	const (
		mailboxes             = 100
		messagesPerMailbox    = 100
		attachmentsPerMessage = 5
		totalMessages         = mailboxes * messagesPerMailbox
		totalAttachments      = totalMessages * attachmentsPerMessage
	)

	ctx := context.Background()
	env := newRC1IntegratedEnv(t)
	start := time.Now()
	startGoroutines := runtime.NumGoroutine()
	var startMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&startMem)

	mboxes := make([]*coremail.Mailbox, 0, mailboxes)
	_, first, err := env.eng.ProvisionDomain(ctx, "rc1-000.test", "smb", "user000@rc1-000.test", "pass", "RC1 User 000", 1)
	if err != nil {
		t.Fatalf("provision first mailbox: %v", err)
	}
	mboxes = append(mboxes, first)
	for i := 1; i < mailboxes; i++ {
		domain := fmt.Sprintf("rc1-%03d.test", i)
		email := fmt.Sprintf("user%03d@%s", i, domain)
		_, mbox, err := env.eng.ProvisionDomain(ctx, domain, "smb", email, "pass", fmt.Sprintf("RC1 User %03d", i), 1)
		if err != nil {
			t.Fatalf("provision mailbox %d: %v", i, err)
		}
		mboxes = append(mboxes, mbox)
	}

	attachmentRoot := filepath.Join(env.dir, "attachments")
	for i, mbox := range mboxes {
		if err := env.ms.EnsureMailboxStorage(ctx, mbox.ID, 1, mbox.DomainID, nil); err != nil {
			t.Fatalf("ensure mailbox %d: %v", mbox.ID, err)
		}
		inbox, err := env.ms.Folders.GetByPath(ctx, mbox.ID, "INBOX", nil)
		if err != nil || inbox == nil {
			t.Fatalf("inbox mailbox %d: %v", mbox.ID, err)
		}
		for msgIdx := 0; msgIdx < messagesPerMailbox; msgIdx++ {
			subject := fmt.Sprintf("rc1 mailbox=%03d message=%03d", i, msgIdx)
			rfc822 := []byte(fmt.Sprintf("From: sender@example.com\r\nTo: %s\r\nSubject: %s\r\n\r\ncertification body", mbox.Email, subject))
			msg := &storage.Message{
				TenantID:          1,
				DomainID:          mbox.DomainID,
				MailboxID:         mbox.ID,
				FolderID:          inbox.ID,
				InternetMessageID: fmt.Sprintf("<rc1-%03d-%03d@orvix.test>", i, msgIdx),
				Subject:           subject,
				FromAddress:       "sender@example.com",
				ToAddresses:       mbox.Email,
				ReceivedDate:      time.Now(),
			}
			if err := env.ms.StoreMessage(ctx, msg, rfc822, nil); err != nil {
				t.Fatalf("store message mailbox=%d message=%d: %v", i, msgIdx, err)
			}
			for attIdx := 0; attIdx < attachmentsPerMessage; attIdx++ {
				data := []byte{byte(i), byte(msgIdx), byte(attIdx)}
				sum := sha256.Sum256(data)
				dir := filepath.Join(attachmentRoot, fmt.Sprintf("%d", mbox.ID), fmt.Sprintf("%d", msg.ID))
				if err := os.MkdirAll(dir, 0750); err != nil {
					t.Fatalf("attachment dir: %v", err)
				}
				path := filepath.Join(dir, fmt.Sprintf("att-%d.bin", attIdx))
				if err := os.WriteFile(path, data, 0640); err != nil {
					t.Fatalf("attachment file: %v", err)
				}
				att := &storage.Attachment{
					MessageID:   msg.ID,
					Filename:    fmt.Sprintf("att-%d.bin", attIdx),
					ContentType: "application/octet-stream",
					SizeBytes:   int64(len(data)),
					SHA256:      hex.EncodeToString(sum[:]),
					StoragePath: path,
				}
				if err := env.ms.Attachments.Create(ctx, att, nil); err != nil {
					t.Fatalf("attachment row mailbox=%d message=%d attachment=%d: %v", i, msgIdx, attIdx, err)
				}
			}
		}
	}

	smtpAddr := startRC1SMTP(t, env)
	imapAddr := startRC1IMAP(t, env)
	pop3Addr := startRC1POP3(t, env)
	jmapAddr := startRC1JMAP(t, env)
	adminAddr := startRC1Admin(t, env)

	smtpReceive(t, smtpAddr, "sender@outside.test", first.Email, "RC1 SMTP receive")
	imapFetchContains(t, imapAddr, first.Email, "pass", "RC1 SMTP receive")
	pop3RetrContains(t, pop3Addr, first.Email, "pass", "RC1 SMTP receive")
	jmapSessionOK(t, jmapAddr, first.Email, "pass")
	jmapEmailQueryOK(t, jmapAddr, first.Email, "pass", fmt.Sprintf("%d", first.ID))
	jmapUploadOK(t, jmapAddr, first.Email, "pass", fmt.Sprintf("%d", first.ID))
	adminLoginAndQueueOK(t, adminAddr, first.Email, "pass")

	deliveryMsg, deliveryEntry := seedLocalDelivery(t, env, first)
	worker := delivery.NewDeliveryWorker(env.qe, env.ms, delivery.NewFakeResolver(), delivery.NewSMTPTransport(delivery.DefaultTransportConfig()), "rc1-000.test", "rc1-worker")
	worker.Recovery = nil
	processed, err := worker.ProcessAll(ctx)
	if err != nil {
		t.Fatalf("delivery worker process: %v", err)
	}
	if processed < 1 {
		t.Fatalf("expected delivery worker to process at least one item")
	}
	stored, _, err := env.ms.LoadMessageByMessageID(ctx, deliveryMsg.MessageID)
	if err != nil || stored == nil {
		t.Fatalf("delivery source message missing after queue processing: %v", err)
	}
	entry, err := env.qe.Repo.Get(ctx, deliveryEntry.ID, nil)
	if err != nil || entry == nil || entry.Status != queue.StatusDelivered {
		t.Fatalf("delivery queue entry not delivered: entry=%v err=%v", entry, err)
	}

	var msgCount, attCount int
	if err := env.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages").Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := env.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_attachments").Scan(&attCount); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if msgCount < totalMessages {
		t.Fatalf("expected at least %d messages, got %d", totalMessages, msgCount)
	}
	if attCount != totalAttachments {
		t.Fatalf("expected %d attachments, got %d", totalAttachments, attCount)
	}

	var endMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&endMem)
	t.Logf("rc1_integrated mailboxes=%d messages=%d attachments=%d smtp=%s imap=%s pop3=%s jmap=%s admin=%s elapsed=%s goroutines_start=%d goroutines_end=%d heap_alloc_start=%d heap_alloc_end=%d db_wait_count=%d db_wait_duration=%s",
		mailboxes,
		msgCount,
		attCount,
		smtpAddr,
		imapAddr,
		pop3Addr,
		jmapAddr,
		adminAddr,
		time.Since(start),
		startGoroutines,
		runtime.NumGoroutine(),
		startMem.HeapAlloc,
		endMem.HeapAlloc,
		env.db.Stats().WaitCount,
		env.db.Stats().WaitDuration,
	)
}

type rc1IntegratedEnv struct {
	dir string
	db  *sql.DB
	eng *coremail.Engine
	ms  *storage.MailStore
	qe  *queue.QueueEngine
}

func newRC1IntegratedEnv(t *testing.T) *rc1IntegratedEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "rc1.db")+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range coremailTables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("coremail table: %v", err)
		}
	}
	for _, stmt := range storage.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("storage table: %v", err)
		}
	}
	for _, stmt := range queue.Tables() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("queue table: %v", err)
		}
	}
	for _, stmt := range storage.Indexes() {
		_, _ = db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		_, _ = db.Exec(stmt)
	}
	eng := coremail.NewEngine(coremail.EngineConfig{DB: db, AuthCfg: coremail.DefaultAuthConfig()})
	ms, err := storage.NewMailStore(db, filepath.Join(dir, "mailstore"))
	if err != nil {
		t.Fatalf("mailstore: %v", err)
	}
	return &rc1IntegratedEnv{dir: dir, db: db, eng: eng, ms: ms, qe: queue.NewQueueEngine(db)}
}

func startRC1SMTP(t *testing.T, env *rc1IntegratedEnv) string {
	t.Helper()
	cfg := smtp.DefaultConfig()
	cfg.Hostname = "mx.rc1.test"
	cfg.AllowPlainAuthWithoutTLS = true
	cfg.RequireAuthForSubmission = false
	auth := smtp.NewAuthenticator(smtp.NewFuncAuthBackend(func(ctx context.Context, username, password string) (string, bool) {
		mbox, err := env.eng.Auth.AuthenticateMailbox(ctx, username, password)
		return username, err == nil && mbox != nil
	}))
	handler := smtp.NewCommandHandler(cfg, auth, smtp.NewSession("", nil, cfg))
	rcv := smtp.NewReceiver(env.eng, env.ms, env.qe, cfg)
	srv := smtp.NewServer(cfg, handler, rcv)
	srv.SetLocalDomainChecker(func(ctx context.Context, domain string) (bool, error) {
		dom, err := env.eng.Domains.GetByName(ctx, domain, nil)
		return dom != nil && dom.Status == coremail.DomainActive, err
	})
	srv.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		targets, err := env.eng.Auth.ResolveAddress(ctx, address)
		return err == nil && len(targets) > 0, err
	}
	addr := reserveTCPAddr(t)
	go func() { _ = srv.ListenAndServe(addr) }()
	waitTCP(t, addr)
	t.Cleanup(func() { _ = srv.Stop() })
	return addr
}

func startRC1IMAP(t *testing.T, env *rc1IntegratedEnv) string {
	t.Helper()
	auth := imap.NewAuthenticator()
	auth.SetEngine(&rc1IMAPAuth{service: env.eng.Auth})
	srv := imap.NewServer(imap.DefaultConfig(), env.ms, auth)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("imap listen: %v", err)
	}
	srv.SetListener(ln)
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

type rc1IMAPAuth struct {
	service *coremail.AuthService
}

func (a *rc1IMAPAuth) AuthenticateMailbox(ctx interface{}, username, password string) (interface{}, error) {
	return a.service.AuthenticateMailbox(context.Background(), username, password)
}

func startRC1POP3(t *testing.T, env *rc1IntegratedEnv) string {
	t.Helper()
	srv := pop3.NewServer(pop3.DefaultConfig(), env.ms, pop3.NewAuthenticator(&rc1POP3Auth{eng: env.eng}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pop3 listen: %v", err)
	}
	srv.SetListener(ln)
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

type rc1POP3Auth struct {
	eng *coremail.Engine
}

func (a *rc1POP3Auth) Authenticate(username, password string) (uint, bool) {
	mbox, err := a.eng.Auth.AuthenticateMailbox(context.Background(), username, password)
	if err != nil || mbox == nil {
		return 0, false
	}
	return mbox.ID, true
}

func startRC1JMAP(t *testing.T, env *rc1IntegratedEnv) string {
	t.Helper()
	srv := NewServer(env.eng)
	srv.SetMailStore(env.ms)
	srv.SetAllowedOrigins([]string{"https://webmail.rc1.test"})
	srv.Hostname = "jmap.rc1.test"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("jmap listen: %v", err)
	}
	addr := ln.Addr().String()
	go func() {
		srv.srv = &http.Server{Addr: addr, Handler: srv.withMiddleware(srv.mux)}
		_ = srv.srv.Serve(ln)
	}()
	waitTCP(t, addr)
	t.Cleanup(func() { srv.Stop() })
	return addr
}

func seedLocalDelivery(t *testing.T, env *rc1IntegratedEnv, mbox *coremail.Mailbox) (*storage.Message, *queue.QueueEntry) {
	t.Helper()
	ctx := context.Background()
	inbox, err := env.ms.Folders.GetByPath(ctx, mbox.ID, "INBOX", nil)
	if err != nil || inbox == nil {
		t.Fatalf("delivery inbox: %v", err)
	}
	msg := &storage.Message{
		TenantID:          1,
		DomainID:          mbox.DomainID,
		MailboxID:         mbox.ID,
		FolderID:          inbox.ID,
		InternetMessageID: "<rc1-delivery@orvix.test>",
		Subject:           "RC1 delivery source",
		FromAddress:       "sender@example.com",
		ToAddresses:       mbox.Email,
		ReceivedDate:      time.Now(),
	}
	body := []byte(fmt.Sprintf("From: sender@example.com\r\nTo: %s\r\nSubject: RC1 delivery source\r\n\r\ndelivery body", mbox.Email))
	if err := env.ms.StoreMessage(ctx, msg, body, nil); err != nil {
		t.Fatalf("delivery source store: %v", err)
	}
	entry := &queue.QueueEntry{
		TenantID:        1,
		DomainID:        mbox.DomainID,
		MailboxID:       &mbox.ID,
		MessageID:       msg.MessageID,
		FromAddress:     "sender@example.com",
		ToAddress:       mbox.Email,
		RecipientDomain: "rc1-000.test",
		Direction:       queue.DirectionInbound,
		DeliveryMode:    queue.DeliveryLocal,
	}
	if err := env.qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue delivery: %v", err)
	}
	return msg, entry
}

func smtpReceive(t *testing.T, addr, from, to, body string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("smtp dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "EHLO rc1.test\r\n")
	_, _ = reader.ReadString('\n')
	for {
		line, _ := reader.ReadString('\n')
		if strings.HasPrefix(line, "250 ") {
			break
		}
	}
	fmt.Fprintf(conn, "MAIL FROM:<%s>\r\n", from)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "RCPT TO:<%s>\r\n", to)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "DATA\r\n")
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "From: %s\r\nTo: %s\r\nSubject: RC1 SMTP\r\n\r\n%s\r\n.\r\n", from, to, body)
	resp, _ := reader.ReadString('\n')
	if !strings.Contains(resp, "250") {
		t.Fatalf("smtp data failed: %s", resp)
	}
}

func imapFetchContains(t *testing.T, addr, username, password, expected string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("imap dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "A1 LOGIN %s %s\r\n", username, password)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "A2 SELECT INBOX\r\n")
	exists := ""
	for {
		line, _ := reader.ReadString('\n')
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "*" && strings.EqualFold(fields[2], "EXISTS") {
			exists = fields[1]
		}
		if strings.Contains(line, "A2 OK") {
			break
		}
	}
	if strings.TrimSpace(exists) == "" {
		t.Fatalf("imap SELECT did not return EXISTS count")
	}
	fmt.Fprintf(conn, "A3 FETCH %s BODY[TEXT]\r\n", strings.TrimSpace(exists))
	var resp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		resp.WriteString(line)
		if strings.Contains(line, "A3 OK") {
			break
		}
	}
	if !strings.Contains(resp.String(), expected) {
		t.Fatalf("imap fetch missing %q in response: %s", expected, resp.String())
	}
}

func pop3RetrContains(t *testing.T, addr, username, password, expected string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("pop3 dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "USER %s\r\n", username)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "PASS %s\r\n", password)
	_, _ = reader.ReadString('\n')
	fmt.Fprintf(conn, "STAT\r\n")
	stat, _ := reader.ReadString('\n')
	fields := strings.Fields(stat)
	if len(fields) < 2 || !strings.HasPrefix(stat, "+OK") {
		t.Fatalf("pop3 STAT failed: %s", stat)
	}
	fmt.Fprintf(conn, "RETR %s\r\n", fields[1])
	var resp strings.Builder
	for {
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) == "." {
			break
		}
		resp.WriteString(line)
	}
	if !strings.Contains(resp.String(), expected) {
		t.Fatalf("pop3 retr missing %q", expected)
	}
}

func jmapSessionOK(t *testing.T, addr, username, password string) {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/jmap/session", addr), nil)
	req.Header.Set("Authorization", "Basic "+basic(username, password))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("jmap session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("jmap session HTTP %d: %s", resp.StatusCode, string(body))
	}
}

func jmapEmailQueryOK(t *testing.T, addr, username, password, accountID string) {
	t.Helper()
	body := map[string]interface{}{
		"using": []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail"},
		"methodCalls": []interface{}{[]interface{}{"Email/query", map[string]interface{}{
			"accountId": accountID,
			"limit":     5,
		}, "c1"}},
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/jmap/api", addr), bytes.NewReader(data))
	req.Header.Set("Authorization", "Basic "+basic(username, password))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("jmap email query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("jmap email query HTTP %d: %s", resp.StatusCode, string(raw))
	}
}

func jmapUploadOK(t *testing.T, addr, username, password, accountID string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "rc1.txt")
	if err != nil {
		t.Fatalf("upload part: %v", err)
	}
	_, _ = part.Write([]byte("rc1 upload"))
	_ = writer.Close()
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://%s/jmap/upload/%s", addr, accountID), &body)
	req.Header.Set("Authorization", "Basic "+basic(username, password))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("jmap upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("jmap upload HTTP %d: %s", resp.StatusCode, string(raw))
	}
}

func adminLoginAndQueueOK(t *testing.T, addr, username, password string) {
	t.Helper()
	body := bytes.NewBufferString(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
	resp, err := http.Post(fmt.Sprintf("http://%s/admin/login", addr), "application/json", body)
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("admin login HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "admin_session" {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatalf("admin login did not return session cookie")
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/admin/queue/summary", addr), nil)
	req.AddCookie(cookie)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin queue summary: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp2.Body)
		t.Fatalf("admin queue summary HTTP %d: %s", resp2.StatusCode, string(raw))
	}
}

func reserveTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitTCP(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not accept tcp connections: %s", addr)
}

func basic(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}
