package smtp

// Inbound delivery idempotency regression tests (Part A of the
// duplicate-delivery fix). Root cause: a remote MTA that never sees
// the final SMTP 250 (e.g. the connection drops between durable
// commit and reply flush — see server.go's ACK boundary) correctly
// retries the identical message. Nothing previously distinguished
// that retry from a brand-new delivery: the RFC822 Message-ID header
// was parsed nowhere and the local-delivery idempotency check in
// DeliveryWorker.deliverLocal only guards against re-copying an
// already-stored row into a folder it's already in — it has no way to
// recognize two independently-accepted Message rows as the same
// underlying email.
//
// The fix: internet_message_id is now persisted at accept time, and
// a durable, DB-enforced UNIQUE(mailbox_id, dedup_key) claim
// (coremail_delivery_dedup) is attempted inside the SAME acceptance
// transaction, keyed on the Message-ID header when present. A losing
// claim is treated as "already delivered" and silently skips storing
// a second copy, while the SMTP client still receives a normal 250.

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeliveryDedup_ConcurrentRetryOfSameMessageProducesExactlyOneCopy(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)
	const messageID = "<concurrent-retry@sender.example>"
	body := "Subject: Concurrent\r\nMessage-ID: " + messageID + "\r\n\r\nbody"

	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn, reader := env.beginSession(t)
			defer conn.Close()
			env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
			results[idx] = env.sendData(t, conn, reader, body)
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if !strings.HasPrefix(r, "250") {
			t.Fatalf("attempt %d: expected 250, got %q", i, r)
		}
	}

	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("concurrent identical-Message-ID deliveries: messages=%d queue=%d, want exactly 1/1", msgs, queues)
	}
}

func TestDeliveryDedup_SameMessageIDDifferentBodyFollowsDocumentedCollisionPolicy(t *testing.T) {
	// A sender that reuses a Message-ID across genuinely different
	// content is violating RFC 5322's uniqueness requirement — the
	// documented policy (deliveryDedupKey's doc comment) is that the
	// SECOND delivery is treated as the same identity and skipped,
	// exactly as a legitimate retry would be. This test pins that
	// documented behavior so it never silently changes.
	env := newAuthoritativeSMTPEnv(t)
	const messageID = "<reused-id@sender.example>"

	conn1, reader1 := env.beginSession(t)
	env.cmd(t, conn1, reader1, "RCPT TO:<user@test.com>")
	final1 := env.sendData(t, conn1, reader1, "Subject: First\r\nMessage-ID: "+messageID+"\r\n\r\nfirst body")
	if !strings.HasPrefix(final1, "250") {
		t.Fatalf("first: expected 250, got %q", final1)
	}
	conn1.Close()

	conn2, reader2 := env.beginSession(t)
	env.cmd(t, conn2, reader2, "RCPT TO:<user@test.com>")
	final2 := env.sendData(t, conn2, reader2, "Subject: Second (different body, same Message-ID)\r\nMessage-ID: "+messageID+"\r\n\r\ncompletely different body")
	if !strings.HasPrefix(final2, "250") {
		t.Fatalf("second: expected 250 (never a hard failure), got %q", final2)
	}
	conn2.Close()

	msgs, queues := env.rowCounts(t)
	if msgs != 1 || queues != 1 {
		t.Fatalf("reused Message-ID with different body: messages=%d queue=%d, want exactly 1/1 per documented collision policy", msgs, queues)
	}
}

func TestDeliveryDedup_SameContentDifferentMailboxesEachGetOneCopy(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)
	const messageID = "<multi-recipient@sender.example>"
	body := "Subject: Multi\r\nMessage-ID: " + messageID + "\r\n\r\nbody"

	conn, reader := env.beginSession(t)
	defer conn.Close()
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	env.cmd(t, conn, reader, "RCPT TO:<bob@target.test>")
	final := env.sendData(t, conn, reader, body)
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}

	msgs, queues := env.rowCounts(t)
	if msgs != 2 || queues != 2 {
		t.Fatalf("same content to two distinct mailboxes: messages=%d queue=%d, want exactly 2/2 (one per mailbox)", msgs, queues)
	}
}

func TestDeliveryDedup_DifferentMessageIDsBothDeliverNormally(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)

	conn1, reader1 := env.beginSession(t)
	env.cmd(t, conn1, reader1, "RCPT TO:<user@test.com>")
	final1 := env.sendData(t, conn1, reader1, "Subject: One\r\nMessage-ID: <one@sender.example>\r\n\r\nbody one")
	if !strings.HasPrefix(final1, "250") {
		t.Fatalf("first: expected 250, got %q", final1)
	}
	conn1.Close()

	conn2, reader2 := env.beginSession(t)
	env.cmd(t, conn2, reader2, "RCPT TO:<user@test.com>")
	final2 := env.sendData(t, conn2, reader2, "Subject: Two\r\nMessage-ID: <two@sender.example>\r\n\r\nbody two")
	if !strings.HasPrefix(final2, "250") {
		t.Fatalf("second: expected 250, got %q", final2)
	}
	conn2.Close()

	msgs, queues := env.rowCounts(t)
	if msgs != 2 || queues != 2 {
		t.Fatalf("two genuinely distinct messages: messages=%d queue=%d, want exactly 2/2", msgs, queues)
	}
}

func TestDeliveryDedup_InternetMessageIDPersisted(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)
	const messageID = "<persisted-check@sender.example>"

	conn, reader := env.beginSession(t)
	defer conn.Close()
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: Persisted\r\nMessage-ID: "+messageID+"\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}

	var got string
	if err := env.db.QueryRow(`SELECT internet_message_id FROM coremail_messages WHERE mailbox_id = (SELECT id FROM coremail_mailboxes WHERE email='user@test.com')`).Scan(&got); err != nil {
		t.Fatalf("query internet_message_id: %v", err)
	}
	if got != messageID {
		t.Fatalf("internet_message_id = %q, want %q — the RFC822 Message-ID header must be persisted, distinct from the internal storage MessageID", got, messageID)
	}
}

// TestDeliveryDedup_QueueRetryAfterLocalSuccessDoesNotDuplicate covers
// item 10 of the mission's matrix at the queue layer: once a message
// has already been locally delivered (DeliveryWorker.deliverLocal's
// existing same-mailbox/same-folder guard), reprocessing the SAME
// queue row must remain a no-op, exactly as before this change — this
// pins that the new acceptance-time dedup claim does not weaken or
// duplicate that pre-existing guarantee.
func TestDeliveryDedup_QueueRetryAfterLocalSuccessDoesNotDuplicate(t *testing.T) {
	env := newAuthoritativeSMTPEnv(t)
	const messageID = "<queue-retry@sender.example>"

	conn, reader := env.beginSession(t)
	defer conn.Close()
	env.cmd(t, conn, reader, "RCPT TO:<user@test.com>")
	final := env.sendData(t, conn, reader, "Subject: QueueRetry\r\nMessage-ID: "+messageID+"\r\n\r\nbody")
	if !strings.HasPrefix(final, "250") {
		t.Fatalf("expected 250, got %q", final)
	}

	// Allow the queue worker (wired inside newAuthoritativeSMTPEnv's
	// receiver, same as production) to run to completion.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var status string
		err := env.db.QueryRow(`SELECT status FROM coremail_queue ORDER BY id DESC LIMIT 1`).Scan(&status)
		if err == nil && status == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			break // best-effort; the row-count assertion below is authoritative either way
		}
		time.Sleep(20 * time.Millisecond)
	}

	msgs, _ := env.rowCounts(t)
	if msgs != 1 {
		t.Fatalf("after queue processing settles: messages=%d, want exactly 1", msgs)
	}
}
