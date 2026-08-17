package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// Phase D/E: email-OTP-verified signup.
//
// pending_registrations holds an unconfirmed signup: no tenant/user/
// subscription row exists for it yet. Only after SignupVerify succeeds does
// the atomic activation (mirroring the existing immediate Signup handler's
// transaction) create the real tenant/user/subscription. The OTP itself is
// NEVER stored in plaintext — only an HMAC-SHA256 digest keyed with the
// server's JWT secret (the codebase's existing server-side secret; there is
// no separate "OTP pepper" convention to reuse, so this reuses the same
// secret already trusted to protect access tokens).
const otpValidityWindow = 10 * time.Minute
const otpResendCooldown = 60 * time.Second
const otpMaxAttempts = 5

func pendingRegistrationsDDL(d *dbdialect.Info) string {
	auto := d.AutoIncrement()
	ts := d.TimestampType()
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS pending_registrations (
		id %s,
		normalized_email TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		otp_hash TEXT NOT NULL,
		otp_expires_at %s NOT NULL,
		otp_attempts INTEGER NOT NULL DEFAULT 0,
		otp_created_at %s NOT NULL,
		consumed_at %s,
		created_at %s NOT NULL DEFAULT %s
	)`, auto, ts, ts, ts, ts, d.NowExpr())
}

func (h *Handler) ensurePendingRegistrationsTable(sqlDB *sql.DB, dial *dbdialect.Info) error {
	_, err := sqlDB.Exec(pendingRegistrationsDDL(dial))
	return err
}

// generateOTP returns a cryptographically random 6-digit code, zero-padded.
func generateOTP() (string, error) {
	max := int64(1000000)
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	n := int64(buf[0])<<24 | int64(buf[1])<<16 | int64(buf[2])<<8 | int64(buf[3])
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%06d", n%max), nil
}

// hashOTP returns the HMAC-SHA256 digest of the code, keyed with the
// server's JWT secret. Never store or log the plaintext code.
func (h *Handler) hashOTP(code string) string {
	mac := hmac.New(sha256.New, []byte(h.cfg.Auth.JWTSecret))
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

type signupStartRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// SignupStart begins email-OTP-verified signup. It validates input,
// normalizes the email, runs the same platform-identity check as the
// immediate Signup path, rejects existing active-user emails generically,
// then upserts a pending_registration row with a freshly minted OTP and
// emails it via the existing transactional mail sender.
// POST /auth/signup/start
func (h *Handler) SignupStart(c fiber.Ctx) error {
	var req signupStartRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}
	if err := passwordStrength(req.Password); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("signup/start: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()

	if err := h.ensurePendingRegistrationsTable(sqlDB, dial); err != nil {
		h.logger.Error("signup/start: ensure table", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	// Platform identity protection — same check as the immediate Signup
	// path (Phase C), generic failure either way.
	if blocked, err := h.emailBelongsToPlatformIdentity(sqlDB, dial, email); err != nil {
		h.logger.Error("signup/start: platform identity check", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	} else if blocked {
		h.logger.Warn("signup/start: rejected — email matches a platform identity", zap.String("email", email))
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "unable to complete signup"})
	}

	// Existing ACTIVE user with this email — generic failure, do not leak
	// existence.
	var existingUsers int64
	if err := sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE email = %s", dial.Placeholder(1)), email).Scan(&existingUsers); err != nil {
		h.logger.Error("signup/start: check duplicate", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if existingUsers > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "unable to complete signup"})
	}

	// 60s resend cooldown: if a pending registration already exists and was
	// (re)issued less than otpResendCooldown ago, reject.
	var lastIssuedAt sql.NullTime
	err = sqlDB.QueryRow(fmt.Sprintf("SELECT otp_created_at FROM pending_registrations WHERE normalized_email = %s", dial.Placeholder(1)), email).Scan(&lastIssuedAt)
	if err != nil && err != sql.ErrNoRows {
		h.logger.Error("signup/start: check existing pending registration", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if lastIssuedAt.Valid && time.Since(lastIssuedAt.Time) < otpResendCooldown {
		retryAfter := int(otpResendCooldown.Seconds() - time.Since(lastIssuedAt.Time).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":       "please wait before requesting another code",
			"retry_after": retryAfter,
		})
	}

	pwHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("signup/start: hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	code, err := generateOTP()
	if err != nil {
		h.logger.Error("signup/start: generate otp", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	otpHash := h.hashOTP(code)

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = loginDomain(email)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(otpValidityWindow)

	// Resend invalidates the prior OTP: this upsert overwrites otp_hash,
	// otp_expires_at, otp_attempts (reset to 0) and otp_created_at.
	upsertSQL := dial.Upsert(
		"pending_registrations",
		[]string{"normalized_email", "name", "password_hash", "otp_hash", "otp_expires_at", "otp_attempts", "otp_created_at", "created_at"},
		[]string{"normalized_email"},
		[]string{"name", "password_hash", "otp_hash", "otp_expires_at", "otp_attempts", "otp_created_at"},
	)
	if _, err := sqlDB.Exec(upsertSQL, email, name, string(pwHash), otpHash, expiresAt, 0, now, now); err != nil {
		h.logger.Error("signup/start: upsert pending registration", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	h.sendOTPEmail(email, code)

	// Never log the OTP plaintext.
	h.logger.Info("signup/start: pending registration issued", zap.String("email", email))

	return c.Status(fiber.StatusOK).JSON(fiber.Map{
		"message": "verification code sent",
		"email":   email,
	})
}

// sendOTPEmail sends the OTP via the existing transactional mail pipeline.
// Follows the same from-address / logging conventions as ForgotPassword.
func (h *Handler) sendOTPEmail(email, code string) {
	if h.mailSender == nil {
		h.logger.Warn("signup: no mail sender configured — verification email not delivered", zap.String("email", email))
		return
	}
	body := fmt.Sprintf("Your Orvix verification code is: %s\n\nThis code expires in %d minutes. If you didn't request this, you can safely ignore this email.", code, int(otpValidityWindow.Minutes()))
	if err := h.mailSender.Send(email, "Your Orvix verification code", body); err != nil {
		h.logger.Error("signup: send otp email", zap.Error(err))
		return
	}
	h.logger.Info("signup: otp email sent", zap.String("email", email))
}

type signupResendRequest struct {
	Email string `json:"email"`
}

// SignupResend re-issues a new OTP for an existing pending registration,
// enforcing the same 60s cooldown and invalidating the prior code.
// POST /auth/signup/resend
func (h *Handler) SignupResend(c fiber.Ctx) error {
	var req signupResendRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email is required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("signup/resend: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()
	if err := h.ensurePendingRegistrationsTable(sqlDB, dial); err != nil {
		h.logger.Error("signup/resend: ensure table", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	var name, pwHash string
	var lastIssuedAt time.Time
	err = sqlDB.QueryRow(
		fmt.Sprintf("SELECT name, password_hash, otp_created_at FROM pending_registrations WHERE normalized_email = %s AND consumed_at IS NULL", dial.Placeholder(1)),
		email,
	).Scan(&name, &pwHash, &lastIssuedAt)
	if err == sql.ErrNoRows {
		// Generic response — do not leak whether a pending registration exists.
		return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "if a pending signup exists, a new code has been sent"})
	}
	if err != nil {
		h.logger.Error("signup/resend: lookup pending registration", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	if time.Since(lastIssuedAt) < otpResendCooldown {
		retryAfter := int(otpResendCooldown.Seconds() - time.Since(lastIssuedAt).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Set("Retry-After", fmt.Sprintf("%d", retryAfter))
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":       "please wait before requesting another code",
			"retry_after": retryAfter,
		})
	}

	code, err := generateOTP()
	if err != nil {
		h.logger.Error("signup/resend: generate otp", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	otpHash := h.hashOTP(code)
	now := time.Now().UTC()
	expiresAt := now.Add(otpValidityWindow)

	if _, err := sqlDB.Exec(
		fmt.Sprintf("UPDATE pending_registrations SET otp_hash = %s, otp_expires_at = %s, otp_attempts = 0, otp_created_at = %s WHERE normalized_email = %s",
			dial.Placeholder(1), dial.Placeholder(2), dial.Placeholder(3), dial.Placeholder(4)),
		otpHash, expiresAt, now, email,
	); err != nil {
		h.logger.Error("signup/resend: update pending registration", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	h.sendOTPEmail(email, code)
	h.logger.Info("signup/resend: pending registration re-issued", zap.String("email", email))

	return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "if a pending signup exists, a new code has been sent"})
}

type signupVerifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// SignupVerify checks the submitted code against a pending registration
// and, on success, performs the atomic activation: create user + tenant +
// subscription in one transaction (mirroring Signup's existing proven
// pattern), mark the pending registration consumed, and return a session
// exactly like the old immediate-signup path.
// POST /auth/signup/verify
func (h *Handler) SignupVerify(c fiber.Ctx) error {
	var req signupVerifyRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	code := strings.TrimSpace(req.Code)
	if email == "" || code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and code are required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("signup/verify: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()
	if err := h.ensurePendingRegistrationsTable(sqlDB, dial); err != nil {
		h.logger.Error("signup/verify: ensure table", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	var pendingID int64
	var name, pwHash, otpHash string
	var expiresAt time.Time
	var attempts int
	var consumedAt sql.NullTime
	err = sqlDB.QueryRow(
		fmt.Sprintf("SELECT id, name, password_hash, otp_hash, otp_expires_at, otp_attempts, consumed_at FROM pending_registrations WHERE normalized_email = %s", dial.Placeholder(1)),
		email,
	).Scan(&pendingID, &name, &pwHash, &otpHash, &expiresAt, &attempts, &consumedAt)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired code"})
	}
	if err != nil {
		h.logger.Error("signup/verify: lookup pending registration", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	// Single-use: a concurrent/replayed verify against an already-consumed
	// registration fails cleanly instead of double-activating.
	if consumedAt.Valid {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired code"})
	}

	if attempts >= otpMaxAttempts {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "too many attempts — request a new code"})
	}

	if time.Now().UTC().After(expiresAt) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired code"})
	}

	submittedHash := h.hashOTP(code)
	if subtle.ConstantTimeCompare([]byte(submittedHash), []byte(otpHash)) != 1 {
		if _, err := sqlDB.Exec(
			fmt.Sprintf("UPDATE pending_registrations SET otp_attempts = otp_attempts + 1 WHERE id = %s AND consumed_at IS NULL", dial.Placeholder(1)),
			pendingID,
		); err != nil {
			h.logger.Error("signup/verify: increment attempts", zap.Error(err))
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired code"})
	}

	// Atomically mark this pending registration consumed BEFORE activation.
	// The UPDATE ... WHERE consumed_at IS NULL is the concurrency guard: if
	// two verify calls race with the same correct code, only one UPDATE
	// affects a row; the loser sees rowsAffected == 0 and fails cleanly
	// instead of double-activating.
	consumeRes, err := sqlDB.Exec(
		fmt.Sprintf("UPDATE pending_registrations SET consumed_at = %s WHERE id = %s AND consumed_at IS NULL", dial.Placeholder(1), dial.Placeholder(2)),
		time.Now().UTC(), pendingID,
	)
	if err != nil {
		h.logger.Error("signup/verify: mark consumed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if n, _ := consumeRes.RowsAffected(); n == 0 {
		// Lost the race to another concurrent verify call.
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired code"})
	}

	// Re-check platform identity + duplicate-user immediately before
	// activation (defense in depth against a race between signup/start and
	// verify — e.g. an admin account created in the interim).
	if blocked, err := h.emailBelongsToPlatformIdentity(sqlDB, dial, email); err != nil {
		h.logger.Error("signup/verify: platform identity check", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	} else if blocked {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "unable to complete signup"})
	}

	userID, tenantID, err := h.activatePendingRegistration(c, sqlDB, dial, email, name, pwHash)
	if err != nil {
		h.logger.Error("signup/verify: activation failed", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
	}

	if h.auditStore != nil {
		if err := h.auditStore.Record(c.Context(), &audit.Entry{
			Actor:     fmt.Sprintf("user:%d", userID),
			Action:    "customer.signup",
			Target:    fmt.Sprintf("user:%d|email:%s|tenant:%d", userID, email, tenantID),
			Result:    "success",
			IP:        c.IP(),
			UserAgent: c.Get("User-Agent"),
		}); err != nil {
			h.logger.Error("signup/verify: write audit", zap.Error(err))
		}
	}

	return h.issueSignupSession(c, userID, email)
}

// issueSignupSession mints the exact same access/refresh token pair and
// cookies as the real Login handler (handlers.go Login, ~line 903-966), so
// a verified signup lands the client in the organization portal without a
// second round trip through /auth/login. Kept as its own helper (rather
// than reusing issueLoginSession, which only sets the opaque-session
// cookie and returns no body) because Login's JWT-issuance block is not
// itself factored into a reusable function.
func (h *Handler) issueSignupSession(c fiber.Ctx, userID uint, email string) error {
	accessToken, accessJTI, issuedRole, err := h.auth.GenerateAccessTokenForUserWithJTI(userID)
	if err != nil {
		h.logger.Error("signup/verify: failed to generate access token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	if err := h.issueLoginSession(c, userID, issuedRole, email); err != nil {
		h.logger.Error("signup/verify: failed to issue login session", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	refreshToken, expiresAt, err := h.auth.GenerateRefreshToken(userID, accessJTI, c.IP(), c.Get("User-Agent"))
	if err != nil {
		h.logger.Error("signup/verify: failed to generate refresh token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Expires:  time.Now().Add(15 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Lax",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Lax",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	h.logger.Info("signup verified and session issued", zap.Uint("user_id", userID))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"access_token":       accessToken,
		"access_expires_in":  900,
		"refresh_expires_in": int(30 * 24 * 3600),
	})
}

// activatePendingRegistration runs the atomic tenant+user+subscription
// creation in one transaction, mirroring the existing Signup handler's
// proven-correct pattern exactly (see Signup in customer_auth.go). Rolls
// back entirely on any failure, leaving no orphan rows.
func (h *Handler) activatePendingRegistration(c fiber.Ctx, sqlDB *sql.DB, dial *dbdialect.Info, email, name, pwHash string) (uint, uint, error) {
	tx, err := sqlDB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	domain := loginDomain(email)
	tenantName := strings.TrimSpace(name)
	if tenantName == "" {
		tenantName = domain
	}
	slug := strings.NewReplacer(".", "-", "@", "-", " ", "-").Replace(strings.ToLower(tenantName))
	now := time.Now().UTC()

	var tenantID uint
	if dial.IsPostgres() {
		if err := tx.QueryRow(
			fmt.Sprintf("INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at) VALUES (%s) RETURNING id", dial.Placeholders(8)),
			tenantName, slug, domain, "smb", 10, 500, now, now,
		).Scan(&tenantID); err != nil {
			return 0, 0, fmt.Errorf("create tenant: %w", err)
		}
	} else {
		res, err := tx.Exec(
			fmt.Sprintf("INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at) VALUES (%s)", dial.Placeholders(8)),
			tenantName, slug, domain, "smb", 10, 500, now, now,
		)
		if err != nil {
			return 0, 0, fmt.Errorf("create tenant: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, 0, fmt.Errorf("tenant last insert id: %w", err)
		}
		tenantID = uint(id)
	}

	var userID uint
	now = time.Now().UTC()
	if dial.IsPostgres() {
		if err := tx.QueryRow(
			fmt.Sprintf("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (%s) RETURNING id", dial.Placeholders(8)),
			now, now, email, pwHash, string(auth.RoleUser), tenantID, true, true,
		).Scan(&userID); err != nil {
			return 0, 0, fmt.Errorf("create user: %w", err)
		}
	} else {
		res, err := tx.Exec(
			fmt.Sprintf("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (%s)", dial.Placeholders(8)),
			now, now, email, pwHash, string(auth.RoleUser), tenantID, true, true,
		)
		if err != nil {
			return 0, 0, fmt.Errorf("create user: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, 0, fmt.Errorf("user last insert id: %w", err)
		}
		userID = uint(id)
	}

	if h.billingSvc != nil {
		if _, err := h.billingSvc.CreateSubscriptionTx(tx, dial, tenantID, billing.PlanFree, billing.IntervalMonthly, 0); err != nil {
			return 0, 0, fmt.Errorf("create subscription: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}

	return userID, tenantID, nil
}
