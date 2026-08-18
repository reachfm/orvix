package handlers

// WebmailSend staged-acceptance tests on real PostgreSQL (S-2,
// PostgreSQL leg).
//
// The same handler code path (multipart parse -> staging -> acceptance
// transaction -> publish -> commit) is exercised against a real
// PostgreSQL 16 database in an isolated schema. Skipped unless PGHOST
// is set and ORVIX_RUN_POSTGRES_DML_TEST=1 (the postgres-dml CI
// convention). These tests FAIL (never silently skip) when the
// PostgreSQL environment is not wired — a skipped acceptance test
// proves nothing.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// buildSendEnforcementHarnessPostgres wires the same handler shape as
// buildSendEnforcementHarness but on real PostgreSQL inside an
// isolated schema.
func buildSendEnforcementHarnessPostgres(t *testing.T) (*Handler, *sql.DB, string) {
	t.Helper()
	if os.Getenv("PGHOST") == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres webmail staging: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	host := os.Getenv("PGHOST")
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
	schema := fmt.Sprintf("webmail_staging_%d", time.Now().UnixNano())
	if _, err := setupDB.Exec("CREATE SCHEMA " + schema); err != nil {
		setupDB.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = setupDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
		setupDB.Close()
	})

	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = baseDSN + " search_path=" + schema
	cfg.Database.MaxOpen = 4
	cfg.Database.MaxIdle = 2
	cfg.CoreMail.MailStorePath = filepath.Join(t.TempDir(), "mailstore")

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	if err := models.MigrateAllPostgres(db); err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}

	mailstoreDir := cfg.CoreMail.MailStorePath
	if err := os.MkdirAll(mailstoreDir, 0o750); err != nil {
		t.Fatalf("mkdir mailstore: %v", err)
	}
	mailStore, err := storage.NewMailStore(sqlDB, mailstoreDir)
	if err != nil {
		t.Fatalf("new mailstore: %v", err)
	}
	qe := queue.NewQueueEngine(sqlDB)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	reg := modules.NewRegistry(logger)
	ff := license.NewFeatureFlags(logger)

	h := NewHandler(db, authenticator, nil, logger, cfg, reg, ff, nil)
	h.SetMailStore(mailStore)
	h.SetQueueEngine(qe)
	h.SetWebmailService(nil)

	billingSvc, _, quotaSvc, _, _, enforcer, err := billing.Initialize(sqlDB)
	if err != nil {
		t.Fatalf("billing.Initialize: %v", err)
	}
	_ = billingSvc
	_ = quotaSvc
	h.SetSendEnforcer(enforcer)

	provisionSendAdminPostgres(t, sqlDB)

	var mailboxID uint
	if err := sqlDB.QueryRow("SELECT id FROM coremail_mailboxes WHERE email='admin@orvix.email'").Scan(&mailboxID); err != nil {
		t.Fatal(err)
	}
	if err := mailStore.Folders.EnsureSystemFolders(t.Context(), mailboxID, nil); err != nil {
		t.Fatal(err)
	}
	return h, sqlDB, schema
}

// provisionSendAdminPostgres is the PostgreSQL dialect variant of
// provisionSendAdmin (no INSERT OR IGNORE, no raw ? placeholders).
func provisionSendAdminPostgres(t *testing.T, sqlDB *sql.DB) {
	t.Helper()
	dial := dbdialect.FromDriver("postgres")
	now := time.Now().UTC()
	exec := func(q string, args ...interface{}) {
		t.Helper()
		if _, err := sqlDB.Exec(dial.Rewrite(q), args...); err != nil {
			t.Fatalf("provision: %v\nSQL: %s", err, q)
		}
	}
	exec(`INSERT INTO tenants (id, created_at, updated_at, name, slug, domain, plan, active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		1, now, now, "orvix", "orvix", "orvix.email", "enterprise", true)
	exec(`INSERT INTO subscriptions (tenant_id, plan_id, status, billing_interval,
		current_period_start, current_period_end, send_limit_day, storage_mb, created_at, updated_at)
		VALUES (1, 'free', 'active', 'monthly', ?, ?, 500, 1024, ?, ?)
		ON CONFLICT DO NOTHING`, now, now.AddDate(0, 1, 0), now, now)
	exec(`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, "admin@orvix.email", "x-bcrypt-hash-placeholder", "tenant_admin", 1, true, true)
	exec(`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"orvix.email", 1, "active", "enterprise", 0, 0, 0, now, now)
	exec(`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, "admin", "admin@orvix.email", "Admin", "x-bcrypt-hash-placeholder", "bcrypt", "active", 1024, true, now, now)
}

// TestWebmailSendStaging_Postgres_SuccessCommitsAndPublishes proves
// the full staged acceptance on PostgreSQL: 201, exactly one message
// row, two attachment rows, one queue row, and all files present at
// their final paths with zero staging leftovers.
func TestWebmailSendStaging_Postgres_SuccessCommitsAndPublishes(t *testing.T) {
	h, sqlDB, _ := buildSendEnforcementHarnessPostgres(t)

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("pg alpha"), []byte("pg beta")})
	if status != fiber.StatusCreated {
		t.Fatalf("expected 201, got %d: %v", status, resp)
	}
	id, _ := resp["id"].(float64)
	if id == 0 {
		t.Fatalf("missing message id: %v", resp)
	}

	var msgCount, attCount, queueCount int64
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_messages`).Scan(&msgCount); err != nil {
		t.Fatalf("message count: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_attachments WHERE message_id = $1`, uint(id)).Scan(&attCount); err != nil {
		t.Fatalf("attachment count: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL`).Scan(&queueCount); err != nil {
		t.Fatalf("queue count: %v", err)
	}
	if msgCount != 1 || attCount != 2 || queueCount != 1 {
		t.Fatalf("rows: messages=%d attachments=%d queue=%d, want 1/2/1", msgCount, attCount, queueCount)
	}

	var rfc822Path string
	if err := sqlDB.QueryRow(`SELECT rfc822_path FROM coremail_messages WHERE id = $1`, uint(id)).Scan(&rfc822Path); err != nil {
		t.Fatalf("rfc822 path: %v", err)
	}
	if _, err := os.Stat(rfc822Path); err != nil {
		t.Fatalf("published rfc822 missing: %v", err)
	}
	rows, err := sqlDB.Query(`SELECT storage_path FROM coremail_attachments WHERE message_id = $1`, uint(id))
	if err != nil {
		t.Fatalf("attachment paths: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan path: %v", err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("published attachment missing at %s: %v", p, err)
		}
	}

	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers on postgres = %d, want 0", st.stagingLeft)
	}
}

// TestWebmailSendStaging_Postgres_CommitFailureCleansAll proves the
// commit-failure path on PostgreSQL: the tx is poisoned before
// commit, the send returns 503, and zero rows / files / staging
// leftovers survive.
func TestWebmailSendStaging_Postgres_CommitFailureCleansAll(t *testing.T) {
	h, sqlDB, _ := buildSendEnforcementHarnessPostgres(t)

	h.webmailSendHooks = &webmailSendTestHooks{
		BeforeCommit: func(tx *sql.Tx) {
			_ = tx.Rollback() // poison: the following Commit fails
		},
	}

	status, resp := multipartSendToHandler(t, h, [][]byte{[]byte("pg payload")})
	if status != fiber.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %v", status, resp)
	}
	assertZeroDurableSideEffects(t, sqlDB)

	st := inspectStaging(t, h.mailStore)
	if st.stagingLeft != 0 {
		t.Fatalf("staging leftovers after postgres commit failure = %d, want 0", st.stagingLeft)
	}
	if len(st.publishedFiles) != 0 {
		t.Fatalf("published files after postgres commit failure = %d, want 0", len(st.publishedFiles))
	}
}
