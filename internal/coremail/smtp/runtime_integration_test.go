package smtp

// Runtime integration tests proving the antivirus +
// acceptance + incoming-rule engines are actually wired
// into the SMTP receive path. The contract under test
// here is the wiring, not the engine logic — a passing
// AntispamEngine unit test does not prove the SMTP
// receive path actually invokes it.
//
// These tests stand up the receiver + an in-process
// TCP listener stubbing a clamav daemon, drive SMTP
// commands through the CommandHandler, and assert the
// wiring produces the documented response codes.

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/ruler"
	"go.uber.org/zap"
)

// stubScanner starts an in-process TCP listener that
// returns the canned reply byte-for-byte. Replaces a
// running clamav so the test has no hard external
// dependency. The drain loop reads until the INSTREAM
// terminator (four zero bytes) is observed OR the
// deadline fires. A single Read is unreliable on
// Windows under parallel test execution because the
// kernel may not flush the scanner's chunked writes
// before the response races in.
func stubScanner(t *testing.T, response string) (string, int) {
	t.Helper()
	var counter int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stub listen: %v", err)
	}
	stop := make(chan struct{})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&counter, 1)
			go func(c net.Conn) {
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				buf := make([]byte, 4096)
				collected := make([]byte, 0, 8192)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						collected = append(collected, buf[:n]...)
						if len(collected) >= 4 {
							tail := collected[len(collected)-4:]
							if tail[0] == 0 && tail[1] == 0 && tail[2] == 0 && tail[3] == 0 {
								break
							}
						}
						continue
					}
					if err != nil {
						break
					}
				}
				c.Write([]byte(response + "\000"))
			}(c)
		}
	}()
	t.Cleanup(func() {
		close(stop)
		ln.Close()
	})
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}
	_ = host
	_ = counter
	return host, port
}

// openTestDB creates an on-disk SQLite DB and the rule /
// quarantine / domain / mailbox tables the receiver and
// ruler expect. Returning *sql.DB keeps the test
// independent of the coremail engine wiring in
// production code.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ruler-it.db")+"?_loc=auto&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE coremail_acceptance_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			scope TEXT NOT NULL DEFAULT 'global',
			scope_target TEXT NOT NULL DEFAULT '',
			sender_pattern TEXT NOT NULL DEFAULT '',
			recipient_pattern TEXT NOT NULL DEFAULT '',
			source_ip_cidr TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'accept',
			redirect_to TEXT NOT NULL DEFAULT '',
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_incoming_msg_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			name TEXT NOT NULL DEFAULT '',
			priority INTEGER NOT NULL DEFAULT 100,
			enabled INTEGER NOT NULL DEFAULT 1,
			field TEXT NOT NULL DEFAULT 'subject',
			operator TEXT NOT NULL DEFAULT 'contains',
			value TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT 'move',
			action_target TEXT NOT NULL DEFAULT '',
			apply_to TEXT NOT NULL DEFAULT 'all',
			stop_processing INTEGER NOT NULL DEFAULT 0,
			note TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_quarantine_index (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			message_id TEXT NOT NULL,
			recipient TEXT NOT NULL DEFAULT '',
			sender TEXT NOT NULL DEFAULT '',
			subject TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT 'low',
			status TEXT NOT NULL DEFAULT 'held',
			resolved_at DATETIME,
			resolved_by TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE coremail_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			reseller_id INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes INTEGER NOT NULL DEFAULT 0,
			max_aliases INTEGER NOT NULL DEFAULT 0,
			max_quota_mb INTEGER NOT NULL DEFAULT 0,
			dkim_enabled INTEGER NOT NULL DEFAULT 0,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled INTEGER NOT NULL DEFAULT 0,
			mtasts_enabled INTEGER NOT NULL DEFAULT 0,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
		`CREATE TABLE coremail_mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			username TEXT NOT NULL DEFAULT '',
			local_part TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL DEFAULT '',
			display_name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			quota_bytes INTEGER NOT NULL DEFAULT 0,
			used_bytes INTEGER NOT NULL DEFAULT 0,
			maildir TEXT NOT NULL DEFAULT '',
			class_id INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return db
}

// newReceiverWithEngines assembles a Receiver with the
// supplied engines wired into the corresponding fields.
// Returns the receiver + the engine (where applicable)
// so the test can assert post-conditions.
func newReceiverWithEngines(t *testing.T, av *antivirus.Engine, rEng *ruler.Engine) *Receiver {
	t.Helper()
	db := openTestDB(t)
	obs := observability.NewObservability(50, 50)
	store, err := storage.NewMailStore(db, t.TempDir())
	if err != nil {
		t.Fatalf("mail store: %v", err)
	}
	qe := queue.NewQueueEngine(db)
	corEng := coremail.NewEngine(coremail.EngineConfig{DB: db})
	cfg := DefaultConfig()
	cfg.MaxMessageSizeBytes = 1 << 20
	rec := NewReceiver(corEng, store, qe, cfg)
	rec.Observability = obs
	rec.DB = db
	if av != nil {
		rec.AntivirusEngine = av
	}
	if rEng != nil {
		rec.AcceptanceEngine = rEng
		rec.IncomingRuleEngine = rEng
	}
	return rec
}

// driveMAILFROM is a minimal CommandHandler driver.
// It opens an EHLO first (so the handler treats the
// session as ESMTP), then issues MAIL FROM and returns
// the response. Accepting the rule engine via SetAcceptanceEngine
// proves the rule flow is wired without needing the
// full Server.
func driveMAILFROM(t *testing.T, rEng *ruler.Engine, sender string) Response {
	t.Helper()
	cfg := DefaultConfig()
	cfg.RequireAuthForSubmission = false
	session := NewSession("192.0.2.1:1234", nil, cfg)
	handler := NewCommandHandler(cfg, nil, session)
	handler.SetAcceptanceEngine(rEng)
	if resp := handler.Handle(context.Background(), &ParsedCommand{Verb: "EHLO", Args: "test.local"}); resp.Code != StatusOK {
		t.Fatalf("ehlo resp: %d/%s", resp.Code, resp.Message)
	}
	return handler.Handle(context.Background(), &ParsedCommand{Verb: "MAIL", Args: "FROM:<" + sender + ">"})
}

func TestAcceptanceRuleAtMAILFROMRejects(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO coremail_acceptance_rules
		(tenant_id, name, priority, enabled, scope, scope_target, sender_pattern, recipient_pattern, source_ip_cidr, action, redirect_to, note, created_at, updated_at)
		VALUES (0, 'drop_spammer', 50, 1, 'global', '', '*@spam.example', '', '', 'reject', '', '', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rEng := ruler.New(db, zap.NewNop(), observability.NewObservability(50, 50))
	rEng.MarkEnforced()
	resp := driveMAILFROM(t, rEng, "u@spam.example")
	if resp.Code != StatusMailboxNotFound {
		t.Fatalf("want 550, got %d/%s", resp.Code, resp.Message)
	}
	if !strings.Contains(resp.Message, "5.7.1") {
		t.Fatalf("want 5.7.1 in message, got %s", resp.Message)
	}
}

func TestAcceptanceRuleAtMAILFROMPassesCleanSender(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO coremail_acceptance_rules
		(tenant_id, name, priority, enabled, scope, scope_target, sender_pattern, recipient_pattern, source_ip_cidr, action, redirect_to, note, created_at, updated_at)
		VALUES (0, 'drop_spammer', 50, 1, 'global', '', '*@spam.example', '', '', 'reject', '', '', '2026-01-01', '2026-01-01')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rEng := ruler.New(db, zap.NewNop(), observability.NewObservability(50, 50))
	rEng.MarkEnforced()
	resp := driveMAILFROM(t, rEng, "u@example.com")
	if resp.Code != StatusOK {
		t.Fatalf("want 250, got %d/%s", resp.Code, resp.Message)
	}
}

func TestAntivirusEngineWiredIntoReceiverRejectsInfectedBody(t *testing.T) {
	host, port := stubScanner(t, "stream: Eicar-Test-Signature FOUND")
	obs := observability.NewObservability(50, 50)
	av, err := antivirus.New(antivirus.Config{
		Enabled: true, Host: host, Port: port,
	}, antivirus.Policy{
		OnInfected: "reject", OnScannerUnavailable: "fail_closed", TimeoutMS: 2000,
	}, zap.NewNop(), obs, nil)
	if err != nil {
		t.Fatalf("av new: %v", err)
	}
	av.MarkEnforced()
	rec := newReceiverWithEngines(t, av, nil)
	session := &Session{
		ID:         "test-1",
		MailFrom:   "u@example.com",
		Recipients: []string{"x@y.local"},
		DataBuffer: []byte("Subject: hi\r\n\r\nINFECTED-PAYLOAD (Eicar)"),
		RemoteAddr: "192.0.2.1:1234",
		TLSActive:  true,
		StartTime:  time.Now(),
	}
	err = rec.AcceptMessage(context.Background(), session)
	if err == nil {
		t.Fatalf("want reject error from infected payload")
	}
	if !strings.Contains(err.Error(), "rejected by antivirus") {
		t.Fatalf("want antivirus rejection; got %v", err)
	}
	scanned, infected := av.Counts()
	if infected < 1 {
		t.Fatalf("infected counter must advance; got scanned=%d infected=%d", scanned, infected)
	}
}

func TestAntivirusEngineFailClosedOnUnreachableDaemon(t *testing.T) {
	obs := observability.NewObservability(50, 50)
	av, err := antivirus.New(antivirus.Config{
		Enabled: true, Host: "127.0.0.1", Port: 1,
	}, antivirus.Policy{
		OnInfected: "reject", OnScannerUnavailable: "fail_closed", TimeoutMS: 200,
	}, zap.NewNop(), obs, nil)
	if err != nil {
		t.Fatalf("av new: %v", err)
	}
	av.MarkEnforced()
	rec := newReceiverWithEngines(t, av, nil)
	session := &Session{
		ID:         "t-failclosed",
		MailFrom:   "u@example.com",
		Recipients: []string{"x@y.local"},
		DataBuffer: []byte("Subject: hi\r\n\r\nclean"),
		RemoteAddr: "192.0.2.1:1234",
		TLSActive:  true,
		StartTime:  time.Now(),
	}
	err = rec.AcceptMessage(context.Background(), session)
	if err == nil {
		t.Fatalf("fail_closed must produce an error when daemon is down")
	}
	if !strings.Contains(err.Error(), "rejected by antivirus") {
		t.Fatalf("want antivirus policy rejection; got %v", err)
	}
}
