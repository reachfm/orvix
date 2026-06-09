package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestProductionCandidateIntegratedStorageLoad(t *testing.T) {
	if os.Getenv("ORVIX_PRODUCTION_CANDIDATE_LOAD") != "1" {
		t.Skip("set ORVIX_PRODUCTION_CANDIDATE_LOAD=1 to run the production-candidate volume harness")
	}

	const (
		mailboxes             = 100
		messagesPerMailbox    = 100
		attachmentsPerMessage = 5
		totalMessages         = mailboxes * messagesPerMailbox
		totalAttachments      = totalMessages * attachmentsPerMessage
	)

	ctx := context.Background()
	_, store := testStore(t)
	attachmentRoot := filepath.Join(t.TempDir(), "attachments")

	var startMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&startMem)
	startGoroutines := runtime.NumGoroutine()
	start := time.Now()

	for mailboxID := uint(1); mailboxID <= mailboxes; mailboxID++ {
		if err := store.EnsureMailboxStorage(ctx, mailboxID, 1, 1, nil); err != nil {
			t.Fatalf("ensure mailbox %d: %v", mailboxID, err)
		}
		inbox, err := store.Folders.GetByPath(ctx, mailboxID, "INBOX", nil)
		if err != nil || inbox == nil {
			t.Fatalf("get inbox mailbox %d: %v", mailboxID, err)
		}

		for i := 0; i < messagesPerMailbox; i++ {
			msg := makeMessage(mailboxID, inbox.ID, 1, 1)
			msg.Subject = fmt.Sprintf("candidate mailbox=%d message=%d", mailboxID, i)
			body := []byte(fmt.Sprintf("From: sender@example.com\r\nTo: user%d@example.com\r\nSubject: %s\r\n\r\nbody", mailboxID, msg.Subject))
			if err := store.StoreMessage(ctx, msg, body, nil); err != nil {
				t.Fatalf("store mailbox=%d message=%d: %v", mailboxID, i, err)
			}
			for a := 0; a < attachmentsPerMessage; a++ {
				dir := filepath.Join(attachmentRoot, fmt.Sprintf("%d", mailboxID), fmt.Sprintf("%d", msg.ID))
				if err := os.MkdirAll(dir, 0750); err != nil {
					t.Fatalf("attachment dir: %v", err)
				}
				path := filepath.Join(dir, fmt.Sprintf("att-%d.bin", a))
				if err := os.WriteFile(path, []byte{byte(a)}, 0640); err != nil {
					t.Fatalf("attachment file: %v", err)
				}
				att := &Attachment{
					MessageID:   msg.ID,
					Filename:    fmt.Sprintf("att-%d.bin", a),
					ContentType: "application/octet-stream",
					SizeBytes:   1,
					StoragePath: path,
				}
				if err := store.Attachments.Create(ctx, att, nil); err != nil {
					t.Fatalf("attachment row mailbox=%d message=%d attachment=%d: %v", mailboxID, i, a, err)
				}
			}
		}
	}

	writeElapsed := time.Since(start)

	var wg sync.WaitGroup
	errs := make(chan error, mailboxes)
	for mailboxID := uint(1); mailboxID <= mailboxes; mailboxID++ {
		wg.Add(1)
		go func(mailboxID uint) {
			defer wg.Done()
			folders, err := store.Folders.ListByMailbox(ctx, mailboxID, nil)
			if err != nil {
				errs <- err
				return
			}
			var inboxID uint
			for _, f := range folders {
				if f.Path == "INBOX" {
					inboxID = f.ID
					break
				}
			}
			msgs, _, err := store.Messages.List(ctx, MessageFilter{MailboxID: mailboxID, FolderID: &inboxID, Limit: 10}, nil)
			if err != nil {
				errs <- err
				return
			}
			for _, msg := range msgs {
				if _, err := store.Attachments.CountByMessage(ctx, msg.ID, nil); err != nil {
					errs <- err
					return
				}
			}
		}(mailboxID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read failed: %v", err)
	}

	var msgCount, attCount int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_messages").Scan(&msgCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_attachments").Scan(&attCount); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if msgCount != totalMessages {
		t.Fatalf("expected %d messages, got %d", totalMessages, msgCount)
	}
	if attCount != totalAttachments {
		t.Fatalf("expected %d attachments, got %d", totalAttachments, attCount)
	}

	var endMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&endMem)
	t.Logf("production_candidate_load mailboxes=%d messages=%d attachments=%d write_elapsed=%s total_elapsed=%s goroutines_start=%d goroutines_end=%d heap_alloc_start=%d heap_alloc_end=%d heap_sys_start=%d heap_sys_end=%d db_wait_count=%d db_wait_duration=%s",
		mailboxes,
		msgCount,
		attCount,
		writeElapsed,
		time.Since(start),
		startGoroutines,
		runtime.NumGoroutine(),
		startMem.HeapAlloc,
		endMem.HeapAlloc,
		startMem.HeapSys,
		endMem.HeapSys,
		store.DB.Stats().WaitCount,
		store.DB.Stats().WaitDuration,
	)
}
