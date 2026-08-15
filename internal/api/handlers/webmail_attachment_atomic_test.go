package handlers

// Send-level atomicity tests for attachment metadata: when attachment
// extraction fails inside the WebmailSend acceptance transaction, the
// whole send is rejected with 500 and ZERO durable side effects survive
// (no queue rows, no Sent message, no attachment rows, no send events,
// no committed quota usage).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// failAfterNAttachmentRepo fails every attachment Create after the Nth
// success, simulating a partial attachment-metadata write failure.
type failAfterNAttachmentRepo struct {
	storage.AttachmentRepository
	callCount int
	failAfter int
}

func (r *failAfterNAttachmentRepo) Create(ctx context.Context, a *storage.Attachment, tx interface{}) error {
	r.callCount++
	if r.callCount > r.failAfter {
		return fmt.Errorf("simulated attachment metadata failure after %d successes", r.failAfter)
	}
	return r.AttachmentRepository.Create(ctx, a, tx)
}

// multipartSendToHandler builds a multipart/form-data request with the
// given files and runs it through the real WebmailSend handler.
func multipartSendToHandler(t *testing.T, h *Handler, files [][]byte) (int, map[string]interface{}) {
	t.Helper()
	app := fiber.New()
	app.Post("/api/v1/webmail/send", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		return h.WebmailSend(c)
	})

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i, body := range files {
		part, err := mw.CreateFormFile("attachment", fmt.Sprintf("file%d.txt", i+1))
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(body); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	_ = mw.WriteField("to", "recipient@example.com")
	_ = mw.WriteField("subject", "Attachment atomicity")
	_ = mw.WriteField("body", "See attached")
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/webmail/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("multipart send: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestWebmailSendAttachmentMetadataFailureRollsBackWholeSend proves
// that an attachment-metadata write failure inside the acceptance
// transaction rejects the send with 500 and rolls back EVERYTHING: no
// queue rows, no Sent message, no attachment rows, no send events, no
// committed quota usage — the committed state can never hold a Sent
// message whose attachment metadata is missing or partial.
func TestWebmailSendAttachmentMetadataFailureRollsBackWholeSend(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	// Wire a store over the same database whose attachment repository
	// fails on the second Create: the first attachment's row is
	// inserted into the tx, then the second fails, and the tx must
	// roll back everything.
	store, err := storage.NewMailStore(sqlDB, filepath.Join(t.TempDir(), "ms"))
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	store.Attachments = &failAfterNAttachmentRepo{AttachmentRepository: store.Attachments, failAfter: 1}
	h.SetMailStore(store)

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("first attachment"), []byte("second attachment")})
	if status != fiber.StatusInternalServerError {
		t.Fatalf("attachment metadata failure: status=%d resp=%v, want 500", status, resp)
	}
	if code, _ := resp["code"].(string); code != "INTERNAL_ERROR" {
		t.Errorf("attachment metadata failure: code=%v, want INTERNAL_ERROR", code)
	}
	for _, key := range []string{"id", "message_id", "queue_ids", "enqueue_errors", "queued_count"} {
		if _, ok := resp[key]; ok {
			t.Errorf("attachment metadata failure: %q must not appear in response: %v", key, resp)
		}
	}

	assertZeroDurableSideEffects(t, sqlDB)
}

// TestWebmailSendAttachmentMetadataSuccessCommitsExactlyOnce is the
// success control for the same path: with a healthy attachment repo,
// the send commits the message AND exactly one attachment metadata row
// per file, and the response reports queued.
func TestWebmailSendAttachmentMetadataSuccessCommitsExactlyOnce(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("alpha"), []byte("beta")})
	if status != fiber.StatusCreated {
		t.Fatalf("healthy attachment send: status=%d resp=%v, want 201", status, resp)
	}
	id, _ := resp["id"].(float64)
	if id == 0 {
		t.Fatalf("healthy attachment send: missing message id: %v", resp)
	}
	var msgCount int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_messages`).Scan(&msgCount); err != nil {
		t.Fatalf("message count: %v", err)
	}
	if msgCount != 1 {
		t.Errorf("message rows = %d, want 1", msgCount)
	}
	var attCount int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_attachments WHERE message_id = ?`, uint(id)).Scan(&attCount); err != nil {
		t.Fatalf("attachment count: %v", err)
	}
	if attCount != 2 {
		t.Errorf("attachment rows = %d, want 2 (exactly once per file)", attCount)
	}
	var queued int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&queued); err != nil {
		t.Fatalf("queue count: %v", err)
	}
	if queued != 1 {
		t.Errorf("queue rows = %d, want 1", queued)
	}
}
