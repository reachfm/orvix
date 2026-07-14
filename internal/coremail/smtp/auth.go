package smtp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

// AuthIdentity represents an authenticated mailbox identity.
type AuthIdentity struct {
	Username  string // Full email address
	LocalPart string
	Domain    string
	TenantID  uint
	MailboxID uint
	DomainID  uint
	IsAdmin   bool
}

// AuthBackend provides authentication against the CoreMail directory.
type AuthBackend interface {
	// Authenticate verifies credentials and returns the identity if valid.
	Authenticate(ctx context.Context, username, password string) (*AuthIdentity, error)
	// IsLocalDomain checks if a domain is hosted locally.
	IsLocalDomain(ctx context.Context, domain string) (bool, error)
	// ResolveSender checks if an identity is allowed to send as the given from address.
	ResolveSender(ctx context.Context, identity *AuthIdentity, fromAddress string) (bool, error)
}

// Authenticator handles SMTP AUTH commands.
type Authenticator struct {
	backend AuthBackend
}

// NewAuthenticator creates an SMTP authenticator using an AuthBackend.
func NewAuthenticator(backend AuthBackend) *Authenticator {
	return &Authenticator{backend: backend}
}

// AuthResult holds the result of an authentication attempt.
type AuthResult struct {
	Username string
	Identity *AuthIdentity
	Success  bool
}

// HandleAuthPlain processes AUTH PLAIN credentials.
func (a *Authenticator) HandleAuthPlain(ctx context.Context, encoded string) AuthResult {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return AuthResult{Success: false}
	}

	parts := strings.SplitN(string(decoded), "\x00", 3)
	var username, password string
	switch len(parts) {
	case 3:
		username = parts[1]
		password = parts[2]
	case 2:
		username = parts[0]
		password = parts[1]
	default:
		return AuthResult{Success: false}
	}

	if username == "" || password == "" {
		return AuthResult{Success: false}
	}

	if a.backend == nil {
		// No backend configured — fallback to verify function for backward compatibility.
		return AuthResult{Success: false}
	}

	identity, err := a.backend.Authenticate(ctx, username, password)
	if err != nil || identity == nil {
		return AuthResult{Success: false}
	}

	return AuthResult{Username: identity.Username, Identity: identity, Success: true}
}

// HandleAuthLogin processes AUTH LOGIN.
func (a *Authenticator) HandleAuthLogin(ctx context.Context, step int, data string) (authResult AuthResult, challenge string, more bool) {
	switch step {
	case 0:
		return AuthResult{}, base64.StdEncoding.EncodeToString([]byte("Username:")), true
	case 1:
		_, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return AuthResult{Success: false}, "", false
		}
		return AuthResult{}, base64.StdEncoding.EncodeToString([]byte("Password:")), true
	case 2:
		passwordBytes, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return AuthResult{Success: false}, "", false
		}
		_ = string(passwordBytes)
		return AuthResult{Success: false}, "", false
	default:
		return AuthResult{Success: false}, "", false
	}
}

// FuncAuthBackend adapts a verify function to the AuthBackend interface.
// This provides backward compatibility for code that uses the old-style verify function.
type FuncAuthBackend struct {
	verifyFn func(ctx context.Context, username, password string) (string, bool)
}

// NewFuncAuthBackend creates an AuthBackend from a verify function.
func NewFuncAuthBackend(fn func(ctx context.Context, username, password string) (string, bool)) *FuncAuthBackend {
	return &FuncAuthBackend{verifyFn: fn}
}

func (f *FuncAuthBackend) Authenticate(ctx context.Context, username, password string) (*AuthIdentity, error) {
	user, ok := f.verifyFn(ctx, username, password)
	if !ok || user == "" {
		return nil, nil
	}
	localPart, domain := splitEmail(user)
	return &AuthIdentity{
		Username:  user,
		LocalPart: localPart,
		Domain:    domain,
	}, nil
}

func (f *FuncAuthBackend) IsLocalDomain(ctx context.Context, domain string) (bool, error) {
	return false, nil
}

func (f *FuncAuthBackend) ResolveSender(ctx context.Context, identity *AuthIdentity, fromAddress string) (bool, error) {
	// Default: user can only send as their own email.
	return identity != nil && identity.Username == fromAddress, nil
}

func splitEmail(email string) (string, string) {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[:i], email[i+1:]
		}
	}
	return email, ""
}

// CreateAuthPlainResponse creates the base64 response for AUTH PLAIN.
func CreateAuthPlainResponse(username, password string) string {
	raw := fmt.Sprintf("\x00%s\x00%s", username, password)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}
