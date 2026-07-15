package imap

import (
	"fmt"
	"net"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// SessionState represents the IMAP session state machine.
type SessionState int

const (
	StateNotAuthenticated SessionState = iota
	StateAuthenticated
	StateSelected
	StateLogout
)

func (s SessionState) String() string {
	switch s {
	case StateNotAuthenticated:
		return "NotAuthenticated"
	case StateAuthenticated:
		return "Authenticated"
	case StateSelected:
		return "Selected"
	case StateLogout:
		return "Logout"
	default:
		return "Unknown"
	}
}

// Config holds IMAP server configuration.
type Config struct {
	Hostname          string
	Port              int
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	MaxSessions       int
	MaxBodySize       int64 // max FETCH BODY[] size in bytes (0 = unlimited)
	TLSCertFile       string
	TLSKeyFile        string
	RequireTLSForAuth bool
}

// DefaultConfig returns sensible IMAP defaults.
func DefaultConfig() Config {
	return Config{
		Hostname:     "0.0.0.0",
		Port:         143,
		ReadTimeout:  30 * time.Minute,
		WriteTimeout: 30 * time.Minute,
		MaxSessions:  250,
		MaxBodySize:  50 * 1024 * 1024, // 50 MB default
	}
}

// Session represents a single IMAP connection session.
type Session struct {
	ID              string
	State           SessionState
	Username        string
	MailboxID       uint
	SelectedMailbox *storage.Folder
	MailStore       *storage.MailStore
	Tag             string // current command tag
	RemoteAddr      string
	CreatedAt       time.Time
	TLSActive       bool
	RequireTLS      bool
	conn            net.Conn
}

// NewSession creates a new IMAP session.
func NewSession(remoteAddr string) *Session {
	return &Session{
		ID:         fmt.Sprintf("imap-%d", time.Now().UnixNano()),
		State:      StateNotAuthenticated,
		RemoteAddr: remoteAddr,
		CreatedAt:  time.Now(),
	}
}

// Reset unselects the mailbox but stays authenticated.
func (s *Session) Reset() {
	s.SelectedMailbox = nil
	if s.State == StateSelected {
		s.State = StateAuthenticated
	}
}

// IMAP response constants.
const (
	StatusOK  = "OK"
	StatusNO  = "NO"
	StatusBAD = "BAD"
	StatusBYE = "BYE"
)

// Response builds an IMAP protocol response line.
func Response(tag, status, message string) string {
	if tag == "" {
		return fmt.Sprintf("* %s %s\r\n", status, message)
	}
	return fmt.Sprintf("%s %s %s\r\n", tag, status, message)
}

// Untagged sends an untagged response.
func Untagged(status, message string) string {
	return fmt.Sprintf("* %s %s\r\n", status, message)
}

// OK sends a tagged OK response.
func OK(tag, message string) string {
	return fmt.Sprintf("%s OK %s\r\n", tag, message)
}

// NO sends a tagged NO response.
func NO(tag, message string) string {
	return fmt.Sprintf("%s NO %s\r\n", tag, message)
}

// BAD sends a tagged BAD response.
func BAD(tag, message string) string {
	return fmt.Sprintf("%s BAD %s\r\n", tag, message)
}

// BYE sends an untagged BYE and tagged OK LOGOUT.
func BYE(tag, message string) string {
	return fmt.Sprintf("* BYE %s\r\n%s OK LOGOUT completed\r\n", message, tag)
}

// AuthBackend defines the authentication interface for IMAP.
type AuthBackend interface {
	Authenticate(username, password string) (mailboxID uint, ok bool)
}
