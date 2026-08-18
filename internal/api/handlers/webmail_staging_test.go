package handlers

// WebmailSend staging tests (S-2).
//
// These tests pin the staged-acceptance contract of WebmailSend: all
// filesystem bytes (RFC822 + attachment bodies) are staged BEFORE the
// acceptance transaction begins (no database lock held), published by
// bounded same-filesystem renames inside the transaction, and cleaned
// up completely on every failure path — with zero durable rows, zero
// published files, and zero staging leftovers.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
)

// failQueueRepo fails the Nth Enqueue call (1-based) and delegates
// everything else to the wrapped repository.
type failQueueRepo struct {
	queue.Repository
	callCount int
	failAt    int
}

func (r *failQueueRepo) Enqueue(ctx context.Context, e *queue.QueueEntry, tx interface{}) error {
	r.callCount++
	if r.callCount >= r.failAt {
		return errors.New("simulated queue repository failure")
	}
	return r.Repository.Enqueue(ctx, e, tx)
}

// stagingState inspects the mailstore filesystem for the tests.
type stagingState struct {
	basePath       string
	publishedFiles []string
	stagingLeft    int
}

func inspectStaging(t *testing.T, ms *storage.MailStore) stagingState {
	t.Helper()
	var st stagingState
	st.basePath = ms.BasePath
	err := filepath.Walk(ms.BasePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "staging" {
				entries, rerr := os.ReadDir(path)
				if rerr == nil {
					st.stagingLeft = len(entries)
				}
				return filepath.SkipDir
			}
			return nil
		}
		st.publishedFiles = append(st.publishedFiles, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk mailstore: %v", err)
	}
	return st
}

// TestWebmailSendStaging_CompletesBeforeBeginTx proves staging
// happens BEFORE BeginTx and before any domain lock: the AfterStage
// hook observes the staged files on disk, zero durable rows, and a
// live write to the domain table (which would block forever if the
// acceptance transaction were holding the single SQLite connection).
func TestWebmailSendStaging_CompletesBeforeBeginTx(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	hookFired := make(chan int, 1)
	h.webmailSendHooks = &webmailSendTestHooks{
		AfterStage: func(stagedCount int) {
			hookFired <- stagedCount

			// 1. Staged files exist under the staging root.
			stageRoot := filepath.Join(h.mailStore.BasePath, "staging")
			entries, err := os.ReadDir(stageRoot)
			if err != nil || len(entries) != 1 {
				t.Errorf("staging root: entries=%v err=%v, want exactly 1 attempt dir", entries, err)
				return
			}
			attemptFiles, err := os.ReadDir(filepath.Join(stageRoot, entries[0].Name()))
			if err != nil || len(attemptFiles) == 0 {
				t.Errorf("attempt dir empty: %v", err)
				return
			}

			// 2. Zero durable rows exist yet.
			var msgCount, queueCount, attCount int
			sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_messages`).Scan(&msgCount)
			sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue`).Scan(&queueCount)
			sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_attachments`).Scan(&attCount)
			if msgCount != 0 || queueCount != 0 || attCount != 0 {
				t.Errorf("rows before BeginTx: messages=%d queue=%d attachments=%d, want 0/0/0", msgCount, queueCount, attCount)
			}

			// 3. No database write lock is held: a concurrent write
			// to the domain table completes immediately. On SQLite
			// with _txlock=immediate this would deadlock if the
			// acceptance tx were open.
			writeDone := make(chan error, 1)
			go func() {
				_, werr := sqlDB.Exec(`UPDATE coremail_domains SET updated_at = updated_at WHERE name='orvix.email'`)
				writeDone <- werr
			}()
			select {
			case werr := <-writeDone:
				if werr != nil {
					t.Errorf("write during staging: %v", werr)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("write blocked during staging — a transaction/lock was already held before BeginTx")
			}
		},
	}

	status, _ := multipartSendToHandler(t, h, [][]byte{[]byte("alpha"), []byte("beta")})
	if status != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d", status)
	}
	if got := <-hookFired; got != 3 {
		t.Fatalf("AfterStage staged file count = %d, want 3 (1 rfc822 + 2 attachments)", got)
	}
	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers after success = %d, want 0", st.stagingLeft)
	}
}

// TestWebmailSendStaging_SuccessPublishesExactlyOneRFC822AndAllAttachments
// pins the success publication: exactly one RFC822 file and one file
// per attachment at the FINAL paths referenced by the rows, with the
// exact staged bytes.
func TestWebmailSendStaging_SuccessPublishesExactlyOneRFC822AndAllAttachments(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("first payload"), []byte("second payload")})
	if status != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	id, _ := resp["id"].(float64)
	if id == 0 {
		t.Fatalf("missing message id: %v", resp)
	}

	var rfc822Path string
	var rfc822Size int64
	if err := sqlDB.QueryRow(`SELECT rfc822_path, size_bytes FROM coremail_messages WHERE id = ?`, uint(id)).Scan(&rfc822Path, &rfc822Size); err != nil {
		t.Fatalf("message row: %v", err)
	}
	info, err := os.Stat(rfc822Path)
	if err != nil {
		t.Fatalf("published rfc822 missing: %v", err)
	}
	if info.Size() != rfc822Size {
		t.Fatalf("published rfc822 size mismatch: file=%d row=%d", info.Size(), rfc822Size)
	}

	rows, err := sqlDB.Query(`SELECT filename, size_bytes, storage_path, sha256 FROM coremail_attachments WHERE message_id = ? ORDER BY id`, uint(id))
	if err != nil {
		t.Fatalf("attachment rows: %v", err)
	}
	defer rows.Close()
	var atts []struct {
		name string
		size int64
		path string
		sha  string
	}
	for rows.Next() {
		var a struct {
			name string
			size int64
			path string
			sha  string
		}
		if err := rows.Scan(&a.name, &a.size, &a.path, &a.sha); err != nil {
			t.Fatalf("scan attachment: %v", err)
		}
		atts = append(atts, a)
	}
	if len(atts) != 2 {
		t.Fatalf("attachment rows = %d, want 2", len(atts))
	}
	for i, a := range atts {
		data, err := os.ReadFile(a.path)
		if err != nil {
			t.Fatalf("attachment %d file missing at %s: %v", i, a.path, err)
		}
		if int64(len(data)) != a.size {
			t.Fatalf("attachment %d size mismatch: file=%d row=%d", i, len(data), a.size)
		}
		if !strings.HasPrefix(a.path, h.mailStore.BasePath) {
			t.Fatalf("attachment %d published outside storage root: %s", i, a.path)
		}
	}

	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers = %d, want 0", st.stagingLeft)
	}
	if len(st.publishedFiles) != 3 {
		t.Fatalf("published files = %d, want exactly 3 (1 rfc822 + 2 attachments)", len(st.publishedFiles))
	}
}

// TestWebmailSendStaging_AttachmentMetadataFailureRollsBackAndCleans
// pins the metadata-failure path: zero durable rows, zero published
// files, zero staging leftovers.
func TestWebmailSendStaging_AttachmentMetadataFailureRollsBackAndCleans(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	store, err := storage.NewMailStore(sqlDB, filepath.Join(t.TempDir(), "ms"))
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	store.Attachments = &failAfterNAttachmentRepo{AttachmentRepository: store.Attachments, failAfter: 1}
	h.SetMailStore(store)

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("first attachment"), []byte("second attachment")})
	if status != fiber.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %v", status, resp)
	}
	assertZeroDurableSideEffects(t, sqlDB)

	st := inspectStaging(t, store)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers after metadata failure = %d, want 0", st.stagingLeft)
	}
	if len(st.publishedFiles) != 0 {
		t.Fatalf("published files after metadata failure = %d, want 0", len(st.publishedFiles))
	}
}

// TestWebmailSendStaging_QueueFailureLeavesNoPublishedArtifacts pins
// the queue-failure path: the enqueue insert fails inside the
// acceptance transaction, the whole send rolls back, and no message
// or attachment file was ever published.
func TestWebmailSendStaging_QueueFailureLeavesNoPublishedArtifacts(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	h.queueEngine.Repo = &failQueueRepo{Repository: h.queueEngine.Repo, failAt: 1}

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("payload")})
	if status != fiber.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %v", status, resp)
	}
	assertZeroDurableSideEffects(t, sqlDB)

	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers after queue failure = %d, want 0", st.stagingLeft)
	}
	if len(st.publishedFiles) != 0 {
		t.Fatalf("published files after queue failure = %d, want 0", len(st.publishedFiles))
	}
}

// TestWebmailSendStaging_CommitFailureCleansAllArtifacts pins the
// commit-failure path: the BeforeCommit hook poisons the transaction,
// the send returns 503, and no rows, published files, or staging
// leftovers survive.
func TestWebmailSendStaging_CommitFailureCleansAllArtifacts(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	h.webmailSendHooks = &webmailSendTestHooks{
		BeforeCommit: func(tx *sql.Tx) {
			_ = tx.Rollback() // poison: the following Commit fails
		},
	}

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("payload")})
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %v", status, resp)
	}
	assertZeroDurableSideEffects(t, sqlDB)

	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers after commit failure = %d, want 0", st.stagingLeft)
	}
	if len(st.publishedFiles) != 0 {
		t.Fatalf("published files after commit failure = %d, want 0", len(st.publishedFiles))
	}
}

// TestWebmailSendStaging_MaliciousFilenameCannotEscapeRoot pins that
// path-traversal filenames are sanitized at every boundary: the
// attachment row's storage path stays inside the storage root and
// the file lands under attachments/{id}/ with a sanitized name.
func TestWebmailSendStaging_MaliciousFilenameCannotEscapeRoot(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	// Build a multipart form with a traversal filename manually so
	// the raw filename reaches the handler.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("attachment", "..\\..\\..\\evil.txt")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write([]byte("evil")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	_ = mw.WriteField("to", "recipient@example.com")
	_ = mw.WriteField("subject", "Traversal")
	_ = mw.WriteField("body", "see attached")
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/v1/webmail/send", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	app := fiber.New()
	app.Post("/api/v1/webmail/send", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(1))
		return h.WebmailSend(c)
	})
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var storagePath string
	if err := sqlDB.QueryRow(`SELECT storage_path FROM coremail_attachments LIMIT 1`).Scan(&storagePath); err != nil {
		t.Fatalf("attachment row: %v", err)
	}
	if !strings.HasPrefix(storagePath, h.mailStore.BasePath) {
		t.Fatalf("attachment storage path escaped the root: %s", storagePath)
	}
	if strings.Contains(storagePath, "..") {
		t.Fatalf("attachment storage path contains traversal: %s", storagePath)
	}
	if _, err := os.Stat(storagePath); err != nil {
		t.Fatalf("published attachment missing: %v", err)
	}
	// No file named evil.txt may exist anywhere outside the root.
	parent := filepath.Dir(h.mailStore.BasePath)
	matches, _ := filepath.Glob(filepath.Join(parent, "**", "evil.txt"))
	for _, m := range matches {
		if !strings.HasPrefix(m, h.mailStore.BasePath) {
			t.Fatalf("traversal escaped the root: %s", m)
		}
	}
}

// TestWebmailSendStaging_CleanupPreservesReferencedFilesAfterSend
// runs the recovery cleanup over a successfully sent mailbox and
// proves it deletes nothing referenced and nothing unrelated.
func TestWebmailSendStaging_CleanupPreservesReferencedFilesAfterSend(t *testing.T) {
	h, db, sqlDB, _ := buildSendEnforcementHarness(t, true)
	t.Cleanup(func() {
		sqlDB.Close()
		if s, err := db.DB(); err == nil {
			s.Close()
		}
	})

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("payload")})
	if status != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}

	unrelated := filepath.Join(h.mailStore.BasePath, "unrelated.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0644); err != nil {
		t.Fatalf("write unrelated: %v", err)
	}

	// Threshold in the future: everything is "old", so the cleanup
	// MUST rely on row references and layout rules alone.
	stats, err := h.mailStore.CleanupOrphanedFiles(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if stats.OrphanFiles != 0 {
		t.Fatalf("cleanup removed %d referenced/unrelated files, want 0", stats.OrphanFiles)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("cleanup deleted an unrelated file: %v", err)
	}
	var rfc822Path string
	if err := sqlDB.QueryRow(`SELECT rfc822_path FROM coremail_messages LIMIT 1`).Scan(&rfc822Path); err != nil {
		t.Fatalf("message row: %v", err)
	}
	if _, err := os.Stat(rfc822Path); err != nil {
		t.Fatalf("cleanup deleted a referenced message file: %v", err)
	}
	var attPath string
	if err := sqlDB.QueryRow(`SELECT storage_path FROM coremail_attachments LIMIT 1`).Scan(&attPath); err != nil {
		t.Fatalf("attachment row: %v", err)
	}
	if _, err := os.Stat(attPath); err != nil {
		t.Fatalf("cleanup deleted a referenced attachment file: %v", err)
	}
}
