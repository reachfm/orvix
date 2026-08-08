package organization

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

// newOrgTestDBWithAuth creates a real DB with both organization tables
// and full users schema, plus a real Authenticator sharing the same DB.
func newOrgTestDBWithAuth(t *testing.T) (*sql.DB, *auth.Authenticator) {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "orgauth.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	// Point at a temp key so NewAuthenticator auto-generates one.
	cfg.Auth.JWTKeyPath = filepath.Join(t.TempDir(), "jwt_key.pem")

	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, _ := gdb.DB()
	t.Cleanup(func() { sqlDB.Close() })

	// Add org-specific tables.
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS org_ownership_transfers (id INTEGER PRIMARY KEY AUTOINCREMENT, organization_id INTEGER NOT NULL, from_user_id INTEGER NOT NULL, to_user_id INTEGER NOT NULL, token_hash TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', expires_at DATETIME, accepted_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("create org table: %v", err)
	}

	realAuth, err := auth.NewAuthenticator(&cfg.Auth, gdb, logger)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	return sqlDB, realAuth
}

func TestOwnershipTransferBumpsBothVersionsAndRevokesBothTokens(t *testing.T) {
	db, authr := newOrgTestDBWithAuth(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()

	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)

	// Create FROM, TO, and third admin.
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (10, 1, 'tenant_admin', 1, 50, ?, ?, 'from@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (20, 1, 'tenant_operator', 1, 70, ?, ?, 'to@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (30, 1, 'tenant_admin', 1, 99, ?, ?, 'extra@test.local', 'h', 1)`, now, now)

	// Issue real access tokens before transfer.
	fromAccess, _, _, err := authr.GenerateAccessTokenWithJTI(10, auth.RoleTenantAdmin)
	if err != nil {
		t.Fatalf("issue FROM token: %v", err)
	}
	toAccess, _, _, err := authr.GenerateAccessTokenWithJTI(20, auth.RoleTenantOperator)
	if err != nil {
		t.Fatalf("issue TO token: %v", err)
	}

	// Prove both tokens validate before transfer.
	fromUID, fromRole, fromErr := authr.ValidateAccessToken(fromAccess)
	if fromErr != nil || fromUID != 10 || string(fromRole) != "tenant_admin" {
		t.Fatalf("FROM token before transfer: err=%v uid=%d role=%s", fromErr, fromUID, fromRole)
	}
	toUID, toRole, toErr := authr.ValidateAccessToken(toAccess)
	if toErr != nil || toUID != 20 || string(toRole) != "tenant_operator" {
		t.Fatalf("TO token before transfer: err=%v uid=%d role=%s", toErr, toUID, toRole)
	}

	// Execute real production ownership flow.
	svc := NewService(repo, nil, nil)
	transfer, _, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}
	if err := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()); err != nil {
		t.Fatalf("AcceptOwnershipTransfer: %v", err)
	}

	// Verify FROM user.
	var fRole string
	var fTV int64
	db.QueryRow("SELECT role, token_version FROM users WHERE id = 10").Scan(&fRole, &fTV)
	if fRole != "tenant_admin" || fTV != 51 {
		t.Fatalf("FROM: role=%s tv=%d", fRole, fTV)
	}
	// Verify TO user.
	var tRole2 string
	var tTV int64
	db.QueryRow("SELECT role, token_version FROM users WHERE id = 20").Scan(&tRole2, &tTV)
	if tRole2 != "tenant_admin" || tTV != 71 {
		t.Fatalf("TO: role=%s tv=%d", tRole2, tTV)
	}

	// Transfer record.
	var st TransferStatus
	var toUID2 uint
	db.QueryRow("SELECT status, to_user_id FROM org_ownership_transfers WHERE id = ?", transfer.ID).Scan(&st, &toUID2)
	if st != TransferAccepted || toUID2 != 20 {
		t.Fatalf("transfer: status=%v to_user=%d", st, toUID2)
	}

	// Old tokens must be rejected.
	fUID2, fRole2, fErr2 := authr.ValidateAccessToken(fromAccess)
	if fErr2 == nil || fUID2 != 0 || fRole2 != "" {
		t.Fatalf("FROM token after transfer: err=%v uid=%d role=%s", fErr2, fUID2, fRole2)
	}
	tUID2, tRole3, tErr2 := authr.ValidateAccessToken(toAccess)
	if tErr2 == nil || tUID2 != 0 || tRole3 != "" {
		t.Fatalf("TO token after transfer: err=%v uid=%d role=%s", tErr2, tUID2, tRole3)
	}
}

func TestCreateOwnershipTransferReturnsPersistedIDSQLite(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	tf := &OwnershipTransfer{OrganizationID: 1, FromUserID: 10, ToUserID: 20, TokenHash: "hash123", Status: TransferPending, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := repo.CreateOwnershipTransfer(ctx, tf); err != nil {
		t.Fatalf("CreateOwnershipTransfer: %v", err)
	}
	if tf.ID == 0 {
		t.Fatal("returned ID must be > 0")
	}
	var foundID uint
	db.QueryRow("SELECT id FROM org_ownership_transfers WHERE id = ?", tf.ID).Scan(&foundID)
	if foundID != tf.ID {
		t.Fatalf("query by returned ID: want %d, got %d", tf.ID, foundID)
	}
}

func TestCountAdminsTenantScoped(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'a', 'a', 'a.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (2, 'b', 'b', 'b.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	// Tenant A: 2 active tenant_admin
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (1, 1, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (2, 1, 'tenant_admin', 1, 0)`)
	// Tenant A: 1 inactive tenant_admin (should not count)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (3, 1, 'tenant_admin', 0, 0)`)
	// Tenant A: 1 deleted tenant_admin (should not count)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, deleted_at, token_version) VALUES (4, 1, 'tenant_admin', 1, datetime('now'), 0)`)
	// Tenant B: 3 active tenant_admin
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (5, 2, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (6, 2, 'tenant_admin', 1, 0)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (7, 2, 'tenant_admin', 1, 0)`)
	// PSA with NULL tenant — should not count for any tenant
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (8, NULL, 'platform_super_admin', 1, 0)`)

	cA, _ := repo.CountAdmins(ctx, 1)
	if cA != 2 {
		t.Fatalf("tenant A: want 2, got %d", cA)
	}
	cB, _ := repo.CountAdmins(ctx, 2)
	if cB != 3 {
		t.Fatalf("tenant B: want 3, got %d", cB)
	}
}

func TestOwnershipTransferConcurrentAcceptExactlyOneSucceeds(t *testing.T) {
	db, _ := newOrgTestDBWithAuth(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (10, 1, 'tenant_admin', 1, 50, ?, ?, 'c10@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (20, 1, 'tenant_support', 1, 70, ?, ?, 'c20@test.local', 'h', 1)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version, created_at, updated_at, email, password_hash, email_verified) VALUES (30, 1, 'tenant_admin', 1, 99, ?, ?, 'c30@test.local', 'h', 1)`, now, now)

	svc := NewService(repo, nil, nil)
	transfer, _, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}

	results := make(chan error, 2)
	go func() { results <- repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()) }()
	go func() { results <- repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()) }()
	e1 := <-results
	e2 := <-results

	if e1 == nil && e2 == nil {
		t.Fatal("both concurrent accepts succeeded — atomicity broken")
	}
	if e1 != nil && e2 != nil {
		t.Fatalf("both concurrent accepts failed: %v / %v", e1, e2)
	}

	// Verify exactly one bump each.
	var fromTV, toTV int64
	db.QueryRow("SELECT token_version FROM users WHERE id = 10").Scan(&fromTV)
	db.QueryRow("SELECT token_version FROM users WHERE id = 20").Scan(&toTV)
	if fromTV != 51 {
		t.Fatalf("FROM token_version: want 51, got %d", fromTV)
	}
	if toTV != 71 {
		t.Fatalf("TO token_version: want 71, got %d", toTV)
	}
	var accCount int
	db.QueryRow("SELECT COUNT(*) FROM org_ownership_transfers WHERE status = ?", TransferAccepted).Scan(&accCount)
	if accCount != 1 {
		t.Fatalf("accepted transfer count: want 1, got %d", accCount)
	}
}

func TestOwnershipTransferRollbackPreservesUsersAndTransfer(t *testing.T) {
	db := newOrganizationTestDB(t)
	repo := NewOrganizationRepo(db)
	ctx := context.Background()
	now := time.Now().UTC()
	db.Exec(`INSERT INTO tenants (id, name, slug, domain, plan, max_domains, max_mailboxes, active, created_at, updated_at) VALUES (1, 'org', 'org', 'org.test', 'enterprise', 0, 0, 1, ?, ?)`, now, now)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (10, 1, 'tenant_admin', 1, 50)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (20, 1, 'tenant_support', 1, 70)`)
	db.Exec(`INSERT INTO users (id, tenant_id, role, active, token_version) VALUES (30, 1, 'tenant_admin', 1, 99)`)

	svc := NewService(repo, nil, nil)
	transfer, _, err := svc.RequestOwnershipTransfer(ctx, 1, 10, 20)
	if err != nil {
		t.Fatalf("RequestOwnershipTransfer: %v", err)
	}
	// Accept transfer — must succeed.
	if err := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC()); err != nil {
		t.Fatalf("AcceptOwnershipTransfer: %v", err)
	}

	// Second accept must fail with ErrTransferAlreadyUsed.
	err2 := repo.AcceptOwnershipTransfer(ctx, transfer.ID, time.Now().UTC())
	if err2 == nil || err2 != ErrTransferAlreadyUsed {
		t.Fatalf("second accept: want ErrTransferAlreadyUsed, got %v", err2)
	}

	// Users must not be changed a second time.
	var fromTV, toTV int64
	db.QueryRow("SELECT token_version FROM users WHERE id = 10").Scan(&fromTV)
	db.QueryRow("SELECT token_version FROM users WHERE id = 20").Scan(&toTV)
	if fromTV != 51 {
		t.Fatalf("FROM token_version: want 51, got %d", fromTV)
	}
	if toTV != 71 {
		t.Fatalf("TO token_version: want 71, got %d", toTV)
	}
}
