package pop3

import (
	"fmt"
	"net"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

// SessionState represents the POP3 state machine.
type SessionState int

const (
	StateAuthorization SessionState = iota
	StateTransaction
	StateUpdate
)

func (s SessionState) String() string {
	switch s {
	case StateAuthorization:
		return "AUTHORIZATION"
	case StateTransaction:
		return "TRANSACTION"
	case StateUpdate:
		return "UPDATE"
	default:
		return "UNKNOWN"
	}
}

// Config holds POP3 server configuration.
type Config struct {
	Hostname     string
	Port         int
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
	MaxSessions   int
	MessageDomain string
	TLSCertFile   string
	TLSKeyFile    string
	RequireTLSForAuth bool
}

func DefaultConfig() Config {
	return Config{
		Hostname:     "0.0.0.0",
		Port:         110,
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		MaxSessions:  250,
	}
}

// Session represents a single POP3 connection.
type Session struct {
	ID             string
	State          SessionState
	Username       string
	MailboxID      uint
	MailStore      *storage.MailStore
	Messages       []storage.Message
	Deleted        map[uint]bool
	RemoteAddr     string
	CreatedAt      time.Time
	TLSActive      bool
	RequireTLS     bool
	conn           net.Conn
}

func NewSession(remoteAddr string) *Session {
	return &Session{
		ID:         fmt.Sprintf("pop3-%d", time.Now().UnixNano()),
		State:      StateAuthorization,
		RemoteAddr: remoteAddr,
		CreatedAt:  time.Now(),
		Deleted:    make(map[uint]bool),
	}
}

// Response formats a POP3 response line.
func Response(status, message string) string {
	if status == "+OK" {
		return fmt.Sprintf("+OK %s\r\n", message)
	}
	return fmt.Sprintf("-ERR %s\r\n", message)
}

// AuthBackend defines POP3 authentication.
type AuthBackend interface {
	Authenticate(username, password string) (mailboxID uint, ok bool)
}
