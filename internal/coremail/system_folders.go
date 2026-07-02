package coremail

// Standalone helper to provision the canonical system
// folders (INBOX, Sent, Drafts, Trash, Junk, Archive) for
// a mailbox. The function takes a *sql.DB and a mailbox
// id and writes the folder rows directly — it does NOT
// depend on the storage.MailStore, so the installer
// bootstrap path and the admin CreateMailbox handler can
// both call it without booting the coremail runtime.
//
// The function is idempotent. Re-running it on a mailbox
// that already has its system folders is a no-op.
//
// The coremail/storage package has an equivalent
// (*FolderSQLRepo).EnsureSystemFolders; we duplicate the
// implementation here so callers that have not wired a
// MailStore can still provision folders. The two
// implementations MUST stay in lock-step — see the
// storage_test.go for the table of canonical folders.

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// folderExec is the minimal interface satisfied by both *sql.DB
// and *sql.Tx. EnsureMailboxSystemFolders / EnsureMailboxSystemFoldersTx
// share the implementation by accepting this interface, so the
// DB-bound and tx-bound versions stay in lock-step.
type folderExec interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// System folder path constants. These match the paths
// the storage layer writes when DefaultSystemFolders
// builds the seed list. Any change here MUST be
// reflected in storage/schema.go's DefaultSystemFolders
// and vice versa.
const (
	SystemFolderInbox   = "INBOX"
	SystemFolderSent    = "Sent"
	SystemFolderDrafts  = "Drafts"
	SystemFolderTrash   = "Trash"
	SystemFolderJunk    = "Junk"
	SystemFolderArchive = "Archive"
)

// EnsureMailboxSystemFolders provisions the six canonical
// system folders (Inbox, Sent, Drafts, Trash, Junk,
// Archive) for the mailbox identified by mailboxID, on
// the supplied *sql.DB. The function is idempotent:
// re-running it for an already-provisioned mailbox is a
// no-op.
//
// Returns an error only on hard database failures
// (constraint violations, missing tables, etc.). Missing
// rows in coremail_mailboxes are reported as
// "mailbox not found".
func EnsureMailboxSystemFolders(ctx context.Context, db *sql.DB, mailboxID uint) error {
	if db == nil {
		return fmt.Errorf("ensure system folders: nil database handle")
	}
	if mailboxID == 0 {
		return fmt.Errorf("ensure system folders: invalid mailbox id")
	}
	return ensureMailboxSystemFolders(ctx, db, mailboxID)
}

// EnsureMailboxSystemFoldersTx is the transaction-bound
// counterpart of EnsureMailboxSystemFolders. It is used by
// bulk-import / admin flows that create a mailbox and
// provision its folders in the SAME transaction, so a
// folder-provisioning failure rolls back the mailbox insert
// (and vice-versa). Single-row CreateMailbox and the
// webmail-login re-provision path can keep using the
// *sql.DB variant; both versions share the same internal
// helper so the canonical folder list stays in lock-step.
func EnsureMailboxSystemFoldersTx(ctx context.Context, tx *sql.Tx, mailboxID uint) error {
	if tx == nil {
		return fmt.Errorf("ensure system folders: nil transaction handle")
	}
	if mailboxID == 0 {
		return fmt.Errorf("ensure system folders: invalid mailbox id")
	}
	return ensureMailboxSystemFolders(ctx, tx, mailboxID)
}

// ensureMailboxSystemFolders is the shared implementation
// for EnsureMailboxSystemFolders and EnsureMailboxSystemFoldersTx.
// Both accept the folderExec interface so a single code
// path drives the DB-bound and tx-bound callers. Keep
// behaviour IDENTICAL across the two callers: any change
// here MUST be exercised by both the *sql.DB tests in
// system_folders_test.go AND a tx-bound test.
func ensureMailboxSystemFolders(ctx context.Context, ex folderExec, mailboxID uint) error {
	// Confirm the mailbox row exists. The coremail_folders
	// table has a foreign-key relationship to
	// coremail_mailboxes; inserting a folder for a
	// non-existent mailbox would fail the FK with a
	// confusing constraint error. Fail fast with a clear
	// message instead.
	var exists int
	if err := ex.QueryRowContext(ctx,
		"SELECT 1 FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL",
		mailboxID,
	).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("ensure system folders: mailbox %d not found", mailboxID)
		}
		return fmt.Errorf("ensure system folders: lookup mailbox: %w", err)
	}

	now := time.Now().UTC()
	folders := []struct {
		path string
		typ  string
	}{
		{SystemFolderInbox, "inbox"},
		{SystemFolderSent, "sent"},
		{SystemFolderDrafts, "drafts"},
		{SystemFolderTrash, "trash"},
		{SystemFolderJunk, "junk"},
		{SystemFolderArchive, "archive"},
	}

	for _, f := range folders {
		// Skip if a folder with this path already
		// exists for the mailbox. We use a separate
		// SELECT to keep the operation idempotent
		// without the corner case of an INSERT that
		// races with a parallel EnsureMailboxSystemFolders
		// call.
		var existingID uint
		err := ex.QueryRowContext(ctx,
			"SELECT id FROM coremail_folders WHERE mailbox_id = ? AND path = ?",
			mailboxID, f.path,
		).Scan(&existingID)
		switch err {
		case nil:
			continue
		case sql.ErrNoRows:
			// fall through to INSERT
		default:
			return fmt.Errorf("ensure system folders: check %s: %w", f.path, err)
		}

		if _, err := ex.ExecContext(ctx, `
			INSERT INTO coremail_folders
				(mailbox_id, parent_id, name, path, folder_type,
				 message_count, unread_count, total_size,
				 created_at, updated_at)
			VALUES (?, NULL, ?, ?, ?, 0, 0, 0, ?, ?)`,
			mailboxID, f.path, f.path, f.typ, now, now,
		); err != nil {
			return fmt.Errorf("ensure system folders: create %s: %w", f.path, err)
		}
	}
	return nil
}
