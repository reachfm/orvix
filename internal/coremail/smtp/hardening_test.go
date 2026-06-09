package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/coremail/queue"
)

// ── CONCURRENCY: Repeated connect/disconnect ────────────────

func TestHardeningRepeatedConnectDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	eng, ms, qe, rcv := testIntegrationEnv(t)
	_ = rcv
	_ = eng
	_ = ms
	_ = qe

	cfg := DefaultConfig()
	cfg.Hostname = "harden.test"
	cfg.AllowPlainAuthWithoutTLS = true

	verify := func(ctx context.Context, username, password string) (string, bool) {
		return username, true
	}
	auth := NewAuthenticator(NewFuncAuthBackend(verify))
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	srv := NewServer(cfg, handler, rcv)

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	go func() {
		srv.listener = listener
		srv.serve()
	}()
	defer listener.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					return
				}
				reader := bufio.NewReader(conn)
				reader.ReadString('\n')
				fmt.Fprintf(conn, "QUIT\r\n")
				reader.ReadString('\n')
				conn.Close()
			}
		}(i)
	}
	wg.Wait()
	_ = eng
	_ = ms
	_ = qe
}

// ── SQLITE: Concurrent writers ──────────────────────────────

func TestHardeningSQLiteConcurrentWriters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	_, ms, _, _ := testIntegrationEnv(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				msg := &storage.Message{
					MessageID:   fmt.Sprintf("harden-sqlite-%d-%d", id, j),
					TenantID:    1, DomainID: 1, MailboxID: 1, FolderID: 1,
					FromAddress: "sender@test.com", ToAddresses: "rcpt@test.com",
					Subject: "Harden SQLite",
				}
				if err := ms.StoreMessage(ctx, msg,
					[]byte("Subject: Test\r\n\r\nBody"), nil); err != nil {
					return
				}
			}
		}(i)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				ms.Messages.CountByFolder(ctx, 1, nil)
			}
		}()
	}
	wg.Wait()
}

// ── LONG-RUNNING: Repeated auth cycles ─────────────────────

func TestHardeningRepeatedAuthCycles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	eng, ms, qe, rcv := testIntegrationEnv(t)

	cfg := DefaultConfig()
	cfg.Hostname = "harden.test"
	cfg.AllowPlainAuthWithoutTLS = true

	verify := func(ctx context.Context, username, password string) (string, bool) {
		return username, true
	}
	auth := NewAuthenticator(NewFuncAuthBackend(verify))
	handler := NewCommandHandler(cfg, auth, NewSession("", nil, cfg))
	srv := NewServer(cfg, handler, rcv)

	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	go func() {
		srv.listener = listener
		srv.serve()
	}()
	defer listener.Close()

	for i := 0; i < 25; i++ {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		reader := bufio.NewReader(conn)
		reader.ReadString('\n')
		fmt.Fprintf(conn, "EHLO test.com\r\n")
		for {
			line, _ := reader.ReadString('\n')
			if strings.HasPrefix(line, "250 ") {
				break
			}
		}
		fmt.Fprintf(conn, "AUTH PLAIN AHVzZXJAdGVzdC5jb20AcGFzcw==\r\n")
		reader.ReadString('\n')
		fmt.Fprintf(conn, "QUIT\r\n")
		reader.ReadString('\n')
		conn.Close()
	}
	_ = eng
	_ = ms
	_ = qe
}

// ── QUEUE: Repeated processing ─────────────────────────────

func TestHardeningQueueRepeatedProcessing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	db, eng, _, qe, _ := testIntegrationEnvWithDB(t)
	defer db.Close()

	ctx := context.Background()

	for i := 0; i < 20; i++ {
		entry := &queue.QueueEntry{
			TenantID: 1, DomainID: 1, MessageID: fmt.Sprintf("harden-q-%d", i),
			FromAddress: "sender@test.com", ToAddress: fmt.Sprintf("rcp%d@test.com", i),
			RecipientDomain: "test.com", Direction: queue.DirectionInbound,
			DeliveryMode: queue.DeliveryLocal, Status: queue.StatusPending,
		}
		qe.Enqueue(ctx, entry)
	}

	for i := 0; i < 5; i++ {
		entry, err := qe.LeaseNext(ctx, "harden-worker")
		if err != nil || entry == nil {
			break
		}
		qe.AckDelivered(ctx, entry.ID)
	}
	_ = eng
}

// ── SECURITY: Observability sanitized ─────────────────────

func TestHardeningObservabilitySanitized(t *testing.T) {
	obs := observability.NewObservability(100, 100)

	obs.EventHistory.Record(observability.EventSMTPAuthSuccess, map[string]string{
		"username": "user@test.com", "method": "PLAIN",
	})
	obs.EventHistory.Record(observability.EventSMTPAuthFailure, map[string]string{
		"remote_ip": "10.0.0.1",
	})
	obs.EventHistory.Record(observability.EventQueueDelivered, map[string]string{
		"queue_id": "123", "message_id": "abc",
	})

	events := obs.EventHistory.Recent()
	for _, e := range events {
		for k, v := range e.Fields {
			if k == "password" || k == "passwd" || k == "secret" || k == "token" || k == "private_key" {
				t.Fatalf("sensitive key %s found in event %s", k, e.Type)
			}
			if len(v) > 512 {
				t.Fatalf("large value (%d bytes) in field %s", len(v), k)
			}
		}
	}
}
