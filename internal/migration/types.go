package migration

import "time"

type ImportSourceType string

const (
	ImportMailcow  ImportSourceType = "mailcow"
	ImportStalwart ImportSourceType = "stalwart"
	ImportModoboa  ImportSourceType = "modoboa"
	ImportExchange ImportSourceType = "exchange"
	ImportIMAP     ImportSourceType = "imap"
)

type ImportJobStatus string

const (
	ImpPending   ImportJobStatus = "pending"
	ImpRunning   ImportJobStatus = "running"
	ImpCompleted ImportJobStatus = "completed"
	ImpFailed    ImportJobStatus = "failed"
	ImpCancelled ImportJobStatus = "cancelled"
)

type ImportJob struct {
	ID                uint            `json:"id"`
	SourceType        ImportSourceType `json:"sourceType"`
	SourceHost        string          `json:"sourceHost,omitempty"`
	Status            ImportJobStatus `json:"status"`
	DomainsImported   int             `json:"domainsImported"`
	MailboxesImported int             `json:"mailboxesImported"`
	MessagesImported  int64           `json:"messagesImported"`
	Errors            int             `json:"errors"`
	StartedAt         time.Time       `json:"startedAt"`
	CompletedAt       *time.Time      `json:"completedAt,omitempty"`
}

type DomainImport struct {
	Domain string `json:"domain"`
	Plan   string `json:"plan"`
}

type MailboxImport struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
	QuotaMB  int64  `json:"quotaMB"`
	DomainID uint   `json:"domainId"`
}

type MessageImport struct {
	MailboxID  uint   `json:"mailboxId"`
	RFC822Data string `json:"rfc822Data"`
	Folder     string `json:"folder,omitempty"`
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS coremail_migrations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		source_type TEXT NOT NULL DEFAULT '',
		source_host TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		domains_imported INTEGER NOT NULL DEFAULT 0,
		mailboxes_imported INTEGER NOT NULL DEFAULT 0,
		messages_imported INTEGER NOT NULL DEFAULT 0,
		errors INTEGER NOT NULL DEFAULT 0,
		started_at DATETIME NOT NULL,
		completed_at DATETIME
	)`,
}
