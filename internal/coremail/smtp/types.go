package smtp

import (
	"crypto/tls"
	"time"
)

// SessionState represents the SMTP session state machine.
type SessionState int

const (
	StateNew      SessionState = iota // Before any greeting
	StateGreeted                      // After EHLO/HELO
	StateMail                         // After MAIL FROM (awaiting RCPT or DATA)
	StateRcpt                         // After at least one RCPT TO (awaiting more RCPT or DATA)
	StateData                         // Inside DATA transfer
	StateAuthenticated                // After successful AUTH
	StateClosed                       // After QUIT or fatal error
)

func (s SessionState) String() string {
	switch s {
	case StateNew:
		return "NEW"
	case StateGreeted:
		return "GREETED"
	case StateMail:
		return "MAIL"
	case StateRcpt:
		return "RCPT"
	case StateData:
		return "DATA"
	case StateAuthenticated:
		return "AUTHENTICATED"
	case StateClosed:
		return "CLOSED"
	default:
		return "UNKNOWN"
	}
}

// SpamEnforcementMode controls how anti-spam verdicts are applied.
type SpamEnforcementMode int

const (
	SpamModeObservation SpamEnforcementMode = iota // headers only, never reject
	SpamModeEnforcement                            // reject when verdict=reject
	SpamModeSuspicious                             // accept but always add headers
)

// Config holds SMTP server configuration.
type Config struct {
	Hostname                string `json:"hostname"`
	MaxMessageSizeBytes     int64  `json:"max_message_size_bytes"`
	MaxRecipientsPerMessage int    `json:"max_recipients_per_message"`
	MaxLineLength           int    `json:"max_line_length"`
	RequireAuthForSubmission bool  `json:"require_auth_for_submission"`
	AllowPlainAuthWithoutTLS bool  `json:"allow_plain_auth_without_tls"`
	RequireTLSForAuth       bool   `json:"require_tls_for_auth"`
	RequireTLSForSubmission bool   `json:"require_tls_for_submission"`
	MaxConcurrentSessions   int    `json:"max_concurrent_sessions"`
	ReadTimeout             time.Duration `json:"read_timeout"`
	WriteTimeout            time.Duration `json:"write_timeout"`
	DataTimeout             time.Duration `json:"data_timeout"`
	TLSCertFile             string `json:"tls_cert_file"`
	TLSKeyFile              string `json:"tls_key_file"`
	SpamMode                SpamEnforcementMode `json:"spam_mode"`
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		Hostname:                  "mail.orvix.local",
		MaxMessageSizeBytes:       25 * 1024 * 1024,
		MaxRecipientsPerMessage:   100,
		MaxLineLength:             1000,
		RequireAuthForSubmission:  true,
		AllowPlainAuthWithoutTLS:  false,
		RequireTLSForAuth:         false,
		RequireTLSForSubmission:   false,
		MaxConcurrentSessions:     250,
		ReadTimeout:               5 * time.Minute,
		WriteTimeout:              5 * time.Minute,
		DataTimeout:               10 * time.Minute,
	}
}

// Session holds the state for a single SMTP connection.
type Session struct {
	ID           string
	State        SessionState
	MailFrom     string
	Recipients   []string
	HeloDomain   string
	AuthUser     string
	AuthIdentity *AuthIdentity
	Authenticated bool
	TLSActive    bool
	TLSConfig    *tls.Config
	DataBuffer   []byte
	RemoteAddr   string
	StartTime    time.Time
	MessageSize  int64
	Extensions   []string
}

// NewSession creates a new SMTP session.
func NewSession(remoteAddr string, tlsCfg *tls.Config, cfg Config) *Session {
	extensions := []string{
		"PIPELINING",
		"8BITMIME",
		"SMTPUTF8",
	}
	if cfg.MaxMessageSizeBytes > 0 {
		extensions = append(extensions, "SIZE "+formatInt64(cfg.MaxMessageSizeBytes))
	}
	// Advertise STARTTLS only when TLS config is available.
	if tlsCfg != nil {
		extensions = append(extensions, "STARTTLS")
	}
	extensions = append(extensions, "AUTH PLAIN LOGIN")

	return &Session{
		ID:         generateSessionID(),
		State:      StateNew,
		TLSConfig:  tlsCfg,
		Extensions: extensions,
		RemoteAddr: remoteAddr,
		StartTime:  time.Now(),
	}
}

func formatInt64(n int64) string {
	if n == 0 {
		return "0"
	}
	digits := make([]byte, 0, 20)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		digits = append(digits, byte('0'+n%10))
		n /= 10
	}
	if neg {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

func generateSessionID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	n := time.Now().UnixNano()
	for i := range b {
		idx := (n + int64(i)*7919) % 36
		if idx < 0 {
			idx = -idx
		}
		b[i] = chars[idx%36]
	}
	return string(b)
}

// ResetTransaction resets the MAIL FROM, RCPT TO, and DATA state (RSET semantics).
func (s *Session) ResetTransaction() {
	s.MailFrom = ""
	s.Recipients = nil
	s.DataBuffer = nil
	s.MessageSize = 0
	if s.State == StateMail || s.State == StateRcpt || s.State == StateData {
		s.State = StateGreeted
	}
}
