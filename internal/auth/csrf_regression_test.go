package auth

// This file is a deterministic regression suite for the CSRF gap
// reported (but never root-caused) during the self-update work: a
// skipped test claimed that internal/auth/csrf.go's Middleware()
// "does not reliably reject a forged/non-issued token... as long as
// ANY real CSRF record exists in the table."
//
// Root cause (proven, not speculated — see comments on
// internal/config/sqlite_dialect.go and the csrf_records table
// additions in internal/models/models.go / postgres_migrations.go):
//
//  1. internal/config/sqlite_dialect.go's custom GORM Dialector for
//     modernc.org/sqlite never called callbacks.RegisterDefaultCallbacks,
//     so EVERY GORM db.Create/db.First/db.Where(...).Find call on the
//     SQLite path executed ZERO SQL and returned a nil error. Any
//     CSRF cookie/header pair that merely matched each other sailed
//     through Middleware()'s DB lookup, because the lookup never ran
//     and its `err` stayed nil — never because "a row existed
//     somewhere". This is a GORM-wiring bug, not a WHERE-clause bug:
//     internal/auth/csrf.go's query (`token_hash = ? AND expires_at >
//     ?`, keyed on the SHA-256 of the submitted cookie token, via
//     First()+error-check) was always correct.
//  2. Once that was fixed, a second, independent bug surfaced: the
//     csrf_records table was never created by ANY migration path
//     (neither MigrateAllRaw for SQLite nor MigrateAllPostgres for
//     PostgreSQL). On SQLite this was masked by bug #1 (the failing
//     "no such table" error was itself swallowed by the same no-op).
//     On PostgreSQL (whose official driver wires callbacks correctly)
//     this would have made CSRF token issuance fail outright.
//
// Both are fixed in this change. This file proves the fix with a real
// middleware + real DB driver paths (SQLite always; PostgreSQL when
// PGHOST is reachable, following the same skip convention as
// internal/selfupdate/store_test.go's TestPostgres_RowLockingSerializesActiveJobCheck).

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ── harness ─────────────────────────────────────────────────────────

// csrfHarness wires a real fiber app with exactly one route mounted
// behind the real CSRFManager.Middleware(), against a real *gorm.DB.
type csrfHarness struct {
	db  *gorm.DB
	cm  *CSRFManager
	app *fiber.App
}

func newSQLiteCSRFHarness(t *testing.T) *csrfHarness {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate",
	}
	db, err := config.NewDatabase(&cfg, logger)
	if err != nil {
		t.Fatalf("sqlite db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return buildCSRFHarness(t, db, logger)
}

func pgAvailable() bool {
	return os.Getenv("PGHOST") != ""
}

func newPostgresCSRFHarness(t *testing.T) *csrfHarness {
	t.Helper()
	logger := zap.NewNop()
	host := os.Getenv("PGHOST")
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("PGUSER")
	password := os.Getenv("PGPASSWORD")
	dbname := os.Getenv("PGDATABASE")

	// Use a dedicated schema per test run so this is safe to run
	// against a shared Postgres instance, same convention as
	// internal/selfupdate/store_test.go.
	schemaName := fmt.Sprintf("orvix_test_csrf_%d", time.Now().UnixNano())

	adminDSN := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	adminDB, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("pgx open: %v", err)
	}
	t.Cleanup(func() { adminDB.Close() })
	if _, err := adminDB.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		adminDB.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
	})

	dsn := adminDSN + fmt.Sprintf(" search_path=%s", schemaName)
	cfg := config.DatabaseConfig{Driver: "postgres", DSN: dsn}
	db, err := config.NewDatabase(&cfg, logger)
	if err != nil {
		t.Fatalf("postgres gorm db: %v", err)
	}
	if err := models.MigrateAllPostgres(db); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	return buildCSRFHarness(t, db, logger)
}

func buildCSRFHarness(t *testing.T, db *gorm.DB, logger *zap.Logger) *csrfHarness {
	t.Helper()
	cm := NewCSRFManager(db, logger, false)

	app := fiber.New()
	app.Post("/protected", cm.Middleware(), func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	return &csrfHarness{db: db, cm: cm, app: app}
}

// send issues a POST to /protected with the given cookie/header token
// values (either may be omitted by passing "" and setHeader/setCookie
// false) and returns the response status code.
func (h *csrfHarness) send(t *testing.T, setCookie bool, cookieVal string, setHeader bool, headerVal string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/protected", nil)
	if setCookie {
		req.Header.Set("Cookie", "csrf_token="+cookieVal)
	}
	if setHeader {
		req.Header.Set("X-CSRF-Token", headerVal)
	}
	resp, err := h.app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp.StatusCode
}

func hashOf(token string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
}

// ── SQLite: core matrix ─────────────────────────────────────────────

func TestCSRF_SQLite_Matrix(t *testing.T) {
	h := newSQLiteCSRFHarness(t)
	runCSRFMatrix(t, h)
}

func TestCSRF_Postgres_Matrix(t *testing.T) {
	if !pgAvailable() {
		t.Skip("PGHOST not set; skipping PostgreSQL CSRF regression matrix (no reachable Postgres instance in this environment)")
	}
	h := newPostgresCSRFHarness(t)
	runCSRFMatrix(t, h)
}

// runCSRFMatrix is dialect-agnostic and runs the full checklist
// against whatever harness (SQLite or Postgres) it's given.
func runCSRFMatrix(t *testing.T, h *csrfHarness) {
	t.Run("legit_token_created_and_stored", func(t *testing.T) {
		token, hash := issueToken(t, h, 1)
		var rec CSRFRecord
		if err := h.db.Where("token_hash = ?", hash).First(&rec).Error; err != nil {
			t.Fatalf("expected stored record for issued token, got err: %v", err)
		}
		if rec.UserID != 1 {
			t.Fatalf("expected UserID=1, got %d", rec.UserID)
		}
		_ = token
	})

	t.Run("valid_token_accepted", func(t *testing.T) {
		token, _ := issueToken(t, h, 2)
		status := h.send(t, true, token, true, token)
		if status != fiber.StatusOK {
			t.Fatalf("expected 200 for valid token, got %d", status)
		}
	})

	t.Run("forged_token_rejected_even_though_a_valid_row_exists", func(t *testing.T) {
		// This is exactly the scenario the buggy skipped test was
		// probing: issue a real, valid token (so the table is
		// non-empty and contains at least one legitimate row), then
		// submit a completely different, never-issued forged value.
		// It must be rejected purely because ITS OWN hash has no
		// matching row — not accepted just because *some* row exists.
		_, _ = issueToken(t, h, 3)
		forged := "forged-token-that-was-never-issued-by-the-server"
		status := h.send(t, true, forged, true, forged)
		if status == fiber.StatusOK {
			t.Fatalf("expected forged token to be rejected even though a valid row exists in csrf_records, got 200")
		}
	})

	t.Run("missing_cookie_rejected", func(t *testing.T) {
		token, _ := issueToken(t, h, 4)
		status := h.send(t, false, "", true, token)
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection with missing cookie, got 200")
		}
	})

	t.Run("missing_header_rejected", func(t *testing.T) {
		token, _ := issueToken(t, h, 5)
		status := h.send(t, true, token, false, "")
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection with missing header, got 200")
		}
	})

	t.Run("cookie_and_header_present_but_mismatched_rejected", func(t *testing.T) {
		token1, _ := issueToken(t, h, 6)
		token2, _ := issueToken(t, h, 7)
		status := h.send(t, true, token1, true, token2)
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection when cookie/header tokens mismatch, got 200")
		}
	})

	t.Run("expired_token_rejected", func(t *testing.T) {
		// CSRFRecord has an ExpiresAt column (24h from issuance in
		// GenerateToken). There is no separate revocation flag; the
		// only staleness concept in the schema is ExpiresAt, so that
		// is what we test here — inserting a record whose ExpiresAt
		// is already in the past and confirming Middleware rejects a
		// cookie/header pair matching its token hash.
		token := "expired-token-value"
		hash := hashOf(token)
		rec := CSRFRecord{TokenHash: hash, UserID: 8, ExpiresAt: time.Now().Add(-time.Hour)}
		if err := h.db.Create(&rec).Error; err != nil {
			t.Fatalf("create expired record: %v", err)
		}
		status := h.send(t, true, token, true, token)
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection for expired token, got 200")
		}
	})

	t.Run("revoked_deleted_token_rejected", func(t *testing.T) {
		// InvalidateToken performs a hard delete of the matching row
		// (there is no separate "revoked" flag/tombstone in the
		// schema — deletion IS revocation). Confirm a deleted token
		// is rejected exactly like a never-issued one.
		token, _ := issueToken(t, h, 9)
		if err := h.cm.InvalidateToken(token); err != nil {
			t.Fatalf("invalidate: %v", err)
		}
		status := h.send(t, true, token, true, token)
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection for revoked/deleted token, got 200")
		}
	})

	t.Run("cross_user_token_still_accepted_because_middleware_is_session_agnostic", func(t *testing.T) {
		// IMPORTANT FINDING: CSRFRecord.UserID identifies who a token
		// was issued to, but Middleware() never reads or compares it
		// — it only checks token_hash + expiry. There is no session
		// binding, so a token issued to user A and later replayed
		// alongside a request authenticated as user B WILL pass CSRF
		// Middleware (auth/authorization for the request itself is a
		// completely separate concern handled by other middleware,
		// e.g. JWT/session auth + RBAC). This is documented here as
		// the actual behavior — not silently assumed to be
		// cross-user-safe. It's a defense-in-depth note, not the bug
		// this file was written to prove; double-submit CSRF cookies
		// are conventionally session-scoped by the browser's own
		// same-origin cookie jar, not by a server-side user check.
		tokenForUser10, _ := issueToken(t, h, 10)
		status := h.send(t, true, tokenForUser10, true, tokenForUser10)
		if status != fiber.StatusOK {
			t.Fatalf("expected acceptance (Middleware does not bind to UserID) got %d", status)
		}
	})

	t.Run("token_reuse_is_allowed_by_design_ttl_based_not_single_use", func(t *testing.T) {
		// GenerateToken/Middleware implement a TTL-based (24h) token,
		// not a single-use nonce: nothing in Middleware deletes or
		// marks a row consumed after a successful check. Using the
		// same valid token twice in a row must succeed both times —
		// verifying this is the actual intended policy per the code,
		// not assuming single-use.
		token, _ := issueToken(t, h, 11)
		first := h.send(t, true, token, true, token)
		second := h.send(t, true, token, true, token)
		if first != fiber.StatusOK || second != fiber.StatusOK {
			t.Fatalf("expected token reuse to succeed both times (TTL-based, not single-use), got %d then %d", first, second)
		}
	})

	t.Run("concurrent_validation_consistent", func(t *testing.T) {
		token, _ := issueToken(t, h, 12)
		forged := "concurrent-forged-token-never-issued"

		const n = 20
		var wg sync.WaitGroup
		validResults := make([]int, n)
		forgedResults := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				validResults[i] = h.send(t, true, token, true, token)
			}(i)
			go func(i int) {
				defer wg.Done()
				forgedResults[i] = h.send(t, true, forged, true, forged)
			}(i)
		}
		wg.Wait()

		for i, s := range validResults {
			if s != fiber.StatusOK {
				t.Fatalf("concurrent valid-token request %d: expected 200, got %d", i, s)
			}
		}
		for i, s := range forgedResults {
			if s == fiber.StatusOK {
				t.Fatalf("concurrent forged-token request %d: expected rejection, got 200", i)
			}
		}
	})

	t.Run("database_not_found_rejects_cleanly_no_panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Middleware panicked on not-found lookup: %v", r)
			}
		}()
		never := "definitely-not-in-the-table-xyz"
		status := h.send(t, true, never, true, never)
		if status == fiber.StatusOK {
			t.Fatalf("expected clean rejection for not-found token, got 200")
		}
	})

	t.Run("malformed_token_rejected", func(t *testing.T) {
		// A raw NUL byte (a classic injection-shaped payload) can't
		// even be transmitted as an HTTP header value — net/http's
		// own header writer rejects it before the request reaches
		// our server at all, which is a stronger guarantee than an
		// app-level check and is exercised separately below. Here we
		// cover header-transmissible malformed shapes: SQL-injection-
		// shaped text, non-hex/non-base64 punctuation, and a token
		// that's syntactically token-shaped but simply too short to
		// be a real 32-byte (43-char base64) token.
		cases := []string{
			"'; DROP TABLE csrf_records; --",
			"not-hex-!!!@@@###",
			strings.Repeat("z", 5),
		}
		for _, tok := range cases {
			status := h.send(t, true, tok, true, tok)
			if status == fiber.StatusOK {
				t.Fatalf("expected rejection for malformed token %q, got 200", tok)
			}
		}
	})

	t.Run("nul_byte_token_cannot_even_be_sent_as_a_header", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/protected", nil)
		req.Header.Set("Cookie", "csrf_token=valid")
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			req.Header.Set("X-CSRF-Token", "\x00\x01\x02binary")
			_, testErr := h.app.Test(req, fiber.TestConfig{Timeout: 0})
			return testErr
		}()
		// Either Go's net/http rejects the invalid header value
		// outright, or fasthttp's request parser errors reading it —
		// both are acceptable fail-closed outcomes; the property
		// under test is simply that a NUL-byte token can never reach
		// Middleware() as an accepted request.
		if err == nil {
			t.Fatalf("expected header construction or request parsing to fail for a NUL-byte token, but it succeeded")
		}
	})

	t.Run("empty_token_rejected", func(t *testing.T) {
		// Empty cookie/header values are indistinguishable from the
		// header/cookie being absent in Middleware's own checks
		// (c.Cookies/c.Get return "" either way), so this exercises
		// the "missing" branches, which is the correct fail-closed
		// behavior for an empty token.
		status := h.send(t, true, "", true, "")
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection for empty token, got 200")
		}
	})

	t.Run("oversized_token_rejected", func(t *testing.T) {
		// Real tokens are 43 base64 chars (32 random bytes). 1500
		// chars is ~35x that — large enough to prove an oversized
		// value is rejected on its merits (no matching hash, and if
		// GORM/SQLite ever silently truncated a column it would
		// still not collide with a real stored hash) while staying
		// safely under the test HTTP server's header read-buffer
		// limit, so this exercises Middleware, not the transport.
		huge := strings.Repeat("a", 1500)
		status := h.send(t, true, huge, true, huge)
		if status == fiber.StatusOK {
			t.Fatalf("expected rejection for oversized token, got 200")
		}
	})

	t.Run("real_db_error_fails_closed", func(t *testing.T) {
		// Simulate a real database error (not just "not found") by
		// closing the underlying connection so the next query errors
		// out at the driver level, then confirming Middleware still
		// rejects (fail-closed) rather than treating the error as
		// "no row to compare against, so allow". We use a throwaway
		// harness so we don't tear down shared state used by other
		// subtests.
		var cfg config.DatabaseConfig
		cfg.Driver = "sqlite"
		cfg.DSN = t.TempDir() + "/orvix_broken.db?_loc=auto&_busy_timeout=5000&_txlock=immediate"
		db, err := config.NewDatabase(&cfg, zap.NewNop())
		if err != nil {
			t.Fatalf("db: %v", err)
		}
		if err := models.MigrateAllRaw(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		hh := buildCSRFHarness(t, db, zap.NewNop())
		token, _ := issueToken(t, hh, 13)

		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("sql db: %v", err)
		}
		sqlDB.Close() // force every subsequent query to error

		status := hh.send(t, true, token, true, token)
		if status == fiber.StatusOK {
			t.Fatalf("expected fail-closed (non-200) when the DB connection is broken, got 200")
		}
	})
}

// issueToken drives the real /csrf-token-equivalent path
// (CSRFManager.GenerateToken) through a real fiber.Ctx so the cookie
// is set exactly the way production code sets it, and returns the
// plaintext token plus its SHA-256 hash.
func issueToken(t *testing.T, h *csrfHarness, userID uint) (string, string) {
	t.Helper()
	var token string
	var genErr error
	app := fiber.New()
	app.Get("/issue", func(c fiber.Ctx) error {
		token, genErr = h.cm.GenerateToken(c, userID)
		return c.SendStatus(fiber.StatusOK)
	})
	req := httptest.NewRequest("GET", "/issue", nil)
	if _, err := app.Test(req, fiber.TestConfig{Timeout: 0}); err != nil {
		t.Fatalf("issue request: %v", err)
	}
	if genErr != nil {
		t.Fatalf("GenerateToken: %v", genErr)
	}
	if token == "" {
		t.Fatalf("GenerateToken returned empty token")
	}
	return token, hashOf(token)
}
