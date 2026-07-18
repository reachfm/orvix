package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrTokenExpired       = errors.New("token expired")
	ErrTokenInvalid       = errors.New("invalid token")
	ErrSessionExpired     = errors.New("session expired")
)

// Role represents a user role for RBAC.
type Role string

const (
	// ── Canonical roles (v2+) ─────────────────────────────────
	// Platform-scoped: no tenant ownership.
	// Platform Super Admin: full platform access, no customer tenant.
	RolePlatformSuperAdmin Role = "platform_super_admin"

	// Tenant-scoped: belong to exactly one tenant.
	RoleTenantAdmin    Role = "tenant_admin"
	RoleTenantOperator Role = "tenant_operator"
	RoleTenantSupport  Role = "tenant_support"
	RoleTenantReadOnly Role = "tenant_readonly"

	// ── Legacy role aliases (v1 compatibility) ─────────────────
	// These are accepted as input during migration but are not the
	// canonical persisted form for new accounts.

	// RoleSuperAdmin has every permission, including license
	// management and system-wide destructive operations.
	// Deprecated: use RolePlatformSuperAdmin.
	RoleSuperAdmin Role = "superadmin"

	// RoleAdmin has every permission EXCEPT license.write. A
	// license change is reserved for super_admin because it can
	// affect feature flags and tier enforcement. The
	// permission matrix lives in internal/auth/rbac.
	// Deprecated: ambiguous — use RolePlatformSuperAdmin or
	// RoleTenantAdmin depending on tenant_id presence.
	RoleAdmin Role = "admin"

	// RoleOperator is a helpdesk persona: read everything, act
	// on queue and users, but cannot modify settings, backups,
	// or license.
	// Deprecated: use RoleTenantOperator for tenant-scoped.
	RoleOperator Role = "operator"

	// RoleReadOnly is an auditor / observer. Read everything,
	// write nothing.
	// Deprecated: use RoleTenantReadOnly for tenant-scoped.
	RoleReadOnly Role = "readonly"

	// RoleUser is the per-mailbox end-user role (webmail).
	// It does NOT have admin permissions.
	RoleUser Role = "user"

	// RoleBilling is a tenant billing-only role.
	RoleBilling Role = "billing"
)

// NormalizeRole maps legacy role strings to their canonical counterparts.
// Returns the normalized role and whether the original value was valid.
// Returns (Role(""), false) for unknown/invalid roles.
//
// Migration rules:
//
//	tenant_id IS NOT NULL + "admin" → "tenant_admin"
//	tenant_id IS NULL + "superadmin"/"super_admin"/"super-admin" → "platform_super_admin"
//	tenant_id IS NULL + "admin" → FAIL (ambiguous, requires manual review)
//	tenant_id IS NOT NULL + "operator" → "tenant_operator"
//	tenant_id IS NOT NULL + "readonly"/"read_only" → "tenant_readonly"
//	tenant_id IS NOT NULL + "support" → "tenant_support"
//	"user" → "user" (unchanged)
//	"billing" → "billing" (unchanged)
func NormalizeRole(role Role, tenantID *int64) (Role, bool) {
	switch role {
	case RolePlatformSuperAdmin, RoleTenantAdmin, RoleTenantOperator,
		RoleTenantSupport, RoleTenantReadOnly:
		// Already canonical.
		return role, true
	case "super_admin", "super-admin", RoleSuperAdmin:
		return RolePlatformSuperAdmin, true
	case RoleUser:
		return RoleUser, true
	case RoleBilling:
		return RoleBilling, true
	case RoleAdmin:
		// Ambiguous: needs tenant_id to determine mapping.
		if tenantID != nil && *tenantID > 0 {
			return RoleTenantAdmin, true
		}
		// No tenant or nil tenant → ambiguous. Return as-is, caller
		// must decide (typically fail closed for migration).
		return RoleAdmin, false
	case "support":
		if tenantID != nil && *tenantID > 0 {
			return RoleTenantSupport, true
		}
		return RoleTenantSupport, true // platform support is unsupported; map to tenant
	case "read_only", "readonly":
		if tenantID != nil && *tenantID > 0 {
			return RoleTenantReadOnly, true
		}
		return RoleTenantReadOnly, true
	case "operator":
		if tenantID != nil && *tenantID > 0 {
			return RoleTenantOperator, true
		}
		return RoleTenantOperator, true
	default:
		return "", false
	}
}

// Authenticator handles JWT-based authentication with Argon2id password hashing.
type Authenticator struct {
	privateKey   *rsa.PrivateKey
	publicKey    *rsa.PublicKey
	db           *gorm.DB
	dialect      *dbdialect.Info
	logger       *zap.Logger
	accessTTL    time.Duration
	refreshTTL   time.Duration
	passwordCost config.AuthConfig
}

// NewAuthenticator creates a new authentication system.
// It loads the RSA key pair from disk if it exists, otherwise generates and persists a new one.
func NewAuthenticator(cfg *config.AuthConfig, db *gorm.DB, logger *zap.Logger) (*Authenticator, error) {
	privateKey, err := loadOrGenerateKey(cfg.JWTKeyPath, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RSA key: %w", err)
	}

	// Detect the SQL dialect once so raw revocation-store queries (H-9) use
	// the correct placeholder style. Raw *sql.DB is used there rather than
	// GORM because GORM writes silently no-op under the custom modernc SQLite
	// dialector (same reason C-1's tenant lookup uses raw *sql.DB).
	dialect := dbdialect.FromDriver("sqlite")
	if db != nil {
		if sqlDB, derr := db.DB(); derr == nil {
			if di, derr2 := dbdialect.Detect(sqlDB); derr2 == nil {
				dialect = di
			}
		}
	}

	return &Authenticator{
		privateKey:   privateKey,
		publicKey:    &privateKey.PublicKey,
		db:           db,
		dialect:      dialect,
		logger:       logger,
		accessTTL:    cfg.JWTAccessTTL,
		refreshTTL:   cfg.JWTRefreshTTL,
		passwordCost: *cfg,
	}, nil
}

// loadOrGenerateKey loads an RSA private key from disk or generates and saves a new one.
func loadOrGenerateKey(keyPath string, logger *zap.Logger) (*rsa.PrivateKey, error) {
	if keyPath == "" {
		keyPath = "/var/lib/orvix/jwt_key.pem"
	}

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block != nil && block.Type == "RSA PRIVATE KEY" {
			key, parseErr := x509.ParsePKCS1PrivateKey(block.Bytes)
			if parseErr == nil {
				logger.Info("loaded persisted JWT signing key", zap.String("path", keyPath))
				return key, nil
			}
			logger.Warn("failed to parse persisted JWT key, generating new one", zap.Error(parseErr))
		}
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("failed to create key directory, key will not persist", zap.Error(err))
		return privateKey, nil
	}

	keyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyBytes,
	})

	if err := os.WriteFile(keyPath, pemBlock, 0600); err != nil {
		logger.Warn("failed to persist JWT signing key, key will not survive restart", zap.Error(err))
	} else {
		logger.Info("persisted new JWT signing key", zap.String("path", keyPath))
	}

	return privateKey, nil
}

// GenerateAccessToken creates a short-lived JWT access token (RS256).
//
// Each token carries a unique "jti" (JWT ID) so it can be individually revoked
// on logout — see RevokeAccessToken / ValidateAccessToken (finding H-9). Tokens
// minted before this change simply have no jti and are treated as
// non-revocable; they expire within the short access TTL.
func (a *Authenticator) GenerateAccessToken(userID uint, role Role) (string, error) {
	token, _, _, err := a.GenerateAccessTokenWithJTI(userID, role)
	return token, err
}

// GenerateAccessTokenWithJTI is GenerateAccessToken but also returns the token's
// jti and expiry so the caller can bind them to the session row it creates.
// Persisting the jti is what lets a specific session be revoked immediately
// (its access token is added to the revocation store), and the expiry bounds
// how long the revocation entry must live.
func (a *Authenticator) GenerateAccessTokenWithJTI(userID uint, role Role) (string, string, time.Time, error) {
	now := time.Now()
	exp := now.Add(a.accessTTL)
	jti, err := newJTI()
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to generate token id: %w", err)
	}

	// Fetch current token_version from database to embed in JWT.
	// Gracefully handles pre-migration databases where the column
	// may not exist yet (COALESCE would fail).
	var tokenVersion int64 = 0
	if a.db != nil {
		var currentTV int64
		// Try raw SQL first, fallback to 0 on any error (column missing,
		// table missing, etc.).
		row := a.db.Raw("SELECT COALESCE(token_version, 0) FROM users WHERE id = ?", userID).Row()
		if row != nil {
			if err := row.Scan(&currentTV); err == nil {
				tokenVersion = currentTV
			}
		}
	}

	claims := jwt.MapClaims{
		"sub":           fmt.Sprintf("%d", userID),
		"role":          string(role),
		"iat":           now.Unix(),
		"exp":           exp.Unix(),
		"jti":           jti,
		"token_version": tokenVersion,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("failed to sign access token: %w", err)
	}

	return tokenString, jti, exp, nil
}

// newJTI returns a random 128-bit token identifier as hex.
func newJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateRefreshToken creates a long-lived refresh token stored as HttpOnly cookie.
// GenerateRefreshToken creates a long-lived refresh token stored as an HttpOnly
// cookie. accessJTI is the jti of the access token minted for the same login;
// it is persisted on the session row so RevokeAccountSession can revoke that
// exact access token immediately when the session is revoked. Pass "" for flows
// that do not mint an access token.
func (a *Authenticator) GenerateRefreshToken(userID uint, accessJTI string) (string, time.Time, error) {
	expiresAt := time.Now().Add(a.refreshTTL)

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate refresh token: %w", err)
	}

	token := hex.EncodeToString(b)
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	// Persist via raw, dialect-safe SQL: under the custom modernc SQLite
	// dialector GORM Create on an anonymous struct silently no-ops, which would
	// drop the row and the access-token jti we need for targeted revocation.
	// All NOT NULL columns are supplied explicitly.
	sqlDB, err := a.db.DB()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to store session: %w", err)
	}
	d := a.dbDialect()
	now := time.Now().UTC()
	insert := "INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, jti, expires_at) VALUES (" +
		d.Placeholders(9) + ")"
	if _, err := sqlDB.Exec(insert, now, now, userID, tokenHash, "", "", "", accessJTI, expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("failed to store session: %w", err)
	}

	return token, expiresAt, nil
}

// RevokeJTI records a JWT access-token id as revoked until expiresAt via the
// dialect-safe revocation store, so ValidateAccessToken rejects it immediately.
// Exported so handlers (targeted session revocation) can revoke a specific
// session's access token without reaching into auth internals. A blank jti is a
// no-op (legacy sessions created before jti persistence).
func (a *Authenticator) RevokeJTI(jti string, expiresAt time.Time) error {
	if jti == "" {
		return nil
	}
	return a.revokeToken(jti, expiresAt)
}

// ValidateAccessToken validates a JWT access token and returns user ID and role.
func (a *Authenticator) ValidateAccessToken(tokenString string) (uint, Role, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil {
		return 0, "", ErrTokenInvalid
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return 0, "", ErrTokenInvalid
	}

	exp, ok := claims["exp"].(float64)
	if ok && time.Now().Unix() > int64(exp) {
		return 0, "", ErrTokenExpired
	}

	// H-9: reject tokens explicitly revoked on logout. Tokens minted
	// before H-9 carry no jti and are treated as non-revocable (they
	// expire within the short access TTL). isTokenRevoked fails safe
	// (returns false) if the revocation store is unavailable, so a
	// storage hiccup can never lock every user out.
	if jti, ok := claims["jti"].(string); ok && jti != "" {
		if a.isTokenRevoked(jti) {
			return 0, "", ErrTokenInvalid
		}
	}

	var userID uint
	fmt.Sscanf(claims["sub"].(string), "%d", &userID)
	role, _ := claims["role"].(string)

	// Validate token_version from database. If the user's version was
	// bumped (role change, suspension, password reset), the old token
	// is rejected even if the JWT signature is valid.
	if a.db != nil {
		if tvClaim, hasTV := claims["token_version"].(float64); hasTV {
			var currentTV int64
			row := a.db.Raw("SELECT COALESCE(token_version, 0) FROM users WHERE id = ?", userID).Row()
			if row != nil {
				if err := row.Scan(&currentTV); err == nil {
					if int64(tvClaim) != currentTV {
						return 0, "", ErrTokenInvalid
					}
				}
			}
		}
	}

	return userID, Role(role), nil
}

// dbDialect returns the detected dialect, defaulting to SQLite when unset (as
// in unit tests that construct Authenticator directly).
func (a *Authenticator) dbDialect() *dbdialect.Info {
	if a.dialect != nil {
		return a.dialect
	}
	return dbdialect.FromDriver("sqlite")
}

// RevokeAccessToken parses a signed access token, extracts its jti and exp, and
// records the jti in the revocation store until that expiry so the token is
// rejected by ValidateAccessToken for the remainder of its (short) lifetime.
// Used by logout. A token without a jti (pre-H-9) or already expired is a
// no-op. Errors from the store are returned but must not block logout.
func (a *Authenticator) RevokeAccessToken(tokenString string) error {
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil // unparseable token: nothing to revoke
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil // pre-H-9 token; expires within the access TTL
	}
	exp, _ := claims["exp"].(float64)
	expiresAt := time.Unix(int64(exp), 0)
	if exp == 0 {
		expiresAt = time.Now().Add(a.accessTTL)
	}
	return a.revokeToken(jti, expiresAt)
}

// revokeToken records jti as revoked until expiresAt (Unix seconds) and
// opportunistically prunes already-expired rows so the table stays bounded.
// Uses raw *sql.DB (not GORM) because GORM writes silently no-op under the
// custom modernc SQLite dialector; dbdialect supplies the correct placeholder
// style and a portable upsert for both SQLite and PostgreSQL.
func (a *Authenticator) revokeToken(jti string, expiresAt time.Time) error {
	if a.db == nil {
		return nil
	}
	sqlDB, err := a.db.DB()
	if err != nil {
		return err
	}
	d := a.dbDialect()
	if _, err := sqlDB.Exec("DELETE FROM revoked_tokens WHERE expires_at < "+d.Placeholder(1), time.Now().Unix()); err != nil {
		a.logger.Warn("failed to prune expired revoked tokens", zap.Error(err))
	}
	upsert := d.Upsert("revoked_tokens", []string{"jti", "expires_at"}, []string{"jti"}, []string{"expires_at"})
	_, err = sqlDB.Exec(upsert, jti, expiresAt.Unix())
	return err
}

// isTokenRevoked reports whether jti is in the revocation store and not yet
// expired. Fails safe (false) if the store is unset or errors, so a
// revocation-store outage degrades to "not revoked" rather than rejecting
// every request.
func (a *Authenticator) isTokenRevoked(jti string) bool {
	if a.db == nil {
		return false
	}
	sqlDB, err := a.db.DB()
	if err != nil {
		return false
	}
	d := a.dbDialect()
	query := "SELECT COUNT(*) FROM revoked_tokens WHERE jti = " + d.Placeholder(1) + " AND expires_at > " + d.Placeholder(2)
	var count int
	if err := sqlDB.QueryRow(query, jti, time.Now().Unix()).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

// RefreshToken validates a refresh token, rotates it, and returns new tokens.
func (a *Authenticator) RefreshToken(ctx context.Context, refreshToken string) (string, string, time.Time, error) {
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(refreshToken)))

	// Raw, dialect-safe read/delete to stay consistent with the raw session
	// writes (GORM and raw do not share a view under the custom SQLite
	// dialector).
	sqlDB, err := a.db.DB()
	if err != nil {
		return "", "", time.Time{}, ErrSessionExpired
	}
	d := a.dbDialect()
	var userID uint
	if err := sqlDB.QueryRow(
		"SELECT user_id FROM sessions WHERE token_hash = "+d.Placeholder(1)+" AND expires_at > "+d.Placeholder(2),
		tokenHash, time.Now().UTC()).Scan(&userID); err != nil {
		return "", "", time.Time{}, ErrSessionExpired
	}

	if _, err := sqlDB.Exec("DELETE FROM sessions WHERE token_hash = "+d.Placeholder(1), tokenHash); err != nil {
		return "", "", time.Time{}, fmt.Errorf("rotate refresh session: %w", err)
	}

	accessToken, accessJTI, _, err := a.GenerateAccessTokenWithJTI(userID, RoleUser)
	if err != nil {
		return "", "", time.Time{}, err
	}

	newRefresh, expiresAt, err := a.GenerateRefreshToken(userID, accessJTI)
	if err != nil {
		return "", "", time.Time{}, err
	}

	return accessToken, newRefresh, expiresAt, nil
}

// InvalidateAllSessions deletes all sessions for a user.
func (a *Authenticator) InvalidateAllSessions(userID uint) error {
	sqlDB, err := a.db.DB()
	if err != nil {
		return fmt.Errorf("failed to invalidate sessions: %w", err)
	}
	d := a.dbDialect()
	if _, err := sqlDB.Exec("DELETE FROM sessions WHERE user_id = "+d.Placeholder(1), userID); err != nil {
		return fmt.Errorf("failed to invalidate sessions: %w", err)
	}
	a.logger.Info("all sessions invalidated", zap.Uint("user_id", userID))
	return nil
}

// InvalidateOtherSessions deletes all sessions except the one with the given token hash.
func (a *Authenticator) InvalidateOtherSessions(userID uint, currentTokenHash string) error {
	sqlDB, err := a.db.DB()
	if err != nil {
		return fmt.Errorf("failed to invalidate other sessions: %w", err)
	}
	d := a.dbDialect()
	if _, err := sqlDB.Exec("DELETE FROM sessions WHERE user_id = "+d.Placeholder(1)+" AND token_hash != "+d.Placeholder(2), userID, currentTokenHash); err != nil {
		return fmt.Errorf("failed to invalidate other sessions: %w", err)
	}
	a.logger.Info("other sessions invalidated", zap.Uint("user_id", userID))
	return nil
}

// HashPassword hashes a password using Argon2id via the centralized
// password package. Returns the encoded hash or an error.
func (a *Authenticator) HashPassword(password string) (string, error) {
	return HashPassword(password)
}

// VerifyPassword verifies a password against an encoded hash.
// Supports Argon2id ($argon2id$) and bcrypt ($2a$, $2b$, $2y$) formats.
// Returns true if the password matches.
func (a *Authenticator) VerifyPassword(password, encoded string) bool {
	return VerifyPasswordWithRehash(password, encoded).Valid
}

// VerifyPasswordWithRehash verifies a password and returns the full
// PasswordVerificationResult including the NeedsRehash flag.
// Use this when the caller must persist a new Argon2id hash after
// a successful bcrypt login (rehash-on-login).
func VerifyPasswordWithRehash(password, encoded string) PasswordVerificationResult {
	result, err := VerifyPassword(encoded, password)
	if err != nil {
		return PasswordVerificationResult{}
	}
	return result
}

func splitHash(encoded string) []string {
	for i := 0; i < len(encoded); i++ {
		if encoded[i] == ':' {
			return []string{encoded[:i], encoded[i+1:]}
		}
	}
	return nil
}

// Middleware returns a Fiber middleware that validates JWT access tokens
// or opaque session cookies. If the request was already authenticated via
// API key (auth_method set), it skips validation.
func (a *Authenticator) Middleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Locals("auth_method") != nil {
			return c.Next()
		}

		token := c.Cookies("access_token")
		if token == "" {
			authHeader := c.Get("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			}
		}

		if token != "" {
			userID, role, err := a.ValidateAccessToken(token)
			if err == nil {
				c.Locals("user_id", userID)
				c.Locals("role", role)
				return c.Next()
			}
		}

		// Fall back to opaque session cookie. The session row
		// stores the user id, the role, and the email captured at
		// login time so middleware can restore every one of them
		// without trusting any client-supplied claim. The role
		// restoration is the contract the /api/v1/internal/* super
		// admin gate and the /api/v1/admin/* admin gate both rely
		// on; a hard-coded "role: user" fallback would silently
		// open every gated route.
		sessionToken := c.Cookies("__Host-orvix_session")
		if sessionToken != "" {
			userID, role, email, err := a.ValidateOpaqueSession(sessionToken)
			if err == nil {
				c.Locals("user_id", userID)
				c.Locals("role", role)
				if email != "" {
					c.Locals("email", email)
				}
				return c.Next()
			}
		}

		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "missing or invalid authentication token",
		})
	}
}

// RequireRole returns a middleware that checks for a specific role.
func RequireRole(role Role) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRole, ok := c.Locals("role").(Role)
		if !ok || userRole != role {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "insufficient permissions",
			})
		}
		return c.Next()
	}
}

// RequireAnyRole returns a middleware that checks for any of the specified roles.
func RequireAnyRole(roles ...Role) fiber.Handler {
	return func(c fiber.Ctx) error {
		userRole, ok := c.Locals("role").(Role)
		if !ok {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "insufficient permissions",
			})
		}
		for _, r := range roles {
			if userRole == r {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "insufficient permissions",
		})
	}
}

// MFAChallengeTTL is the lifetime of an MFA challenge token.
const MFAChallengeTTL = 5 * time.Minute

// MFAChallengeClaim is the JWT claim name used to distinguish
// MFA challenge tokens from real access tokens. Access tokens
// carry "role"; challenge tokens carry "mfa_challenge" instead.
const MFAChallengeClaim = "mfa_challenge"

var mfaChallengeNow = time.Now

// SetMFAChallengeClockForTest overrides the MFA challenge clock and returns a
// restore function. It is intended for expiry tests only.
func SetMFAChallengeClockForTest(now func() time.Time) func() {
	prev := mfaChallengeNow
	mfaChallengeNow = now
	return func() { mfaChallengeNow = prev }
}

// GenerateMFAChallengeToken creates a short-lived token that proves
// the caller passed password authentication but has not yet completed
// MFA. The token MUST NOT be accepted by any protected endpoint.
// It is only usable with the MFA verify endpoint.
func (a *Authenticator) GenerateMFAChallengeToken(userID uint) (string, error) {
	now := mfaChallengeNow()
	claims := jwt.MapClaims{
		"sub":             fmt.Sprintf("%d", userID),
		MFAChallengeClaim: true,
		"iat":             now.Unix(),
		"exp":             now.Add(MFAChallengeTTL).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(a.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign MFA challenge token: %w", err)
	}
	return tokenString, nil
}

// ValidateMFAChallengeToken validates an MFA challenge token and
// returns the user ID. Returns an error if the token is invalid,
// expired, or is not an MFA challenge token.
// OpaqueSessionTTL is the 30-minute idle TTL applied to opaque
// HttpOnly session cookies. The session is renewed on every
// authenticated request so an actively-used browser stays signed in
// while a forgotten browser expires.
const OpaqueSessionTTL = 30 * time.Minute

// GenerateOpaqueSession creates an opaque session token, stores its
// SHA-256 hash server-side with a 30-minute idle TTL along with the
// caller-supplied role and email (so middleware can restore the real
// role on every request without trusting the client), and returns the
// raw token for use in the HttpOnly cookie. The raw token is never
// persisted — only its SHA-256 hash is written to the database, so a
// DB read alone cannot replay a session.
//
// role MUST be the server-derived role for the user (looked up from
// the users table at login time). It is the authoritative source the
// middleware reads back; passing the client's claim would defeat the
// purpose. email is the canonical user email and is included so
// future /me and audit calls do not need a second DB roundtrip.
func (a *Authenticator) GenerateOpaqueSession(userID uint, role Role, email string) (string, error) {
	if userID == 0 {
		return "", fmt.Errorf("generate opaque session: userID is required")
	}
	if role == "" {
		return "", fmt.Errorf("generate opaque session: role is required")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(b)
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	expiresAt := time.Now().Add(OpaqueSessionTTL)

	// Raw, dialect-safe INSERT: GORM Create on an anonymous struct silently
	// no-ops under the custom modernc SQLite dialector, which would drop the
	// session row. Raw SQL keeps opaque-session create/validate/revoke on one
	// consistent storage path across SQLite and PostgreSQL.
	sqlDB, err := a.db.DB()
	if err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	d := a.dbDialect()
	now := time.Now().UTC()
	insert := "INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, jti, expires_at) VALUES (" +
		d.Placeholders(9) + ")"
	if _, err := sqlDB.Exec(insert, now, now, userID, tokenHash, string(role), email, "", "", expiresAt); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}

	return token, nil
}

// ValidateOpaqueSession validates an opaque session token by looking
// up its SHA-256 hash in the sessions table. On success it returns
// the user ID, the server-persisted role, and the email so the
// middleware can populate c.Locals("role") and c.Locals("email")
// with the same values the session was created with. A row missing
// role (legacy data from before the column existed) is treated as
// expired so the browser is forced to re-authenticate through the
// login flow — the response always restores the real role, never a
// guess.
func (a *Authenticator) ValidateOpaqueSession(token string) (uint, Role, string, error) {
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))

	// Raw, dialect-safe read: matches GenerateOpaqueSession / the revoke
	// delete path so all three see the same rows on both engines.
	sqlDB, err := a.db.DB()
	if err != nil {
		return 0, "", "", ErrSessionExpired
	}
	d := a.dbDialect()
	var userID uint
	var role, email string
	sel := "SELECT user_id, role, email FROM sessions WHERE token_hash = " +
		d.Placeholder(1) + " AND expires_at > " + d.Placeholder(2)
	if err := sqlDB.QueryRow(sel, tokenHash, time.Now().UTC()).Scan(&userID, &role, &email); err != nil {
		return 0, "", "", ErrSessionExpired
	}

	// Defensive: refuse to honour sessions persisted before the role column
	// existed. Returning ErrSessionExpired forces a fresh session, after which
	// every request restores the real role.
	if role == "" {
		return 0, "", "", ErrSessionExpired
	}

	return userID, Role(role), email, nil
}

func (a *Authenticator) ValidateMFAChallengeToken(tokenString string) (uint, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.publicKey, nil
	})
	if err != nil || !token.Valid {
		return 0, ErrTokenInvalid
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, ErrTokenInvalid
	}
	// Must carry the MFA challenge claim.
	if val, _ := claims[MFAChallengeClaim].(bool); !val {
		return 0, fmt.Errorf("not an MFA challenge token")
	}
	exp, ok := claims["exp"].(float64)
	if ok && mfaChallengeNow().Unix() > int64(exp) {
		return 0, ErrTokenExpired
	}
	var userID uint
	fmt.Sscanf(claims["sub"].(string), "%d", &userID)
	return userID, nil
}
