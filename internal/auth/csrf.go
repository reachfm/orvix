package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/orvix/orvix/internal/dbdialect"
)

var (
	csrfTokenLength = 32
)

// CredentialSourceLocal is the fiber Locals key under which the auth
// middleware records how the caller authenticated (cookie vs bearer). It is
// informational for handlers and audit; CSRF enforcement deliberately does NOT
// exempt bearer callers — see Middleware.
const CredentialSourceLocal = "auth_credential_source"

// Credential sources recorded by Authenticator.Middleware.
const (
	// CredentialCookie means the request authenticated with a session
	// cookie (access_token or the opaque __Host-orvix_session). Cookies are
	// ambient: a browser attaches them to cross-site requests.
	CredentialCookie = "cookie"
	// CredentialBearer means the request authenticated with an
	// Authorization: Bearer header, which a browser never attaches
	// automatically.
	CredentialBearer = "bearer"
)

// ErrCSRFStoreUnavailable is returned when the backing token store cannot be
// reached. Callers must treat it as fatal for the request (fail closed).
var ErrCSRFStoreUnavailable = errors.New("csrf token store unavailable")

// CSRFManager handles double-submit cookie CSRF protection.
//
// Persistence deliberately uses database/sql + dbdialect rather than GORM.
// The SQLite path installs a custom gorm.Dialector whose Initialize() is a
// no-op, so GORM's default callbacks are never registered: every GORM query
// on SQLite silently executed nothing and returned a nil error. That made the
// previous implementation's Create(&record) a no-op and its validation lookup
// always "succeed" with an empty record — the server-side half of the
// double-submit scheme was inert, and InvalidateUserTokens (logout) never
// deleted anything. The csrf_records table did not even exist. Raw SQL
// through dbdialect is the codebase's canonical portable persistence path and
// makes the control real on both SQLite and PostgreSQL.
type CSRFManager struct {
	db           *gorm.DB
	sqlDB        *sql.DB
	dialect      *dbdialect.Info
	logger       *zap.Logger
	secure       bool
	cookieDomain string
}

// NewCSRFManager creates a new CSRF manager and ensures its table exists.
func NewCSRFManager(db *gorm.DB, logger *zap.Logger, secure bool) *CSRFManager {
	cm := &CSRFManager{
		db:           db,
		logger:       logger,
		secure:       secure,
		cookieDomain: "",
	}
	if db != nil {
		sqlDB, err := db.DB()
		if err == nil && sqlDB != nil {
			cm.sqlDB = sqlDB
			if d, derr := dbdialect.Detect(sqlDB); derr == nil {
				cm.dialect = d
			} else {
				cm.dialect = dbdialect.FromDriver("sqlite")
			}
			// Not fatal at construction: validation fails closed if the
			// table is genuinely unusable, because a token can never be
			// found.
			if serr := cm.EnsureSchema(context.Background()); serr != nil && logger != nil {
				logger.Warn("csrf token store schema init failed", zap.Error(serr))
			}
		} else if logger != nil {
			logger.Warn("csrf token store unavailable: no *sql.DB", zap.Error(err))
		}
	}
	return cm
}

// EnsureSchema creates the csrf_records table if it is absent. Additive and
// idempotent; portable across SQLite and PostgreSQL.
func (cm *CSRFManager) EnsureSchema(ctx context.Context) error {
	if cm.sqlDB == nil {
		return ErrCSRFStoreUnavailable
	}
	d := cm.dialectOrDefault()
	if _, err := cm.sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS csrf_records (
		id `+d.AutoIncrement()+`,
		token_hash TEXT NOT NULL UNIQUE,
		user_id BIGINT NOT NULL DEFAULT 0,
		expires_at `+d.TimestampType()+` NOT NULL,
		created_at `+d.TimestampType()+` NOT NULL
	)`); err != nil {
		return fmt.Errorf("create csrf_records: %w", err)
	}
	// Index supports InvalidateUserTokens; failure is non-fatal.
	_, _ = cm.sqlDB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_csrf_records_user_id ON csrf_records (user_id)`)
	return nil
}

func (cm *CSRFManager) dialectOrDefault() *dbdialect.Info {
	if cm.dialect != nil {
		return cm.dialect
	}
	return dbdialect.FromDriver("sqlite")
}

// SetCookieDomain updates the Domain attribute written on
// the CSRF cookie. Empty means "no Domain attribute" (the
// browser scopes the cookie to the response Host). The
// installer sets this to ".parent_domain" so admin.<p> and
// webmail.<p> share a single CSRF cookie.
func (cm *CSRFManager) SetCookieDomain(domain string) {
	cm.cookieDomain = domain
}

// CSRFRecord stores CSRF token hashes for validation.
type CSRFRecord struct {
	ID        uint      `gorm:"primaryKey"`
	TokenHash string    `gorm:"uniqueIndex;not null"`
	UserID    uint      `gorm:"index;not null"`
	ExpiresAt time.Time `gorm:"not null"`
	CreatedAt time.Time
}

// GenerateToken creates a new CSRF token, stores its hash, and sets the cookie.
func (cm *CSRFManager) GenerateToken(c fiber.Ctx, userID uint) (string, error) {
	if cm.sqlDB == nil {
		return "", ErrCSRFStoreUnavailable
	}

	b := make([]byte, csrfTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate CSRF token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(b)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	d := cm.dialectOrDefault()
	now := time.Now().UTC()
	if _, err := cm.sqlDB.ExecContext(c.Context(),
		`INSERT INTO csrf_records (token_hash, user_id, expires_at, created_at) VALUES (`+d.Placeholders(4)+`)`,
		hash, userID, now.Add(24*time.Hour), now); err != nil {
		return "", fmt.Errorf("failed to store CSRF token: %w", err)
	}

	c.Cookie(&fiber.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Expires:  now.Add(24 * time.Hour),
		HTTPOnly: false,
		Secure:   cm.secure,
		// Lax keeps the cookie on top-level cross-site GET
		// navigations (links, 30x redirects) without sending
		// it on cross-site sub-resource requests. That is the
		// right balance for an admin UI that the user might
		// also reach through webmail.<parent> (subdomain
		// navigation). Strict would silently break the webmail
		// gate's /api/v1/me probe after a redirect.
		SameSite: "Lax",
		Path:     "/",
		Domain:   cm.cookieDomain,
	})

	return token, nil
}

// lookupToken returns the owning user id for a live (unexpired) token hash.
// A missing row yields sql.ErrNoRows so callers fail closed.
func (cm *CSRFManager) lookupToken(ctx context.Context, hash string) (uint, error) {
	if cm.sqlDB == nil {
		return 0, ErrCSRFStoreUnavailable
	}
	d := cm.dialectOrDefault()
	var userID uint
	err := cm.sqlDB.QueryRowContext(ctx,
		`SELECT user_id FROM csrf_records WHERE token_hash = `+d.Placeholder(1)+
			` AND expires_at > `+d.Placeholder(2),
		hash, time.Now().UTC()).Scan(&userID)
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// Middleware validates CSRF tokens on state-changing requests.
func (cm *CSRFManager) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		// API key requests carry no ambient browser credentials (no
		// cookies are automatically attached cross-site to a Bearer
		// token the way they are to a session cookie), so they are
		// not a CSRF target and must not be required to present a
		// CSRF cookie they were never issued. apikeys.Middleware
		// sets this local only on a successfully authenticated API
		// key request.
		if c.Locals("auth_method") == "apikey" {
			return c.Next()
		}

		method := c.Method()

		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			return c.Next()
		}

		// NOTE: there is deliberately NO exemption for Authorization: Bearer
		// JWT callers. A bearer header is not ambient, so in isolation such a
		// request is not forgeable — but this platform's existing contract
		// (asserted by the admin router suite) is that every non-API-key
		// mutation presents a CSRF token, and the caller may still be a
		// browser that holds BOTH a bearer token and the session cookie.
		// Enforcing uniformly is the fail-closed choice and keeps one rule
		// for the whole surface; only true API-key traffic, which is issued
		// no CSRF cookie at all, is exempt.
		cookieToken := c.Cookies("csrf_token")
		if cookieToken == "" {
			cm.warn("CSRF cookie missing")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token missing in cookie",
			})
		}

		headerToken := c.Get("X-CSRF-Token")
		if headerToken == "" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token missing in header",
			})
		}

		// Constant-time comparison: these are equal-length secrets under
		// normal operation, and a timing oracle would leak the issued token.
		if subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
			cm.warn("CSRF token mismatch")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token mismatch",
			})
		}

		// The token must additionally exist server-side and be unexpired.
		// Without this the scheme degrades to "cookie equals header", which
		// any party able to write the victim's cookie jar could satisfy, and
		// revocation on logout would be impossible.
		cookieHash := fmt.Sprintf("%x", sha256.Sum256([]byte(cookieToken)))
		ownerID, err := cm.lookupToken(c.Context(), cookieHash)
		if err != nil {
			cm.warn("CSRF token not found or expired")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token invalid or expired",
			})
		}

		// Bind the stored token to the authenticated identity. Double-submit
		// alone assumes the attacker cannot write the victim's csrf_token
		// cookie, which stops being true if any sibling subdomain is
		// compromised (cookies are writable across a Domain=.parent scope).
		// Requiring the token record to belong to the caller means a token
		// the attacker minted for their own account cannot authorise a
		// mutation on the victim's session.
		//
		// Tokens minted by the public bootstrap endpoint before login carry
		// UserID 0; they stay valid so the login POST itself still works.
		if uid, ok := c.Locals("user_id").(uint); ok && uid != 0 && ownerID != 0 && ownerID != uid {
			cm.warn("CSRF token owner mismatch")
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "CSRF token invalid or expired",
			})
		}

		return c.Next()
	}
}

func (cm *CSRFManager) warn(msg string) {
	if cm.logger != nil {
		cm.logger.Warn(msg)
	}
}

// InvalidateUserTokens removes all CSRF tokens for a user.
func (cm *CSRFManager) InvalidateUserTokens(userID uint) error {
	if cm.sqlDB == nil {
		return ErrCSRFStoreUnavailable
	}
	d := cm.dialectOrDefault()
	_, err := cm.sqlDB.Exec(`DELETE FROM csrf_records WHERE user_id = `+d.Placeholder(1), userID)
	return err
}

// InvalidateToken removes a specific CSRF token.
func (cm *CSRFManager) InvalidateToken(token string) error {
	if cm.sqlDB == nil {
		return ErrCSRFStoreUnavailable
	}
	d := cm.dialectOrDefault()
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	_, err := cm.sqlDB.Exec(`DELETE FROM csrf_records WHERE token_hash = `+d.Placeholder(1), hash)
	return err
}
