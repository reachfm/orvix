package migration

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DomainCreator creates domains for import.
type DomainCreator interface {
	CreateDomain(ctx context.Context, domain, plan string) (interface{}, error)
	DomainExists(ctx context.Context, domain string) (bool, error)
}

// MailboxCreator creates mailboxes for import.
type MailboxCreator interface {
	CreateMailbox(ctx context.Context, email, name, password string, domainID uint, quotaMB int64) (interface{}, error)
}

// MessageStorer stores imported messages.
type MessageStorer interface {
	StoreMessage(ctx context.Context, mailboxID uint, rfc822Data []byte) (interface{}, error)
}

// Service provides migration import operations.
type Service struct {
	db             *sql.DB
	domainCreator  DomainCreator
	mailboxCreator MailboxCreator
	messageStorer  MessageStorer
}

// NewService creates a migration service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// SetDomainCreator attaches a domain registry.
func (s *Service) SetDomainCreator(d DomainCreator) { s.domainCreator = d }

// SetMailboxCreator attaches a mailbox management service.
func (s *Service) SetMailboxCreator(m MailboxCreator) { s.mailboxCreator = m }

// SetMessageStorer attaches a mail store.
func (s *Service) SetMessageStorer(m MessageStorer) { s.messageStorer = m }

// EnsureSchema creates required tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── Job Management ──────────────────────────────────────

func (s *Service) CreateJob(ctx context.Context, sourceType ImportSourceType, sourceHost string) (*ImportJob, error) {
	j := &ImportJob{
		SourceType: sourceType, SourceHost: sourceHost,
		Status: ImpPending, StartedAt: time.Now().UTC(),
	}
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO coremail_migrations (source_type, source_host, status, started_at) VALUES (?, ?, ?, ?)",
		string(j.SourceType), j.SourceHost, string(j.Status), j.StartedAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	j.ID = uint(id)
	return j, nil
}

func (s *Service) ListJobs(ctx context.Context) ([]ImportJob, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, source_type, source_host, status, domains_imported, mailboxes_imported, messages_imported, errors, started_at, completed_at FROM coremail_migrations ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []ImportJob
	for rows.Next() {
		var j ImportJob
		var completedAt sql.NullTime
		if err := rows.Scan(&j.ID, &j.SourceType, &j.SourceHost, &j.Status, &j.DomainsImported, &j.MailboxesImported, &j.MessagesImported, &j.Errors, &j.StartedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			j.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Service) GetJob(ctx context.Context, id uint) (*ImportJob, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, source_type, source_host, status, domains_imported, mailboxes_imported, messages_imported, errors, started_at, completed_at FROM coremail_migrations WHERE id=?", id)
	var j ImportJob
	var completedAt sql.NullTime
	err := row.Scan(&j.ID, &j.SourceType, &j.SourceHost, &j.Status, &j.DomainsImported, &j.MailboxesImported, &j.MessagesImported, &j.Errors, &j.StartedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		j.CompletedAt = &completedAt.Time
	}
	return &j, nil
}

func (s *Service) CancelJob(ctx context.Context, id uint) error {
	return s.updateStatus(ctx, id, ImpCancelled)
}

func (s *Service) updateStatus(ctx context.Context, id uint, status ImportJobStatus) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE coremail_migrations SET status=?, completed_at=? WHERE id=?", string(status), now, id)
	return err
}

// ── Domain Import ───────────────────────────────────────

func (s *Service) ImportDomain(ctx context.Context, jobID uint, domain DomainImport) error {
	if s.domainCreator == nil {
		return fmt.Errorf("domain creator not configured")
	}

	exists, err := s.domainCreator.DomainExists(ctx, domain.Domain)
	if err != nil {
		return fmt.Errorf("check domain: %w", err)
	}
	if exists {
		s.incrementErrors(ctx, jobID)
		return fmt.Errorf("domain already exists: %s", domain.Domain)
	}

	_, err = s.domainCreator.CreateDomain(ctx, domain.Domain, domain.Plan)
	if err != nil {
		s.incrementErrors(ctx, jobID)
		return fmt.Errorf("create domain: %w", err)
	}

	s.incrementDomains(ctx, jobID)
	return nil
}

// ── Mailbox Import ──────────────────────────────────────

func (s *Service) ImportMailbox(ctx context.Context, jobID uint, mb MailboxImport) error {
	if s.mailboxCreator == nil {
		return fmt.Errorf("mailbox creator not configured")
	}

	_, err := s.mailboxCreator.CreateMailbox(ctx, mb.Email, mb.Name, mb.Password, mb.DomainID, mb.QuotaMB)
	if err != nil {
		s.incrementErrors(ctx, jobID)
		return fmt.Errorf("create mailbox: %w", err)
	}

	s.incrementMailboxes(ctx, jobID)
	return nil
}

// ── Message Import ──────────────────────────────────────

func (s *Service) ImportMessage(ctx context.Context, jobID uint, msg MessageImport) error {
	if s.messageStorer == nil {
		return fmt.Errorf("message storer not configured")
	}

	_, err := s.messageStorer.StoreMessage(ctx, msg.MailboxID, []byte(msg.RFC822Data))
	if err != nil {
		s.incrementErrors(ctx, jobID)
		return fmt.Errorf("store message: %w", err)
	}

	s.incrementMessages(ctx, jobID)
	return nil
}

// ── Internal ────────────────────────────────────────────

func (s *Service) incrementDomains(ctx context.Context, jobID uint) {
	s.db.ExecContext(ctx, "UPDATE coremail_migrations SET domains_imported = domains_imported + 1 WHERE id=?", jobID)
}

func (s *Service) incrementMailboxes(ctx context.Context, jobID uint) {
	s.db.ExecContext(ctx, "UPDATE coremail_migrations SET mailboxes_imported = mailboxes_imported + 1 WHERE id=?", jobID)
}

func (s *Service) incrementMessages(ctx context.Context, jobID uint) {
	s.db.ExecContext(ctx, "UPDATE coremail_migrations SET messages_imported = messages_imported + 1 WHERE id=?", jobID)
}

func (s *Service) incrementErrors(ctx context.Context, jobID uint) {
	s.db.ExecContext(ctx, "UPDATE coremail_migrations SET errors = errors + 1 WHERE id=?", jobID)
}
