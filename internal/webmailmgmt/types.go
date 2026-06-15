package webmailmgmt

import (
	"context"
	"time"
)

type WebmailSession struct {
	ID         uint       `json:"id"`
	MailboxID  uint       `json:"mailbox_id"`
	Email      string     `json:"email"`
	IP         string     `json:"ip"`
	UserAgent  string     `json:"user_agent"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type LoginActivity struct {
	SuccessfulLogins  int        `json:"successful_logins"`
	FailedLogins      int        `json:"failed_logins"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
	LastFailedLoginAt *time.Time `json:"last_failed_login_at,omitempty"`
}

type StorageMetrics struct {
	MessageCount  int   `json:"message_count"`
	MailboxSize   int64 `json:"mailbox_size"`
	SentCount     int   `json:"sent_count"`
	ReceivedCount int   `json:"received_count"`
}

type WebmailAccount struct {
	MailboxID   uint       `json:"mailbox_id"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	Domain      string     `json:"domain"`
	IsAdmin     bool       `json:"is_admin"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type SessionRecorder interface {
	RecordSession(ctx context.Context, mailboxID uint, ip, userAgent string) error
}
