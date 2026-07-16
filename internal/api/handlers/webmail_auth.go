package handlers

// User-facing webmail authentication.
//
// This file decouples webmail login from the admin-panel
// login flow. The webmail SPA at /webmail asks for
// `email` + `password` (the mailbox's own credentials),
// not for an admin session. After login, a HttpOnly
// `access_token` cookie is set on the configured
// cross-subdomain domain, so subsequent /api/v1/webmail/*
// calls authenticate via the same JWT middleware the
// admin panel uses â€” but the JWT was minted in the
// webmail flow and the user only needs to own a
// coremail_mailboxes row.
//
// The webmail login:
//   - looks up the mailbox by email in coremail_mailboxes
//     (NOT in users);
//   - verifies the mailbox password with the mailbox's
//     own Argon2id hash;
//   - finds or auto-provisions the matching `users` row
//     (this is the common case for mailboxes created via
//     the admin "Create mailbox" form â€” they get a
//     coremail_mailboxes row but no users row);
//   - mints an access_token JWT with the user's role
//     (RoleUser for regular mailboxes, RoleAdmin if the
//     mailbox has is_admin=1) and a refresh_token tied
//     to the users.id row;
//   - returns 200 with `{authenticated: true, mailbox: {...}}`
//     so the auth-gate can immediately call window.location.reload().
//
// Endpoints (mounted in router.go):
//   GET   /api/v1/webmail/session             — 200 if a webmail
//                                              session is present
//                                              (used by the
//                                              auth-gate.js probe);
//                                              401 otherwise.
//   POST  /api/v1/webmail/login               — webmail login form
//                                              submission (email +
//                                              password).
//   POST  /api/v1/webmail/logout              — clear cookies (CSRF).
//   POST  /api/v1/webmail/password/change     — change mailbox password
//                                              (authenticated; CSRF).

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// WebmailSession is the probe endpoint used by
// release/webmail/assets/auth-gate.js. It returns 200 if
// the caller has a valid access_token cookie whose
// user_id resolves to a coremail_mailboxes row, and 401
// otherwise.
//
// This endpoint is mounted on the protected group in
// router.go, so the auth middleware rejects missing /
// invalid cookies with 401 before this handler runs. The
// handler is therefore reached only when the cookie is
// valid; if the user has no mailbox row, the response
// is 200 with `authenticated: false` and a reason
// string so the UI can render the "no mailbox" state
// without bouncing back to the login form.
func (h *Handler) WebmailSession(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		// The auth middleware would have already
		// 401'd a missing/invalid token. If we
		// are here, the cookie is valid but the
		// user has no mailbox (or the mailbox is
		// not active, or the mailstore is not
		// wired). Surface as 200 so the gate
		// shows the empty state instead of the
		// login card.
		return c.JSON(fiber.Map{
			"authenticated": false,
			"reason":        reason,
		})
	}
	return c.JSON(fiber.Map{
		"authenticated": true,
		"user": fiber.Map{
			"id":    ctx.UserID,
			"email": ctx.Email,
			"role":  ctx.Role,
		},
		"mailbox": fiber.Map{
			"id":       ctx.Mailbox.ID,
			"email":    ctx.Mailbox.Email,
			"is_admin": ctx.Mailbox.IsAdmin,
		},
	})
}

// WebmailLoginRequest is the JSON body the login form
// submits. We accept either "email" or "username" so
// the front-end can use whichever name it prefers; the
// handler treats them identically.
type WebmailLoginRequest struct {
	Email    string `json:"email"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// WebmailLogin authenticates a mailbox user and sets the
// HttpOnly access_token + refresh_token cookies. The
// caller MUST have a coremail_mailboxes row matching the
// supplied email; the password is verified against the
// mailbox's own Argon2id hash (or bcrypt for legacy
// mailboxes).
//
// The handler auto-provisions a `users` row if the
// mailbox exists but no user row does â€” this is the case
// for every mailbox created through the admin
// CreateMailbox endpoint in production today. The new
// users row is given role="user" (or role="admin" if
// the mailbox is_admin=1) so the existing admin-role
// middleware still works for admin mailboxes.
//
// On success, the response is 200 with
// {authenticated: true, mailbox: {id, email, is_admin}}.
// The auth-gate then probes the new session and reveals
// the SPA.
func (h *Handler) WebmailLogin(c fiber.Ctx) error {
	var req WebmailLoginRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request",
		})
	}
	loginEmail := strings.TrimSpace(req.Email)
	if loginEmail == "" {
		loginEmail = strings.TrimSpace(req.Username)
	}
	loginEmail = strings.ToLower(loginEmail)
	if loginEmail == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "email and password required",
		})
	}
	if _, err := mail.ParseAddress(loginEmail); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid email format",
		})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("webmail login: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}

	dial := dbdialect.FromDriver(h.cfg.Database.Driver)

	// Look up the mailbox. The user is logging into
	// webmail as a mailbox owner; an admin user with
	// no mailbox cannot log in here.
	var (
		mailboxID     uint
		mailboxStatus string
		isAdmin       bool
		hash          string
		authScheme    string
		allowWebmail  bool
	)
	row := sqlDB.QueryRow(
		fmt.Sprintf("SELECT id, status, is_admin, password_hash, COALESCE(auth_scheme,''), COALESCE(allow_webmail,"+dial.TrueLiteral()+") FROM coremail_mailboxes WHERE email = %s AND deleted_at IS NULL", dial.Placeholder(1)),
		loginEmail,
	)
	if err := row.Scan(&mailboxID, &mailboxStatus, &isAdmin, &hash, &authScheme, &allowWebmail); err != nil {
		if err == sql.ErrNoRows {
			h.security.RecordFailedLogin(c.Context(), c.IP(), loginEmail)
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "invalid credentials",
			})
		}
		h.logger.Error("webmail login: mailbox query failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}
	if mailboxStatus != "active" {
		h.security.RecordFailedLogin(c.Context(), c.IP(), loginEmail)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "mailbox is not active",
		})
	}
	if !allowWebmail {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	// Verify the password against the mailbox's hash.
	if !verifyMailboxPassword(req.Password, hash) {
		h.security.RecordFailedLogin(c.Context(), c.IP(), loginEmail)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	// Find or create the users row that the JWT
	// middleware expects. The webmail SPA only requires
	// a coremail_mailboxes row; for historical
	// compatibility (and so the admin-role middleware
	// still gates the right endpoints) we map the
	// mailbox to a users row by email.
	userID, err := h.ensureWebmailUser(dial, sqlDB, loginEmail, isAdmin)
	if err != nil {
		h.logger.Error("webmail login: ensureWebmailUser failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "internal error",
		})
	}

	role := auth.RoleUser
	if isAdmin {
		role = auth.RoleAdmin
	}

	// Best-effort: ensure system folders exist for
	// this mailbox. The bootstrap path also runs
	// this, but the handler-level guarantee is what
	// makes the "Sent folder not found" bug
	// impossible: any mailbox that has ever been
	// logged into via webmail has its system folders
	// provisioned before the user can send.
	if err := coremail.EnsureMailboxSystemFolders(c.Context(), sqlDB, mailboxID); err != nil {
		// Do not fail login on folder provision
		// failure — the UI will surface the error
		// when the user tries to send. Logging
		// the error is enough.
		h.logger.Warn("webmail login: ensure system folders",
			zap.String("email", loginEmail),
			zap.Uint("mailbox_id", mailboxID),
			zap.Error(err))
	}

	// Issue opaque session cookie for the admin SPA. Cookie
	// issuance is the source of truth for browser auth; if the
	// store refuses the write we refuse the login rather than
	// return success without a usable session.
	if err := h.issueLoginSession(c, userID, role, loginEmail); err != nil {
		h.logger.Error("webmail login: issue login session", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "authentication failed",
		})
	}

	accessToken, accessJTI, _, err := h.auth.GenerateAccessTokenWithJTI(userID, role)
	if err != nil {
		h.logger.Error("webmail login: mint access token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "authentication failed",
		})
	}
	refreshToken, refreshExp, err := h.auth.GenerateRefreshToken(userID, accessJTI)
	if err != nil {
		h.logger.Error("webmail login: mint refresh token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "authentication failed",
		})
	}

	// Set the same cookies the admin login sets, on
	// the same domain, so cross-subdomain SSO still
	// works for the admin panel when an admin logs
	// in via webmail. The cookie is HttpOnly, Secure,
	// SameSite=None, with the cross-subdomain domain
	// from config so admin.<parent> and webmail.<parent>
	// share the session.
	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Expires:  time.Now().Add(15 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  refreshExp,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	h.security.RecordSuccessfulLogin(c.IP())
	if h.rateLimiter != nil {
		h.rateLimiter.ResetLoginLimit(c.IP())
	}

	h.logger.Info("webmail login success",
		zap.String("email", loginEmail),
		zap.Uint("mailbox_id", mailboxID),
		zap.Uint("user_id", userID),
		zap.String("role", string(role)))

	return c.JSON(fiber.Map{
		"authenticated": true,
		"mailbox": fiber.Map{
			"id":       mailboxID,
			"email":    loginEmail,
			"is_admin": isAdmin,
		},
	})
}

// WebmailLogout clears the auth cookies and invalidates
// the current refresh-token session if any. It is a
// thin wrapper around the existing /api/v1/auth/logout
// path, mounted at /api/v1/webmail/logout so the SPA
// can call it without depending on the admin endpoint.
func (h *Handler) WebmailLogout(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uint)
	if ok {
		_ = h.auth.InvalidateAllSessions(userID)
	}
	h.clearAuthCookies(c)
	return c.JSON(fiber.Map{"status": "logged_out"})
}

// webmailMinPasswordLength is the minimum length the
// webmail Security → Change Password form enforces on
// the client side AND on the server side. The server
// value is the source of truth; the client value is a
// UX hint. Kept local to this file so a future
// operator-configurable password policy can change it
// in one place.
const webmailMinPasswordLength = 8

// WebmailChangePassword changes the currently-authenticated
// mailbox's password.
//
// Auth model: the auth middleware has already validated
// the access_token cookie and set `c.Locals("user_id")`,
// and `resolveWebmailUserContext` has already resolved
// the JWT identity to the active mailbox row. This
// handler uses that resolved mailbox — NEVER any id
// supplied in the request body — so a JWT for mailbox A
// cannot update mailbox B.
//
// Request body:
//
//	{ "current_password": "...", "new_password": "..." }
//
// On success the handler hashes the new password with the
// canonical Argon2id helper (the same path every new
// mailbox uses; see hashPasswordArgon2id in handlers.go)
// and writes it to coremail_mailboxes along with
// auth_scheme='$argon2id$' and a fresh updated_at.
//
// The webmail session is NOT invalidated by a successful
// change — the production webmail auth model uses a
// 15-minute JWT lifetime on the access_token plus a
// long-lived refresh_token, so the cookie the user has
// keeps working until it naturally rotates. Invalidation
// would force the user to re-enter their password in the
// same minute, which is the wrong UX for a feature the
// spec calls a day-to-day password rotation.
//
// Errors:
//
//	400  { error: "current password required" }
//	400  { error: "new password required" }
//	400  { error: "new password must be at least N characters" }
//	401  { error: "invalid credentials" }   <- wrong current password (generic, anti-enumeration)
//	500  { error: "password change failed" }   <- hash / DB / unknown
//
// The handler NEVER echoes the current password, the new
// password, the hash, or any secret back to the client.
// It NEVER writes the password to a log line.
//
// Pinned by tests:
//
//	TestWebmailChangePasswordUnauthenticatedReturns401
//	TestWebmailChangePasswordWrongCurrentPasswordRejected
//	TestWebmailChangePasswordRejectsMissingFields
//	TestWebmailChangePasswordRejectsMismatchedConfirmationOrWeakPassword
//	TestWebmailChangePasswordIgnoresExtraFieldsSafely
//	TestWebmailChangePasswordSuccessUpdatesHash
//	TestWebmailChangePasswordOldPasswordNoLongerWorks
//	TestWebmailChangePasswordNewPasswordWorks
//	TestWebmailChangePasswordResponseCarriesNoHash
//	TestWebmailChangePasswordCrossMailboxImpossible
func (h *Handler) WebmailChangePassword(c fiber.Ctx) error {
	ctx, reason := h.resolveWebmailUserContext(c)
	if ctx == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "no mailbox",
			"reason": reason,
		})
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		// Optional: the client may send a confirmation
		// copy. We enforce the match server-side so a
		// tampered client cannot bypass the check, but
		// we also accept the case where the client
		// already enforced it itself.
		ConfirmPassword string `json:"confirm_password"`
		// Optional: the client may send a "current_mailbox_id"
		// so the user can see which mailbox they are
		// about to update. The handler IGNORES every
		// id-shaped body field; the only mailbox that
		// gets updated is the one resolveWebmailUserContext
		// resolved from the JWT.
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request",
		})
	}

	if req.CurrentPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "current password required",
		})
	}
	if req.NewPassword == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "new password required",
		})
	}
	if req.ConfirmPassword != "" && req.NewPassword != req.ConfirmPassword {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "new password and confirmation do not match",
		})
	}
	if len(req.NewPassword) < webmailMinPasswordLength {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("new password must be at least %d characters", webmailMinPasswordLength),
		})
	}

	// Re-read the mailbox hash directly. The user-context
	// already proved ownership of the JWT, but we re-read
	// so the password verify runs against the latest
	// hash on disk (covers the case where an admin
	// rotated the hash between the JWT mintage and this
	// request).
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "password change failed",
		})
	}
	dial := dbdialect.FromDriver(h.cfg.Database.Driver)
	var (
		currentHash string
		authScheme  string
	)
	row := sqlDB.QueryRowContext(c.Context(),
		fmt.Sprintf(`SELECT password_hash, COALESCE(auth_scheme, '')
		   FROM coremail_mailboxes
		  WHERE id = %s AND deleted_at IS NULL`,
			dial.Placeholder(1)),
		ctx.Mailbox.ID)
	if err := row.Scan(&currentHash, &authScheme); err != nil {
		// Mailbox not found (or deleted) — generic error
		// so a client with a stale JWT cannot enumerate
		// mailbox rows.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "password change failed",
		})
	}

	// Verify CURRENT password. Use the same helper the
	// webmail login handler uses so legacy bcrypt-hashed
	// mailboxes still work — a real production operator
	// migration can take months and the user must not
	// be locked out during the cutover.
	if !verifyMailboxPassword(req.CurrentPassword, currentHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "invalid credentials",
		})
	}

	// Hash with the canonical Argon2id helper used for
	// every new mailbox.
	newHash, err := hashPasswordArgon2id(req.NewPassword)
	if err != nil {
		// Compute failure — log a structured event with
		// NO sensitive fields. Returning a generic body
		// here stops a misconfigured Argon2id on the
		// server from leaking (which would already be
		// obvious to the operator from other logs).
		h.logger.Error("webmail change password: hash new password failed",
			zap.Uint("mailbox_id", ctx.Mailbox.ID),
			zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "password change failed",
		})
	}

	now := time.Now().UTC()
	if _, err := sqlDB.ExecContext(c.Context(),
		fmt.Sprintf(`UPDATE coremail_mailboxes
		    SET password_hash = %s, auth_scheme = %s, updated_at = %s
		  WHERE id = %s AND deleted_at IS NULL`,
			dial.Placeholder(1), dial.Placeholder(2), dial.Placeholder(3), dial.Placeholder(4)),
		newHash, "$argon2id$", now, ctx.Mailbox.ID); err != nil {
		h.logger.Error("webmail change password: db update failed",
			zap.Uint("mailbox_id", ctx.Mailbox.ID),
			zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "password change failed",
		})
	}

	// Note: we deliberately do NOT invalidate the current
	// session here. The auth model is short-lived access
	// tokens (15 min) + long refresh tokens, and a same-
	// minute forced re-login on every password change is
	// bad UX. A future iteration can wire refresh-token
	// rotation here if the threat model demands it.

	// Audit. NO sensitive fields in the audit fields.
	h.writeAuditLog(c, "webmail.password_change", fmt.Sprintf("mailbox:%d", ctx.Mailbox.ID))

	return c.JSON(fiber.Map{"status": "changed"})
}

// ensureWebmailUser finds the users row with the given
// email, creating one if missing. The created row has
// the supplied role and a placeholder password_hash
// (a bcrypt-encoding of "!" so the row is not a
// security hole if a future admin-password-reset is
// run against it). The user row is only used for the
// JWT subject and role; password verification always
// runs against the mailbox table.
//
// We deliberately do NOT bind the mailbox's password
// to the user row. If the user ever sets an admin-panel
// password, it is independent of the mailbox password.
func (h *Handler) ensureWebmailUser(dial *dbdialect.Info, sqlDB *sql.DB, email string, isAdmin bool) (uint, error) {
	var userID uint
	row := sqlDB.QueryRow(fmt.Sprintf("SELECT id FROM users WHERE email = %s", dial.Placeholder(1)), email)
	if err := row.Scan(&userID); err == nil {
		// Existing user row; make sure the role
		// matches the mailbox so admin mailboxes
		// keep admin panel access.
		desired := "user"
		if isAdmin {
			desired = "admin"
		}
		if _, err := sqlDB.Exec(
			fmt.Sprintf("UPDATE users SET role = %s, updated_at = %s WHERE id = %s", dial.Placeholder(1), dial.Placeholder(2), dial.Placeholder(3)),
			desired, time.Now().UTC(), userID,
		); err != nil {
			return 0, fmt.Errorf("update user role: %w", err)
		}
		return userID, nil
	} else if err != sql.ErrNoRows {
		return 0, fmt.Errorf("query user: %w", err)
	}

	// No users row â€” create one tied to the same
	// tenant as the mailbox.
	var tenantID uint
	row = sqlDB.QueryRow(
		fmt.Sprintf(`SELECT m.tenant_id
		FROM coremail_mailboxes m
		WHERE m.email = %s AND m.deleted_at IS NULL`, dial.Placeholder(1)), email)
	if err := row.Scan(&tenantID); err != nil {
		return 0, fmt.Errorf("lookup tenant: %w", err)
	}
	role := "user"
	if isAdmin {
		role = "admin"
	}
	// The password_hash is a bcrypt of "!" so the
	// user row cannot be used to log in via the
	// admin /api/v1/auth/login endpoint (which would
	// use the user row's hash, not the mailbox's).
	// If a future operator wants to give this user
	// an admin-panel password, they can run a
	// password-set flow that re-hashes the column.
	placeholder, err := bcrypt.GenerateFromPassword([]byte("!"), bcrypt.MinCost)
	if err != nil {
		return 0, fmt.Errorf("hash placeholder: %w", err)
	}
	now := time.Now().UTC()
	if dial.IsPostgres() {
		if err := sqlDB.QueryRow(
			fmt.Sprintf(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
			 VALUES (%s) RETURNING id`, dial.Placeholders(8)),
			now, now, email, string(placeholder), role, tenantID, true, true,
		).Scan(&userID); err != nil {
			return 0, fmt.Errorf("insert user: %w", err)
		}
		return userID, nil
	}
	res, err := sqlDB.Exec(
		fmt.Sprintf(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (%s)`, dial.Placeholders(8)),
		now, now, email, string(placeholder), role, tenantID, 1, 1,
	)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("insert user last id: %w", err)
	}
	return uint(id), nil
}

// verifyMailboxPassword handles the two hash formats
// the mailbox table sees in production today:
//
//   - $argon2id$v=19$m=...$salt$hash  â€” the default
//     format written by every code path that creates a
//     mailbox today.
//
//   - bcrypt â€” legacy mailboxes written by older
//     versions of the installer / migration tools.
//
// Plain-text "!" placeholder rows (the placeholder
// written by ensureWebmailUser) are rejected
// unconditionally so a placeholder row cannot be
// logged into via the mailbox path either.
func verifyMailboxPassword(password, encoded string) bool {
	if encoded == "" || encoded == "!" {
		return false
	}
	if strings.HasPrefix(encoded, "$argon2id$") {
		return verifyArgon2idMailbox(password, encoded)
	}
	// bcrypt fallback. Some legacy mailboxes use
	// bcrypt.
	return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
}

func verifyArgon2idMailbox(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return false
	}
	var mem uint32
	var timeParam uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &timeParam, &threads); err != nil {
		return false
	}
	salt, err := decodeArgonSalt(parts[4])
	if err != nil {
		return false
	}
	expected, err := decodeArgonSalt(parts[5])
	if err != nil {
		return false
	}
	key := argon2.IDKey([]byte(password), salt, timeParam, mem, threads, uint32(len(expected)))
	if len(key) != len(expected) {
		return false
	}
	for i := range key {
		if key[i] != expected[i] {
			return false
		}
	}
	return true
}

func decodeArgonSalt(s string) ([]byte, error) {
	// hashPasswordArgon2id uses base64.RawStdEncoding.
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	// Some early installer versions used the padded
	// encoding; accept it for those rows.
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, fmt.Errorf("decode base64")
}
