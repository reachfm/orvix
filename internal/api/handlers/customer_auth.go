package handlers

import (
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
	"github.com/orvix/orvix/internal/dbdialect"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// passwordResetTokensDDL returns dialect-appropriate DDL for the
// password_reset_tokens table.
func passwordResetTokensDDL(d *dbdialect.Info) string {
	auto := d.AutoIncrement()
	ts := d.TimestampType()
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS password_reset_tokens (
		id %s,
		user_id INTEGER NOT NULL,
		token_hash TEXT NOT NULL,
		expires_at %s NOT NULL,
		used_at %s,
		created_at %s NOT NULL DEFAULT %s
	)`, auto, ts, ts, ts, d.NowExpr())
}

func (h *Handler) ensurePasswordResetTokensTable(sqlDB *sql.DB, dial *dbdialect.Info) error {
	_, err := sqlDB.Exec(passwordResetTokensDDL(dial))
	return err
}

type SignupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func passwordStrength(pw string) error {
	if len(pw) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	var hasUpper, hasLower, hasDigit bool
	for _, ch := range pw {
		switch {
		case ch >= 'A' && ch <= 'Z':
			hasUpper = true
		case ch >= 'a' && ch <= 'z':
			hasLower = true
		case ch >= '0' && ch <= '9':
			hasDigit = true
		}
	}
	if !hasUpper {
		return fmt.Errorf("password must contain an uppercase letter")
	}
	if !hasLower {
		return fmt.Errorf("password must contain a lowercase letter")
	}
	if !hasDigit {
		return fmt.Errorf("password must contain a digit")
	}
	return nil
}

// Signup creates a new customer portal account.
// POST /auth/signup
func (h *Handler) Signup(c fiber.Ctx) error {
	var req SignupRequest
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
		h.logger.Error("signup: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()

	var existing int64
	if err := sqlDB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM users WHERE email = %s", dial.Placeholder(1)), email).Scan(&existing); err != nil {
		h.logger.Error("signup: check duplicate", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if existing > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "an account with this email already exists"})
	}

	domain := loginDomain(email)
	tenantName := strings.TrimSpace(req.Name)
	if tenantName == "" {
		tenantName = domain
	}

	var tenantID uint
	slug := strings.NewReplacer(".", "-", "@", "-", " ", "-").Replace(strings.ToLower(tenantName))
	now := time.Now().UTC()
	if dial.IsPostgres() {
		err = sqlDB.QueryRow(
			fmt.Sprintf("INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at) VALUES (%s) RETURNING id", dial.Placeholders(8)),
			tenantName, slug, domain, "smb", 10, 500, now, now,
		).Scan(&tenantID)
		if err != nil {
			h.logger.Error("signup: create tenant", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
	} else {
		res, err := sqlDB.Exec(
			fmt.Sprintf("INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, created_at, updated_at) VALUES (%s)", dial.Placeholders(8)),
			tenantName, slug, domain, "smb", 10, 500, now, now,
		)
		if err != nil {
			h.logger.Error("signup: create tenant", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
		id, err := res.LastInsertId()
		if err != nil {
			h.logger.Error("signup: tenant last insert id", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
		tenantID = uint(id)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("signup: hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	var userID uint
	now = time.Now().UTC()
	if dial.IsPostgres() {
		err = sqlDB.QueryRow(
			fmt.Sprintf("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (%s) RETURNING id", dial.Placeholders(8)),
			now, now, email, string(hash), string(auth.RoleUser), tenantID, true, true,
		).Scan(&userID)
		if err != nil {
			h.logger.Error("signup: insert user", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
	} else {
		res, err := sqlDB.Exec(
			fmt.Sprintf("INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (%s)", dial.Placeholders(8)),
			now, now, email, string(hash), string(auth.RoleUser), tenantID, true, true,
		)
		if err != nil {
			h.logger.Error("signup: insert user", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
		id, err := res.LastInsertId()
		if err != nil {
			h.logger.Error("signup: user last insert id", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create account"})
		}
		userID = uint(id)
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
			h.logger.Error("signup: write audit", zap.Error(err))
		}
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"user": fiber.Map{
			"id":    userID,
			"email": email,
			"role":  string(auth.RoleUser),
		},
	})
}

// ForgotPassword initiates a password reset flow.
// Always returns 200 OK for account enumeration resistance.
// POST /auth/forgot-password
func (h *Handler) ForgotPassword(c fiber.Ctx) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("forgot password: failed to get underlying DB", zap.Error(err))
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}
	dial := h.sqlDialect()

	var userID uint
	if err := sqlDB.QueryRow(fmt.Sprintf("SELECT id FROM users WHERE email = %s", dial.Placeholder(1)), email).Scan(&userID); err != nil {
		if err != sql.ErrNoRows {
			h.logger.Error("forgot password: query user", zap.Error(err))
		}
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}

	if err := h.ensurePasswordResetTokensTable(sqlDB, dial); err != nil {
		h.logger.Error("forgot password: ensure table", zap.Error(err))
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		h.logger.Error("forgot password: generate token", zap.Error(err))
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}
	token := hex.EncodeToString(tokenBytes)
	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
	expiresAt := time.Now().UTC().Add(1 * time.Hour)

	if _, err := sqlDB.Exec(
		fmt.Sprintf("INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, created_at) VALUES (%s)", dial.Placeholders(4)),
		userID, tokenHash, expiresAt, time.Now().UTC(),
	); err != nil {
		h.logger.Error("forgot password: insert token", zap.Error(err))
		return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
	}

	h.logger.Info("password reset token generated",
		zap.String("email", email),
		zap.Uint("user_id", userID),
		zap.Time("expires_at", expiresAt))

	return c.JSON(fiber.Map{"message": "if the email exists, a reset link has been sent"})
}

// ResetPasswordRequest is the JSON body for password reset.
type ResetPasswordRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// ResetPassword completes a password reset flow.
// POST /auth/reset-password
func (h *Handler) ResetPassword(c fiber.Ctx) error {
	var req ResetPasswordRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "token is required"})
	}
	if err := passwordStrength(req.NewPassword); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("reset password: failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	dial := h.sqlDialect()

	tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Token)))

	var (
		resetID   int64
		userID    uint
		expiresAt time.Time
		dbHash    string
	)
	row := sqlDB.QueryRow(
		fmt.Sprintf("SELECT id, user_id, token_hash, expires_at FROM password_reset_tokens WHERE token_hash = %s AND used_at IS NULL", dial.Placeholder(1)),
		tokenHash,
	)
	if err := row.Scan(&resetID, &userID, &dbHash, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired token"})
		}
		h.logger.Error("reset password: query token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	if subtle.ConstantTimeCompare([]byte(dbHash), []byte(tokenHash)) != 1 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired token"})
	}

	if time.Now().UTC().After(expiresAt) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or expired token"})
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Error("reset password: hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	now := time.Now().UTC()
	if _, err := sqlDB.Exec(
		fmt.Sprintf("UPDATE users SET password_hash = %s, updated_at = %s WHERE id = %s", dial.Placeholder(1), dial.Placeholder(2), dial.Placeholder(3)),
		string(newHash), now, userID,
	); err != nil {
		h.logger.Error("reset password: update user", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	if _, err := sqlDB.Exec(
		fmt.Sprintf("UPDATE password_reset_tokens SET used_at = %s WHERE id = %s", dial.Placeholder(1), dial.Placeholder(2)),
		now, resetID,
	); err != nil {
		h.logger.Error("reset password: mark token used", zap.Error(err))
	}

	if err := h.auth.InvalidateAllSessions(userID); err != nil {
		h.logger.Error("reset password: invalidate sessions", zap.Error(err))
	}

	if h.auditStore != nil {
		if err := h.auditStore.Record(c.Context(), &audit.Entry{
			Actor:     fmt.Sprintf("user:%d", userID),
			Action:    "customer.password_reset",
			Target:    fmt.Sprintf("user:%d", userID),
			Result:    "success",
			IP:        c.IP(),
			UserAgent: c.Get("User-Agent"),
		}); err != nil {
			h.logger.Error("reset password: write audit", zap.Error(err))
		}
	}

	return c.JSON(fiber.Map{"status": "password reset successfully"})
}
