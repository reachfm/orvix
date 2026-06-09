package imap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

func TestHardeningIMAPRepeatedConnectDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, reader := opDial(t, addr)
			defer conn.Close()
			opLoginSelect(t, conn, reader)
			opSendCmd(t, conn, reader, fmt.Sprintf("A%d", id*10+3), "FETCH 1:* (FLAGS UID)")
			opSendCmd(t, conn, reader, fmt.Sprintf("A%d", id*10+4), "LOGOUT")
		}(i)
	}
	wg.Wait()
}

func TestHardeningIMAPSelectAfterExpunge(t *testing.T) {
	ms, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	ctx := context.TODO()
	_ = ms.EnsureMailboxStorage(ctx, 1, 1, 1, nil)
	folder, _ := ms.Folders.GetByPath(ctx, 1, "INBOX", nil)

	// Store several messages.
	for i := 1; i <= 5; i++ {
		msg := &storage.Message{
			MessageID: fmt.Sprintf("harden-imap-%d", i), TenantID: 1, DomainID: 1,
			MailboxID: 1, FolderID: folder.ID, Deleted: (i == 3 || i == 5),
			FromAddress: "a@test.com", ToAddresses: "b@test.com", Subject: fmt.Sprintf("%d", i),
		}
		ms.StoreMessage(ctx, msg, []byte(fmt.Sprintf("Subject: %d\r\n\r\n%d", i, i)), nil)
	}

	conn, reader := opDial(t, addr)
	defer conn.Close()
	opLoginSelect(t, conn, reader)

	// Expunge.
	opSendCmd(t, conn, reader, "A3", "EXPUNGE")

	// Re-select and verify state.
	sel := uidLoginSelect(t, conn, reader)
	if !strings.Contains(sel, "3 EXISTS") {
		t.Fatal("expected 3 messages after expunge (removed 2 of 5)")
	}
}

func TestHardeningIMAPConcurrentFetchSelect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping hardening test in short mode")
	}

	_, _, addr, cleanup := testIMAPServer(t)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, reader := opDial(t, addr)
			defer conn.Close()
			opLoginSelect(t, conn, reader)
			opSendCmd(t, conn, reader, "A3", "FETCH 1:* (UID FLAGS RFC822.SIZE INTERNALDATE)")
			time.Sleep(5 * time.Millisecond)
			opLoginSelect(t, conn, reader)
			opSendCmd(t, conn, reader, "A5", "LOGOUT")
		}()
	}
	wg.Wait()
}
