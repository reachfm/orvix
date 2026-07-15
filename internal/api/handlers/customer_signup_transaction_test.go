package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

func TestSignupTransactionSQLiteMaxOpenConnsOneNoDeadlock(t *testing.T) {
	env := newSignupTxEnvSQLite(t)
	assertSignupCompletesAndCommitsAtomically(t, env)
}

func TestSignupTransactionSQLiteSubscriptionFailureRollsBackTenantAndUser(t *testing.T) {
	env := newSignupTxEnvSQLite(t)
	assertSignupSubscriptionFailureRollsBack(t, env)
}

func TestSignupTransactionPostgresMaxOpenConnsOneNoDeadlock(t *testing.T) {
	env := newSignupTxEnvPostgres(t)
	assertSignupCompletesAndCommitsAtomically(t, env)
}

func TestSignupTransactionPostgresSubscriptionFailureRollsBackTenantAndUser(t *testing.T) {
	env := newSignupTxEnvPostgres(t)
	assertSignupSubscriptionFailureRollsBack(t, env)
}

type signupTxEnv struct {
	router *api.Router
	sqlDB  *sql.DB
}

func newSignupTxEnvSQLite(t *testing.T) *signupTxEnv {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = t.TempDir() + "/signup.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	return newSignupTxEnv(t, cfg)
}

func newSignupTxEnvPostgres(t *testing.T) *signupTxEnv {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" {
		t.Skip("PGHOST not set; PostgreSQL signup transaction variant runs in PostgreSQL DML workflow")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("PGUSER")
	password := os.Getenv("PGPASSWORD")
	dbname := os.Getenv("PGDATABASE")
	baseDSN := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbname)
	setupDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres setup db: %v", err)
	}
	defer setupDB.Close()
	schema := fmt.Sprintf("signup_tx_%d", time.Now().UnixNano())
	if _, err := setupDB.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { setupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })

	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = baseDSN + " search_path=" + schema
	cfg.Database.MaxOpen = 1
	cfg.Database.MaxIdle = 1
	return newSignupTxEnv(t, cfg)
}

func newSignupTxEnv(t *testing.T, cfg *config.Config) *signupTxEnv {
	t.Helper()
	logger := zap.NewNop()
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	if cfg.Database.Driver == "postgres" {
		if err := models.MigrateAllPostgres(db); err != nil {
			t.Fatalf("migrate postgres: %v", err)
		}
	} else if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate raw: %v", err)
	}
	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	router := api.NewRouter(cfg, authenticator, logger, db, modules.NewRegistry(logger), license.NewFeatureFlags(logger), nil)
	t.Cleanup(func() { _ = router.App().Shutdown() })
	return &signupTxEnv{router: router, sqlDB: sqlDB}
}

func assertSignupCompletesAndCommitsAtomically(t *testing.T, env *signupTxEnv) {
	t.Helper()
	status, body := signupWithTimeout(t, env, "signup-ok@example.com")
	if status != http.StatusCreated {
		t.Fatalf("signup status=%d body=%s", status, body)
	}
	assertSignupCounts(t, env.sqlDB, "signup-ok@example.com", "example.com", 1, 1, 1)
}

func assertSignupSubscriptionFailureRollsBack(t *testing.T, env *signupTxEnv) {
	t.Helper()
	if _, err := env.sqlDB.Exec("DELETE FROM plans WHERE id = 'free'"); err != nil {
		t.Fatalf("delete free plan: %v", err)
	}
	status, _ := signupWithTimeout(t, env, "signup-fail@example.com")
	if status != http.StatusInternalServerError {
		t.Fatalf("signup with missing plan status=%d, want 500", status)
	}
	assertSignupCounts(t, env.sqlDB, "signup-fail@example.com", "example.com", 0, 0, 0)
}

func signupWithTimeout(t *testing.T, env *signupTxEnv, email string) (int, string) {
	t.Helper()
	type result struct {
		status int
		body   string
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		payload, _ := json.Marshal(map[string]string{
			"email":    email,
			"password": "StrongPass123",
			"name":     "Example Inc",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signup", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp, err := env.router.App().Test(req, fiber.TestConfig{Timeout: 0})
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		ch <- result{status: resp.StatusCode, body: string(raw)}
	}()
	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("signup request: %v", got.err)
		}
		return got.status, got.body
	case <-time.After(3 * time.Second):
		t.Fatalf("signup timed out; possible transaction deadlock")
		return 0, ""
	}
}

func assertSignupCounts(t *testing.T, db *sql.DB, email, domain string, wantTenants, wantUsers, wantSubs int64) {
	t.Helper()
	assertCountWhere(t, db, "tenants", "domain", domain, wantTenants)
	assertCountWhere(t, db, "users", "email", strings.ToLower(email), wantUsers)
	assertScalarInt64Handlers(t, db, "SELECT COUNT(*) FROM subscriptions", wantSubs)
}

func assertCountWhere(t *testing.T, db *sql.DB, table, column, value string, want int64) {
	t.Helper()
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
	assertScalarInt64Handlers(t, db, query, want, value)
}

func assertScalarInt64Handlers(t *testing.T, db *sql.DB, query string, want int64, args ...interface{}) {
	t.Helper()
	dial, err := dbdialect.Detect(db)
	if err != nil {
		dial = dbdialect.FromDriver("sqlite")
	}
	var got int64
	if err := db.QueryRow(dial.Rewrite(query), args...).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("query %q got %d want %d", query, got, want)
	}
}
