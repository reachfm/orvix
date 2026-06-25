package storage

import "fmt"

// Tables returns all DDL statements required by the MailStore.
func Tables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS coremail_folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			parent_id INTEGER,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			folder_type TEXT NOT NULL DEFAULT 'custom',
			message_count INTEGER NOT NULL DEFAULT 0,
			unread_count INTEGER NOT NULL DEFAULT 0,
			total_size INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (parent_id) REFERENCES coremail_folders(id)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT UNIQUE NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			domain_id INTEGER NOT NULL DEFAULT 0,
			mailbox_id INTEGER NOT NULL,
			folder_id INTEGER NOT NULL,
			thread_id TEXT,
			internet_message_id TEXT,
			subject TEXT NOT NULL DEFAULT '',
			from_address TEXT NOT NULL DEFAULT '',
			to_addresses TEXT NOT NULL DEFAULT '',
			cc_addresses TEXT NOT NULL DEFAULT '',
			bcc_addresses TEXT NOT NULL DEFAULT '',
			reply_to TEXT NOT NULL DEFAULT '',
			message_date DATETIME,
			received_date DATETIME NOT NULL,
			size_bytes INTEGER NOT NULL DEFAULT 0,
			rfc822_path TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			seen INTEGER NOT NULL DEFAULT 0,
			answered INTEGER NOT NULL DEFAULT 0,
			flagged INTEGER NOT NULL DEFAULT 0,
			draft INTEGER NOT NULL DEFAULT 0,
			deleted INTEGER NOT NULL DEFAULT 0,
			junk INTEGER NOT NULL DEFAULT 0,
			importance INTEGER NOT NULL DEFAULT 0,
			retention_policy_id INTEGER,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			purged_at DATETIME,
			FOREIGN KEY (folder_id) REFERENCES coremail_folders(id),
			FOREIGN KEY (retention_policy_id) REFERENCES coremail_retention_policies(id)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL,
			filename TEXT NOT NULL DEFAULT '',
			content_type TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			sha256 TEXT NOT NULL DEFAULT '',
			storage_path TEXT NOT NULL DEFAULT '',
			cid TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			FOREIGN KEY (message_id) REFERENCES coremail_messages(id)
		)`,
		`CREATE TABLE IF NOT EXISTS coremail_retention_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			retention_type TEXT NOT NULL DEFAULT 'age',
			retention_days INTEGER NOT NULL DEFAULT 365,
			max_messages INTEGER NOT NULL DEFAULT 0,
			max_size_bytes INTEGER NOT NULL DEFAULT 0,
			delete_after_expiry INTEGER NOT NULL DEFAULT 1,
			hold INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS push_subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			endpoint TEXT NOT NULL,
			p256dh_key TEXT NOT NULL,
			auth_key TEXT NOT NULL,
			user_agent TEXT NOT NULL DEFAULT '',
			disabled_at DATETIME,
			last_seen_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY (mailbox_id) REFERENCES coremail_mailboxes(id)
		)`,
	}
}

// Indexes returns all index DDL statements.
func Indexes() []string {
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_mailbox ON coremail_messages(mailbox_id, folder_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_message_id ON coremail_messages(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_internet_id ON coremail_messages(internet_message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_subject ON coremail_messages(subject)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_from ON coremail_messages(from_address)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_date ON coremail_messages(received_date)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_flags ON coremail_messages(mailbox_id, folder_id, seen, deleted, junk)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_messages_purged ON coremail_messages(purged_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_coremail_folders_mailbox_path ON coremail_folders(mailbox_id, path)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_folders_parent ON coremail_folders(parent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_attachments_message ON coremail_attachments(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_coremail_attachments_sha256 ON coremail_attachments(sha256)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_push_subscriptions_endpoint ON push_subscriptions(endpoint)`,
		`CREATE INDEX IF NOT EXISTS idx_push_subscriptions_mailbox ON push_subscriptions(mailbox_id)`,
	}
}

// DefaultSystemFolders returns the folder entries to create for a new mailbox.
func DefaultSystemFolders(mailboxID uint) []Folder {
	now := nowFn()
	system := []struct {
		name string
		ft   FolderType
	}{
		{"INBOX", FolderInbox},
		{"Sent", FolderSent},
		{"Drafts", FolderDrafts},
		{"Trash", FolderTrash},
		{"Junk", FolderJunk},
		{"Archive", FolderArchive},
	}
	folders := make([]Folder, len(system))
	for i, s := range system {
		folders[i] = Folder{
			MailboxID:  mailboxID,
			Name:       s.name,
			Path:       s.name,
			FolderType: s.ft,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
	}
	return folders
}

func init() {
	// Ensure fmt import is used if this file needs it for error formatting elsewhere.
	_ = fmt.Sprintf
}
