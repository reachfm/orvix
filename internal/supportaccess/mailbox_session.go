package supportaccess

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// MailboxViewSession is a narrow, audited, read-only grant that lets a
// single platform operator inspect a single customer mailbox for a
// bounded window. It is deliberately NOT the tenant-wide AccessGrant
// above: a mailbox support session is bound to one operator, one
// tenant, and one mailbox — never reusable across any of those three,
// never a bearer token, and never convertible into a normal customer
// session. The mailbox's password/hash is never read or touched by
// this type or anything that constructs one.
type MailboxViewSession struct {
	ID              string
	OperatorID      uint
	TargetTenantID  uint
	TargetMailboxID uint
	Scope           string // always "mailbox_view"
	TicketRef       string
	Reason          string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	EndedAt         *time.Time
	RevokedAt       *time.Time

	version int
}

const (
	MailboxViewScope = "mailbox_view"

	MailboxViewSessionDefaultDuration = 30 * time.Minute
	MailboxViewSessionMaxDuration     = 60 * time.Minute
)

var (
	ErrMailboxSessionNotFound = &saError{"mailbox support-view session not found"}
	ErrMailboxSessionExpired  = &saError{"mailbox support-view session has expired"}
	ErrMailboxSessionEnded    = &saError{"mailbox support-view session has ended"}
	ErrMailboxSessionRevoked  = &saError{"mailbox support-view session has been revoked"}
	ErrMailboxSessionMismatch = &saError{"mailbox support-view session does not match the requested operator, tenant, or mailbox"}
)

// Active reports whether the session may still be used to read the
// bound mailbox: not ended, not revoked, and not past ExpiresAt.
func (s *MailboxViewSession) Active(now time.Time) error {
	if s.RevokedAt != nil {
		return ErrMailboxSessionRevoked
	}
	if s.EndedAt != nil {
		return ErrMailboxSessionEnded
	}
	if !now.Before(s.ExpiresAt) {
		return ErrMailboxSessionExpired
	}
	return nil
}

type MailboxSessionRepository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewMailboxSessionRepository(db *sql.DB) *MailboxSessionRepository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &MailboxSessionRepository{db: db, dialect: d}
}

func (r *MailboxSessionRepository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_mailbox_view_sessions (
		id TEXT PRIMARY KEY,
		operator_id INTEGER NOT NULL,
		target_tenant_id INTEGER NOT NULL,
		target_mailbox_id INTEGER NOT NULL,
		scope TEXT NOT NULL DEFAULT 'mailbox_view',
		ticket_ref TEXT NOT NULL,
		reason TEXT NOT NULL,
		expires_at `+ts+` NOT NULL,
		ended_at `+ts+`,
		revoked_at `+ts+`,
		version INTEGER NOT NULL DEFAULT 1,
		created_at `+ts+` NOT NULL
	)`)
	return err
}

func newMailboxSessionID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (r *MailboxSessionRepository) Insert(ctx context.Context, s *MailboxViewSession) error {
	id, err := newMailboxSessionID()
	if err != nil {
		return err
	}
	s.ID = id
	s.CreatedAt = time.Now().UTC()
	s.version = 1
	_, err = r.db.ExecContext(ctx, r.dialect.Rewrite(
		`INSERT INTO platform_mailbox_view_sessions (id, operator_id, target_tenant_id, target_mailbox_id, scope, ticket_ref, reason, expires_at, version, created_at)
		VALUES (?,?,?,?,?,?,?,?,1,?)`),
		s.ID, s.OperatorID, s.TargetTenantID, s.TargetMailboxID, s.Scope, s.TicketRef, s.Reason, s.ExpiresAt, s.CreatedAt)
	return err
}

func (r *MailboxSessionRepository) Get(ctx context.Context, id string) (*MailboxViewSession, error) {
	var s MailboxViewSession
	var endedAt, revokedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, r.dialect.Rewrite(
		`SELECT id, operator_id, target_tenant_id, target_mailbox_id, scope, ticket_ref, reason, expires_at, ended_at, revoked_at, version, created_at
		FROM platform_mailbox_view_sessions WHERE id=?`), id).
		Scan(&s.ID, &s.OperatorID, &s.TargetTenantID, &s.TargetMailboxID, &s.Scope, &s.TicketRef, &s.Reason, &s.ExpiresAt, &endedAt, &revokedAt, &s.version, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrMailboxSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if endedAt.Valid {
		s.EndedAt = &endedAt.Time
	}
	if revokedAt.Valid {
		s.RevokedAt = &revokedAt.Time
	}
	return &s, nil
}

// End marks the session ended (operator-initiated) via optimistic
// concurrency; a concurrent End/Revoke is a no-op error, never a panic
// or a silent double-decrement.
func (r *MailboxSessionRepository) End(ctx context.Context, s *MailboxViewSession) error {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, r.dialect.Rewrite(
		`UPDATE platform_mailbox_view_sessions SET ended_at=?, version=version+1 WHERE id=? AND version=? AND ended_at IS NULL AND revoked_at IS NULL`,
	), now, s.ID, s.version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &saError{"mailbox support-view session already ended, revoked, or concurrently modified"}
	}
	s.EndedAt = &now
	s.version++
	return nil
}
