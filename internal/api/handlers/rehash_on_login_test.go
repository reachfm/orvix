package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type rehashTestEnv struct {
	router        *api.Router
	authenticator *auth.Authenticator
	sqlDB         *sql.DB
}

func newRouterWithBcryptUser(t *testing.T, email, password string) *rehashTestEnv {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Server.AdminUIDir = "../../release/admin"
	cfg.Server.WebmailUIDir = "../../release/webmail"
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}

	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	if _, err := sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, ?, ?, 'admin', 1, 1, 1)`,
		now, now, email, string(bcryptHash)); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, status, plan, created_at, updated_at)
		 VALUES ('test.local', 1, 'active', 'enterprise', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (1, 1, 'admin', ?, 'Admin', 'hash', 'argon2id', 'active', 1024, 1, ?, ?)`,
		email, now, now); err != nil {
		t.Fatalf("insert mailbox: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() {
		router.App().Shutdown()
		sqlDB.Close()
	})
	return &rehashTestEnv{router: router, authenticator: authenticator, sqlDB: sqlDB}
}

func (env *rehashTestEnv) getStoredPasswordHash(email string) string {
	var hash string
	if err := env.sqlDB.QueryRow("SELECT password_hash FROM users WHERE email = ?", email).Scan(&hash); err != nil {
		panic("query stored hash: " + err.Error())
	}
	return hash
}

func (env *rehashTestEnv) updatePasswordHashDirectly(email, newHash string) {
	if _, err := env.sqlDB.Exec("UPDATE users SET password_hash = ? WHERE email = ?", newHash, email); err != nil {
		panic("update hash directly: " + err.Error())
	}
}

func (env *rehashTestEnv) doLogin(email, password string) int {
	body, _ := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.router.App().Test(req)
	if err != nil {
		panic("login request: " + err.Error())
	}
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestRehashOnLogin_BcryptLoginSucceeds(t *testing.T) {
	const (
		email    = "admin@test.local"
		password = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, email, password)
	status := env.doLogin(email, password)
	if status != 200 {
		t.Fatalf("bcrypt login: want 200, got %d", status)
	}
}

func TestRehashOnLogin_BcryptHashBecomesArgon2id(t *testing.T) {
	const (
		email    = "admin@test.local"
		password = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, email, password)

	hashBefore := env.getStoredPasswordHash(email)
	if !strings.HasPrefix(hashBefore, "$2a$") && !strings.HasPrefix(hashBefore, "$2b$") && !strings.HasPrefix(hashBefore, "$2y$") {
		t.Fatalf("expected bcrypt hash, got prefix: %.6s...", hashBefore)
	}

	status := env.doLogin(email, password)
	if status != 200 {
		t.Fatalf("login: want 200, got %d", status)
	}

	hashAfter := env.getStoredPasswordHash(email)
	if !strings.HasPrefix(hashAfter, "$argon2id$") {
		t.Fatalf("expected Argon2id hash after rehash-on-login, got prefix: %.12s...", hashAfter)
	}
	if hashAfter == hashBefore {
		t.Fatal("hash should have changed after rehash-on-login")
	}
}

func TestRehashOnLogin_WrongPasswordDoesNotUpdateDB(t *testing.T) {
	const (
		email    = "admin@test.local"
		password = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, email, password)

	hashBefore := env.getStoredPasswordHash(email)
	status := env.doLogin(email, "WrongPassword!")
	if status != 401 {
		t.Fatalf("wrong password login: want 401, got %d", status)
	}

	hashAfter := env.getStoredPasswordHash(email)
	if hashAfter != hashBefore {
		t.Fatal("password_hash should not change on failed login")
	}
}

func TestRehashOnLogin_ConcurrentPasswordChangeNotOverwritten(t *testing.T) {
	const (
		email    = "admin@test.local"
		password = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, email, password)

	hashBefore := env.getStoredPasswordHash(email)

	otherHash, err := env.authenticator.HashPassword("OtherPassword!")
	if err != nil {
		t.Fatalf("other hash: %v", err)
	}
	env.updatePasswordHashDirectly(email, otherHash)

	status := env.doLogin(email, password)
	if status != 401 {
		t.Fatalf("login after concurrent change: want 401 (no match), got %d", status)
	}

	hashAfter := env.getStoredPasswordHash(email)
	if hashAfter == hashBefore {
		t.Fatal("password_hash should remain the concurrent change, not the original bcrypt")
	}
	if !strings.HasPrefix(hashAfter, "$argon2id$") {
		t.Fatalf("concurrent hash should still be Argon2id, got: %.12s...", hashAfter)
	}
}

func TestRehashOnLogin_Argon2idLoginDoesNotReUpdate(t *testing.T) {
	const (
		email    = "admin@test.local"
		password = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, email, password)

	status := env.doLogin(email, password)
	if status != 200 {
		t.Fatalf("first login: want 200, got %d", status)
	}
	hashAfterFirst := env.getStoredPasswordHash(email)
	if !strings.HasPrefix(hashAfterFirst, "$argon2id$") {
		t.Fatalf("expected Argon2id after first login, got: %.12s...", hashAfterFirst)
	}

	status = env.doLogin(email, password)
	if status != 200 {
		t.Fatalf("second login: want 200, got %d", status)
	}
	hashAfterSecond := env.getStoredPasswordHash(email)
	if hashAfterSecond != hashAfterFirst {
		t.Fatal("Argon2id hash should not change on subsequent logins")
	}
}

func TestRehashOnLogin_OnlyAuthenticatedUserUpdated(t *testing.T) {
	const (
		emailA = "admin@test.local"
		emailB = "user@test.local"
		pass   = "TestPassword123!"
	)
	env := newRouterWithBcryptUser(t, emailA, pass)

	now := time.Now().UTC().Format("2006-01-02 15:04:05")
	bcryptHash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	if _, err := env.sqlDB.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, ?, ?, 'user', 1, 1, 1)`,
		now, now, emailB, string(bcryptHash)); err != nil {
		t.Fatalf("insert user B: %v", err)
	}

	hashB_Before := env.getStoredPasswordHash(emailB)

	status := env.doLogin(emailA, pass)
	if status != 200 {
		t.Fatalf("login A: want 200, got %d", status)
	}

	hashA_After := env.getStoredPasswordHash(emailA)
	if !strings.HasPrefix(hashA_After, "$argon2id$") {
		t.Fatalf("user A hash should be Argon2id, got: %.12s...", hashA_After)
	}

	hashB_After := env.getStoredPasswordHash(emailB)
	if hashB_After != hashB_Before {
		t.Fatal("user B password_hash should not have changed")
	}
}
