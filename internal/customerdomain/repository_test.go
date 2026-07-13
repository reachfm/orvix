package customerdomain

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/dnsops"
	_ "modernc.org/sqlite"
)

// ── SQLite repository tests ──────────────────────────────────

func sqliteVerifRepo(t *testing.T) (*VerificationRepo, *sql.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "vrep.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	repo := NewVerificationRepo(db)
	if err := repo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	return repo, db
}

func TestSQLiteEnsureTable_CreatesTables(t *testing.T) {
	_, db := sqliteVerifRepo(t)
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='customer_domain_verifications'").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("customer_domain_verifications table missing, count=%d", count)
	}
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='customer_domain_verification_claims'").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("customer_domain_verification_claims table missing, count=%d", count)
	}
}

func TestSQLiteEnsureTable_Idempotent(t *testing.T) {
	repo, db := sqliteVerifRepo(t)
	if err := repo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM customer_domain_verifications").Scan(&count)
	// Table exists; no error means idempotent.
}

func TestSQLiteSaveAndGetLatest(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{
		DomainID:    1,
		Score:       75,
		Status:      "warning",
		MXStatus:    "pass",
		SPFStatus:   "pass",
		DKIMStatus:  "warning",
		DMARCStatus: "fail",
		Evidence:    `{"mx":{"status":"pass"}}`,
	}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("snapshot ID is 0 after save")
	}

	got, err := repo.GetLatest(ctx, 1)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("get latest returned nil")
	}
	if got.DomainID != 1 || got.Score != 75 {
		t.Errorf("got DomainID=%d Score=%d, want 1 75", got.DomainID, got.Score)
	}
}

func TestSQLiteGetLatest_NoRows(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	got, err := repo.GetLatest(context.Background(), 999)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent domain")
	}
}

func TestSQLiteExistsRecent(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	recent, err := repo.ExistsRecent(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("exists recent: %v", err)
	}
	if !recent {
		t.Fatal("expected recent to be true")
	}

	recent, err = repo.ExistsRecent(ctx, 1, 0)
	if err != nil {
		t.Fatalf("exists recent: %v", err)
	}
	if recent {
		t.Fatal("expected recent to be false for zero cooldown")
	}
}

func TestSQLiteTryClaim_AcquiresAndBlocks(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	claimed, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}

	claimed2, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim second: %v", err)
	}
	if claimed2 {
		t.Fatal("expected second claim to fail (duplicate)")
	}
}

func TestSQLiteTryClaim_CooldownRespected(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	claimed, err := repo.TryClaim(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("try claim: %v", err)
	}
	if claimed {
		t.Fatal("expected claim to fail (cooldown active)")
	}
}

func TestSQLiteSaveAndRelease(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.SaveAndRelease(ctx, snap, 1); err != nil {
		t.Fatalf("save and release: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("snapshot ID is 0 after save and release")
	}

	got, err := repo.GetLatest(ctx, 1)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("expected snapshot after save and release")
	}

	// Claim should be released.
	var claimCount int
	repo.db.QueryRow("SELECT COUNT(*) FROM customer_domain_verification_claims WHERE domain_id = ?", 1).Scan(&claimCount)
	if claimCount != 0 {
		t.Fatalf("expected claim to be released, count=%d", claimCount)
	}
}

func TestSQLiteConcurrentClaims_OnlyOneSucceeds(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()
	const workers = 8

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			claimed, _ := repo.TryClaim(ctx, 42, time.Minute)
			results[idx] = claimed
		}(i)
	}
	close(start)
	wg.Wait()

	success := 0
	for _, c := range results {
		if c {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", success)
	}
}

func TestSQLiteStaleClaimCleaned(t *testing.T) {
	repo, _ := sqliteVerifRepo(t)
	ctx := context.Background()

	// Insert a stale claim directly.
	if _, err := repo.db.Exec("INSERT INTO customer_domain_verification_claims (domain_id, claimed_until) VALUES (?, ?)", 1, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("insert stale claim: %v", err)
	}

	// TryClaim should clean it and acquire.
	claimed, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim after stale: %v", err)
	}
	if !claimed {
		t.Fatal("expected claim to succeed after stale cleanup")
	}
}

// ── PostgreSQL repository tests ───────────────────────────────

func postgresVerifRepo(t *testing.T) (*VerificationRepo, *sql.DB) {
	t.Helper()
	dsn := postgresDSN(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { db.Close() })

	// Create isolated schema.
	schema := fmt.Sprintf("orvix_cd_test_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("SET search_path TO public")
		db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	})

	repo := NewVerificationRepo(db)
	if err := repo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure table: %v", err)
	}
	return repo, db
}

func postgresDSN(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST")) != "1" {
		t.Skip("set ORVIX_RUN_POSTGRES_DML_TEST=1 to run postgres tests")
	}
	if strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER"))) != "postgres" {
		t.Skip("ORVIX_DB_DRIVER must be postgres")
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))
	if dsn == "" {
		t.Skip("ORVIX_DB_DSN is empty")
	}
	return dsn
}

func TestPostgresEnsureTable_CreatesTables(t *testing.T) {
	_, db := postgresVerifRepo(t)
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'customer_domain_verifications'`).Scan(&count)
	if err != nil {
		t.Fatalf("query verifications: %v", err)
	}
	if count != 1 {
		t.Fatalf("customer_domain_verifications table missing")
	}
	err = db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'customer_domain_verification_claims'`).Scan(&count)
	if err != nil {
		t.Fatalf("query claims: %v", err)
	}
	if count != 1 {
		t.Fatalf("customer_domain_verification_claims table missing")
	}
}

func TestPostgresEnsureTable_Idempotent(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	if err := repo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}

func TestPostgresSaveAndGetLatest(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{
		DomainID:    1,
		Score:       85,
		Status:      "pass",
		MXStatus:    "pass",
		SPFStatus:   "pass",
		DKIMStatus:  "pass",
		DMARCStatus: "pass",
		Evidence:    `{"mx":{"status":"pass"}}`,
	}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("snapshot ID is 0 after save (RETURNING id failed)")
	}

	got, err := repo.GetLatest(ctx, 1)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("get latest returned nil")
	}
	if got.DomainID != 1 || got.Score != 85 {
		t.Errorf("got DomainID=%d Score=%d, want 1 85", got.DomainID, got.Score)
	}
	if got.CheckedAt.IsZero() || got.CreatedAt.IsZero() {
		t.Error("timestamps are zero")
	}
}

func TestPostgresGetLatest_NoRows(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	got, err := repo.GetLatest(context.Background(), 999)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent domain")
	}
}

func TestPostgresExistsRecent(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	recent, err := repo.ExistsRecent(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("exists recent: %v", err)
	}
	if !recent {
		t.Fatal("expected recent to be true")
	}

	recent, err = repo.ExistsRecent(ctx, 1, 0)
	if err != nil {
		t.Fatalf("exists recent: %v", err)
	}
	if recent {
		t.Fatal("expected recent to be false for zero cooldown")
	}
}

func TestPostgresTryClaim_AcquiresAndBlocks(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	claimed, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}

	claimed2, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim second: %v", err)
	}
	if claimed2 {
		t.Fatal("expected second claim to fail (duplicate)")
	}
}

func TestPostgresTryClaim_CooldownRespected(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.Save(ctx, snap); err != nil {
		t.Fatalf("save: %v", err)
	}

	claimed, err := repo.TryClaim(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("try claim: %v", err)
	}
	if claimed {
		t.Fatal("expected claim to fail (cooldown active)")
	}
}

func TestPostgresSaveAndRelease(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	snap := &VerificationSnapshot{DomainID: 1, Score: 100, Status: "pass"}
	if err := repo.SaveAndRelease(ctx, snap, 1); err != nil {
		t.Fatalf("save and release: %v", err)
	}
	if snap.ID == 0 {
		t.Fatal("snapshot ID is 0 after save and release")
	}

	got, err := repo.GetLatest(ctx, 1)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if got == nil {
		t.Fatal("expected snapshot after save and release")
	}

	var claimCount int
	repo.db.QueryRow("SELECT COUNT(*) FROM customer_domain_verification_claims WHERE domain_id = $1", 1).Scan(&claimCount)
	if claimCount != 0 {
		t.Fatalf("expected claim released, count=%d", claimCount)
	}
}

func TestPostgresConcurrentClaims_OnlyOneSucceeds(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()
	const workers = 8

	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]bool, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			claimed, _ := repo.TryClaim(ctx, 42, time.Minute)
			results[idx] = claimed
		}(i)
	}
	close(start)
	wg.Wait()

	success := 0
	for _, c := range results {
		if c {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 successful claim, got %d", success)
	}
}

func TestPostgresStaleClaimCleaned(t *testing.T) {
	repo, _ := postgresVerifRepo(t)
	ctx := context.Background()

	if _, err := repo.db.Exec("INSERT INTO customer_domain_verification_claims (domain_id, claimed_until) VALUES ($1, $2)", 1, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatalf("insert stale claim: %v", err)
	}

	claimed, err := repo.TryClaim(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("try claim after stale: %v", err)
	}
	if !claimed {
		t.Fatal("expected claim to succeed after stale cleanup")
	}
}

func TestPostgresDialectDetection(t *testing.T) {
	_, db := postgresVerifRepo(t)
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		t.Fatalf("detect dialect: %v", err)
	}
	if !dialect.IsPostgres() {
		t.Fatal("expected postgres dialect")
	}
}

// ── Integration: service-level tests on PostgreSQL ───────────

func postgresServiceEnv(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dsn := postgresDSN(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db.SetMaxOpenConns(5)
	t.Cleanup(func() { db.Close() })

	schema := fmt.Sprintf("orvix_cd_svc_%d", time.Now().UnixNano())
	if _, err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := db.Exec("SET search_path TO " + schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("SET search_path TO public")
		db.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE")
	})

	// Create coremail_domains table (PostgreSQL-compatible).
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS coremail_domains (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			tenant_id BIGINT NOT NULL DEFAULT 0,
			reseller_id BIGINT NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			plan TEXT NOT NULL DEFAULT 'smb',
			description TEXT NOT NULL DEFAULT '',
			max_mailboxes BIGINT NOT NULL DEFAULT 0,
			max_aliases BIGINT NOT NULL DEFAULT 0,
			max_quota_mb BIGINT NOT NULL DEFAULT 0,
			dkim_enabled BOOLEAN NOT NULL DEFAULT false,
			dkim_selector TEXT NOT NULL DEFAULT '',
			dmarc_enabled BOOLEAN NOT NULL DEFAULT false,
			mtasts_enabled BOOLEAN NOT NULL DEFAULT false,
			catchall_address TEXT NOT NULL DEFAULT '',
			abuse_contact TEXT NOT NULL DEFAULT '',
			labels TEXT NOT NULL DEFAULT '',
			mailbox_count BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			deleted_at TIMESTAMP
		)`)
	if err != nil {
		t.Fatalf("create coremail_domains: %v", err)
	}

	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uq_cd_name ON coremail_domains(name)`)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}

	domainRepo := coremail.NewDomainSQLRepo(db)
	verifRepo := NewVerificationRepo(db)
	if err := verifRepo.EnsureTable(context.Background()); err != nil {
		t.Fatalf("ensure verifications table: %v", err)
	}
	inspector := NewDNSInspector(dnsops.NewFakeResolver())
	svc := NewService(db, domainRepo, inspector, verifRepo)
	return svc, db
}

func postgresSeedDomain(t *testing.T, svc *Service, name string, tenantID uint) uint {
	t.Helper()
	d := &coremail.Domain{Name: name, TenantID: tenantID, Status: coremail.DomainActive}
	if err := svc.domains.Create(context.Background(), d, nil); err != nil {
		t.Fatalf("create domain %s: %v", name, err)
	}
	return d.ID
}

func TestPostgresServiceTenantScopedList(t *testing.T) {
	svc, _ := postgresServiceEnv(t)
	ctx := context.Background()

	postgresSeedDomain(t, svc, "pg-t1-a.example.com", 1)
	postgresSeedDomain(t, svc, "pg-t1-b.example.com", 1)
	postgresSeedDomain(t, svc, "pg-t2-a.example.com", 2)

	resp, err := svc.ListDomains(ctx, 1, DomainListRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Total != 2 || len(resp.Domains) != 2 {
		t.Fatalf("tenant 1 sees total=%d len=%d, want 2/2", resp.Total, len(resp.Domains))
	}
	for _, d := range resp.Domains {
		if d.Name == "pg-t2-a.example.com" {
			t.Fatal("tenant 1 list leaked tenant 2 domain")
		}
	}
}

func TestPostgresServiceTenantScopedGet(t *testing.T) {
	svc, _ := postgresServiceEnv(t)
	ctx := context.Background()

	domID := postgresSeedDomain(t, svc, "pg-owned.example.com", 1)

	if _, err := svc.GetDomain(ctx, 2, domID); err != ErrDomainNotFound {
		t.Fatalf("cross-tenant get err=%v, want ErrDomainNotFound", err)
	}

	if _, err := svc.GetDomain(ctx, 1, domID); err != nil {
		t.Fatalf("owner get err=%v, want nil", err)
	}
}

func TestPostgresServiceVerifyAndCooldown(t *testing.T) {
	svc, _ := postgresServiceEnv(t)
	ctx := context.Background()
	domID := postgresSeedDomain(t, svc, "pg-verify.example.com", 1)

	if err := svc.VerifyDomain(ctx, 1, domID); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := svc.VerifyDomain(ctx, 1, domID); err != ErrVerificationCooldown {
		t.Fatalf("second verify err=%v, want ErrVerificationCooldown", err)
	}

	// Should have exactly one snapshot.
	snap, err := svc.GetLatestSnapshot(ctx, 1, domID)
	if err != nil {
		t.Fatalf("get latest: %v", err)
	}
	if snap == nil {
		t.Fatal("expected snapshot after verify")
	}
}
