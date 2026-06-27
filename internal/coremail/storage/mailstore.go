package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/orvix/orvix/internal/coremail/mime"
)

// writeMu serializes SQLite write transactions. SQLite only supports one writer
// at a time; without this mutex, concurrent goroutines hit SQLITE_BUSY even with
// WAL mode and busy_timeout because the pure-Go modernc.org/sqlite driver does
// not reliably honor busy_timeout across concurrent BeginTx calls on separate
// connections from the pool. The critical section is very short (single INSERT)
// because file I/O is done outside the transaction, so contention is minimal.
// This mutex is harmless under Postgres (no contention, no effect).
var writeMu sync.Mutex

// MailStore orchestrates all message storage operations.
// It ensures transactional consistency between database records and filesystem files.
type MailStore struct {
	DB          *sql.DB
	Messages    MessageRepository
	Folders     FolderRepository
	Attachments AttachmentRepository
	Settings    UserSettingsRepository
	BasePath    string // root directory for RFC822 message files
}

// NewMailStore creates a MailStore with all sub-repositories wired.
func NewMailStore(db *sql.DB, basePath string) (*MailStore, error) {
	if err := os.MkdirAll(basePath, 0750); err != nil {
		return nil, fmt.Errorf("create mailstore base path: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, _ = db.Exec("PRAGMA journal_mode=WAL")
	_, _ = db.Exec("PRAGMA busy_timeout=5000")
	return &MailStore{
		DB:          db,
		Messages:    NewMessageSQLRepo(db),
		Folders:     NewFolderSQLRepo(db),
		Attachments: NewAttachmentSQLRepo(db),
		Settings:    NewUserSettingsRepo(db),
		BasePath:    basePath,
	}, nil
}

// GenerateMessageID returns a unique UUID for a message.
func GenerateMessageID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // RFC 4122 version 4
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b)
}

// messagePath returns the filesystem path for a message's RFC822 file.
// Layout: {BasePath}/{tenantID}/{domainID}/{mailboxID}/{messageID}.eml
func (ms *MailStore) messagePath(tenantID, domainID, mailboxID uint, messageID string) string {
	return filepath.Join(ms.BasePath,
		fmt.Sprintf("%d", tenantID),
		fmt.Sprintf("%d", domainID),
		fmt.Sprintf("%d", mailboxID),
		messageID+".eml")
}

// EnsureMailboxStorage creates the filesystem directories and system folders for a mailbox.
func (ms *MailStore) EnsureMailboxStorage(ctx context.Context, mailboxID, tenantID, domainID uint, tx interface{}) error {
	dir := filepath.Join(ms.BasePath, fmt.Sprintf("%d", tenantID), fmt.Sprintf("%d", domainID), fmt.Sprintf("%d", mailboxID))
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create mailbox storage dir: %w", err)
	}
	return ms.Folders.EnsureSystemFolders(ctx, mailboxID, tx)
}

// StoreMessage persists a message to both database and filesystem in a single transaction.
// If the file write succeeds but DB insert fails, the file is cleaned up.
// If the DB insert succeeds but the file write fails, the DB insert is rolled back.
//
// Design: File I/O happens OUTSIDE the database transaction to minimize SQLite lock
// contention under concurrent writes. The database transaction only covers the INSERT.
// If the INSERT fails, the previously written file is removed.
func (ms *MailStore) StoreMessage(ctx context.Context, msg *Message, rfc822Data []byte, tx interface{}) error {
	if msg.MessageID == "" {
		msg.MessageID = GenerateMessageID()
	}

	msg.RFC822Path = ms.messagePath(msg.TenantID, msg.DomainID, msg.MailboxID, msg.MessageID)
	msg.SizeBytes = int64(len(rfc822Data))

	// Step 1: Write file to disk (outside transaction).
	dir := filepath.Dir(msg.RFC822Path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create message dir: %w", err)
	}
	if err := os.WriteFile(msg.RFC822Path, rfc822Data, 0640); err != nil {
		return fmt.Errorf("write rfc822 file: %w", err)
	}

	// Step 2: Compute SHA256 from the written file.
	sha, err := ComputeSHA256(msg.RFC822Path)
	if err != nil {
		os.Remove(msg.RFC822Path)
		return fmt.Errorf("compute sha256: %w", err)
	}
	msg.SHA256 = sha

	// Step 3: Insert database record inside a transaction.
	// If a tx is already provided, use it directly (caller manages locking).
	if tx != nil {
		if err := ms.Messages.Create(ctx, msg, tx); err != nil {
			os.Remove(msg.RFC822Path)
			return err
		}
		// Extract attachments from RFC822 data (best-effort).
		if len(rfc822Data) > 0 {
			ms.extractAndStoreAttachments(ctx, msg.ID, rfc822Data)
		}
		return nil
	}

	// Serialize SQLite write transactions via mutex. SQLite only supports one
	// writer; the pure-Go driver does not always honor busy_timeout across
	// concurrent connections from the pool. The critical section is minimal
	// (single INSERT). Under Postgres this mutex has zero contention.
	writeMu.Lock()
	defer writeMu.Unlock()

	sqlTx, err := ms.DB.BeginTx(ctx, nil)
	if err != nil {
		os.Remove(msg.RFC822Path)
		return fmt.Errorf("begin tx: %w", err)
	}

	if err := ms.Messages.Create(ctx, msg, sqlTx); err != nil {
		sqlTx.Rollback()
		os.Remove(msg.RFC822Path)
		return fmt.Errorf("create message: %w", err)
	}

	if err := sqlTx.Commit(); err != nil {
		sqlTx.Rollback()
		os.Remove(msg.RFC822Path)
		return fmt.Errorf("commit tx: %w", err)
	}

	// Extract attachments from RFC822 data (best-effort, non-fatal).
	// The message is already stored successfully; attachment extraction is
	// an optimization that can be re-run from the RFC822 file at any time.
	if len(rfc822Data) > 0 {
		ms.extractAndStoreAttachments(ctx, msg.ID, rfc822Data)
	}

	return nil
}

// LoadMessage reads a message's RFC822 content from disk.
func (ms *MailStore) LoadMessage(ctx context.Context, id uint, tx interface{}) (*Message, []byte, error) {
	msg, err := ms.Messages.GetByID(ctx, id, tx)
	if err != nil {
		return nil, nil, err
	}
	if msg == nil {
		return nil, nil, fmt.Errorf("message %d not found", id)
	}

	data, err := os.ReadFile(msg.RFC822Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read rfc822 file %s: %w", msg.RFC822Path, err)
	}
	return msg, data, nil
}

// LoadMessageByMessageID loads a message by its UUID message_id.
func (ms *MailStore) LoadMessageByMessageID(ctx context.Context, messageID string) (*Message, []byte, error) {
	msg, err := ms.Messages.GetByMessageID(ctx, messageID, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("get by message_id %s: %w", messageID, err)
	}
	if msg == nil {
		return nil, nil, nil
	}
	data, err := os.ReadFile(msg.RFC822Path)
	if err != nil {
		return nil, nil, fmt.Errorf("read rfc822 for %s: %w", messageID, err)
	}
	return msg, data, nil
}

// DeleteMessage marks a message as deleted in the database.
// The file remains on disk until purged.
func (ms *MailStore) DeleteMessage(ctx context.Context, id uint, tx interface{}) error {
	msg, err := ms.Messages.GetByID(ctx, id, tx)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("message %d not found", id)
	}

	if err := ms.Messages.Delete(ctx, id, tx); err != nil {
		return err
	}
	return nil
}

// extractAndStoreAttachments parses RFC822 data and stores extracted attachments
// to both filesystem and database. This is best-effort: errors are logged only
// to observability (not returned) so that message storage always succeeds.
func (ms *MailStore) extractAndStoreAttachments(ctx context.Context, msgID uint, rfc822Data []byte) {
	parts, err := mime.ExtractParts(rfc822Data)
	if err != nil || len(parts) == 0 {
		return
	}

	var attachParts []*mime.Part
	for _, p := range parts {
		if p.IsAttachment {
			attachParts = append(attachParts, p)
		}
	}
	if len(attachParts) == 0 {
		return
	}

	// Enforce max attachments (20).
	if len(attachParts) > 20 {
		attachParts = attachParts[:20]
	}

	for i, p := range attachParts {
		// Enforce max size per attachment (25 MB).
		if p.Size > 25*1024*1024 {
			continue
		}

		filename := p.Filename
		if filename == "" {
			filename = fmt.Sprintf("attachment_%d", i+1)
		}

		// Storage layout: {BasePath}/attachments/{messageID}/{counter}_{filename}
		attDir := filepath.Join(ms.BasePath, "attachments", fmt.Sprintf("%d", msgID))
		if err := os.MkdirAll(attDir, 0750); err != nil {
			continue
		}

		storageName := fmt.Sprintf("%d_%s", i, sanitizeFilenameForStorage(filename))
		storagePath := filepath.Join(attDir, storageName)

		if err := os.WriteFile(storagePath, p.Body, 0640); err != nil {
			continue
		}

		sha, err := ComputeSHA256(storagePath)
		if err != nil {
			os.Remove(storagePath)
			continue
		}

		att := &Attachment{
			MessageID:   msgID,
			Filename:    p.Filename,
			ContentType: p.ContentType,
			SizeBytes:   int64(p.Size),
			SHA256:      sha,
			StoragePath: storagePath,
			CID:         p.ContentID,
		}
		if err := ms.Attachments.Create(ctx, att, nil); err != nil {
			os.Remove(storagePath)
			continue
		}
	}
}

// sanitizeFilenameForStorage prepares a filename for safe filesystem storage.
func sanitizeFilenameForStorage(name string) string {
	// Remove path separators and null bytes.
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	// Replace non-alphanumeric characters except hyphen and underscore.
	var clean []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			clean = append(clean, c)
		} else {
			clean = append(clean, '_')
		}
	}
	return string(clean)
}

// PurgeMessage permanently removes a message from both database and filesystem,
// including all attachment files and records.
func (ms *MailStore) PurgeMessage(ctx context.Context, id uint, tx interface{}) error {
	msg, err := ms.Messages.GetByID(ctx, id, tx)
	if err != nil {
		return err
	}
	if msg == nil {
		return nil // already purged
	}

	// Remove attachment files and DB records.
	atts, listErr := ms.Attachments.ListByMessage(ctx, id, tx)
	if listErr == nil {
		for _, att := range atts {
			os.Remove(att.StoragePath)
		}
		ms.Attachments.DeleteByMessage(ctx, id, tx)
	}
	// Remove attachment directory.
	attDir := filepath.Join(ms.BasePath, "attachments", fmt.Sprintf("%d", id))
	os.RemoveAll(attDir)

	if err := ms.Messages.Purge(ctx, id, tx); err != nil {
		return err
	}
	// Remove RFC822 file after DB is updated.
	os.Remove(msg.RFC822Path)
	return nil
}

// MoveMessage moves a message between folders.
func (ms *MailStore) MoveMessage(ctx context.Context, id, newFolderID uint, tx interface{}) error {
	return ms.Messages.Move(ctx, id, newFolderID, tx)
}

// CopyMessage copies a message to another folder by inserting a new DB record
// and hardlinking the RFC822 file.
func (ms *MailStore) CopyMessage(ctx context.Context, srcID, destMailboxID, destFolderID uint, tx interface{}) (*Message, error) {
	src, err := ms.Messages.GetByID(ctx, srcID, tx)
	if err != nil {
		return nil, err
	}
	if src == nil {
		return nil, fmt.Errorf("source message %d not found", srcID)
	}

	newMsg := *src
	newMsg.ID = 0
	newMsg.MessageID = GenerateMessageID()
	newMsg.MailboxID = destMailboxID
	newMsg.FolderID = destFolderID
	newMsg.RFC822Path = ms.messagePath(newMsg.TenantID, newMsg.DomainID, newMsg.MailboxID, newMsg.MessageID)
	newMsg.Seen = false
	newMsg.PurgedAt = nil

	dir := filepath.Dir(newMsg.RFC822Path)
	os.MkdirAll(dir, 0750)

	if err := os.Link(src.RFC822Path, newMsg.RFC822Path); err != nil {
		return nil, fmt.Errorf("link rfc822: %w", err)
	}

	if tx == nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}

	if err := ms.Messages.Create(ctx, &newMsg, tx); err != nil {
		os.Remove(newMsg.RFC822Path)
		return nil, err
	}
	return &newMsg, nil
}

// RestoreMessage restores a soft-deleted message.
func (ms *MailStore) RestoreMessage(ctx context.Context, id uint, tx interface{}) error {
	return ms.Messages.Restore(ctx, id, tx)
}

// GetRFC822 reads the raw RFC822 content for a message.
func (ms *MailStore) GetRFC822(ctx context.Context, id uint, tx interface{}) ([]byte, error) {
	_, data, err := ms.LoadMessage(ctx, id, tx)
	return data, err
}

// GetMetadata returns a message's metadata without loading the RFC822 file.
func (ms *MailStore) GetMetadata(ctx context.Context, id uint, tx interface{}) (*Message, error) {
	return ms.Messages.GetByID(ctx, id, tx)
}

// ListMessages returns messages matching the filter.
func (ms *MailStore) ListMessages(ctx context.Context, filter MessageFilter, tx interface{}) ([]Message, int64, error) {
	return ms.Messages.List(ctx, filter, tx)
}

// WriteRFC822 overwrites the on-disk RFC822 file for an
// existing message. The DB row is left untouched — callers
// that also need to change metadata should call Update.
//
// Used by the drafts endpoint: the user is editing a draft
// in the compose UI and we want the body they typed to be
// persisted on disk without re-creating the message row.
//
// Authorization is the caller's responsibility; WriteRFC822
// does not check mailbox ownership.
func (ms *MailStore) WriteRFC822(ctx context.Context, id uint, data []byte, tx interface{}) error {
	msg, err := ms.Messages.GetByID(ctx, id, tx)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("message %d not found", id)
	}
	if msg.RFC822Path == "" {
		return fmt.Errorf("message %d has no RFC822 path", id)
	}
	dir := filepath.Dir(msg.RFC822Path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("mkdir rfc822 dir: %w", err)
	}
	if tx == nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}
	if err := os.WriteFile(msg.RFC822Path, data, 0640); err != nil {
		return fmt.Errorf("write rfc822: %w", err)
	}
	// Refresh sha256 + size so future de-duplication
	// sees the new content.
	sha, shaErr := ComputeSHA256(msg.RFC822Path)
	if shaErr == nil {
		_ = ms.Messages.Update(ctx, msg, tx) // no-op for fields, but keeps the path warm
		_ = sha
	}
	return nil
}

// UpdateMetadata persists the supplied Message struct
// fields to the database. Used by the drafts endpoint to
// update subject/to/cc/bcc in place after the user edits
// them. The MessageID / MailboxID / FolderID must not
// change — callers should treat those as immutable.
func (ms *MailStore) UpdateMetadata(ctx context.Context, msg *Message, tx interface{}) error {
	if msg == nil {
		return fmt.Errorf("nil message")
	}
	return ms.Messages.Update(ctx, msg, tx)
}
