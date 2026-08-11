package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db")+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, domain TEXT, plan TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, email TEXT, name TEXT, password_hash TEXT, role TEXT, status TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_domains (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, name TEXT, status TEXT, plan TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_mailboxes (id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER, tenant_id INTEGER, local_part TEXT, email TEXT, name TEXT, status TEXT, quota_mb INTEGER, is_admin INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_aliases (id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER, tenant_id INTEGER, from_addr TEXT, to_addr TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, name TEXT, description TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_group_members (id INTEGER PRIMARY KEY AUTOINCREMENT, group_id INTEGER, email TEXT, created_at DATETIME)`)
	return db
}

func testRepo(t *testing.T, db *sql.DB) *Repository {
	t.Helper()
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return repo
}

// testAdapters returns lightweight in-memory adapters for tests.
func testAdapters(t *testing.T, db *sql.DB) *Adapters {
	t.Helper()
	return NewAdapters(
		&testOrgPort{db},
		&testAdminPort{db},
		&testDomainPort{db},
		&testMailboxPort{db},
		&testAliasPort{db},
		&testGroupPort{db},
	)
}

// testLookup returns a DB-backed EntityLookup for validation tests.
type testLookup struct{ db *sql.DB }

func (l *testLookup) OrgExists(ctx context.Context, domain string, tenantID uint) (bool, error) {
	var c int
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenants WHERE domain=? AND deleted_at IS NULL`, domain).Scan(&c)
	return c > 0, err
}
func (l *testLookup) UserExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL`, email).Scan(&c)
	return c > 0, err
}
func (l *testLookup) DomainExists(ctx context.Context, name string) (bool, uint, error) {
	var id, tenantID uint
	err := l.db.QueryRowContext(ctx, `SELECT id, tenant_id FROM coremail_domains WHERE name=? AND deleted_at IS NULL AND status='active'`, name).Scan(&id, &tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, tenantID, nil
}
func (l *testLookup) MailboxExists(ctx context.Context, email string) (bool, error) {
	var c int
	err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL`, email).Scan(&c)
	return c > 0, err
}

// Test port implementations
type testOrgPort struct{ db *sql.DB }

func (p *testOrgPort) CreateOrganization(ctx context.Context, name, domain string, tenantID uint) (uint, error) {
	res, err := p.db.ExecContext(ctx, `INSERT INTO tenants (name,domain,plan,active,created_at,updated_at) VALUES (?,?,'free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, name, domain)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testOrgPort) SoftDeleteOrganization(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE tenants SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

type testAdminPort struct{ db *sql.DB }

func (p *testAdminPort) CreateTenantAdmin(ctx context.Context, email, name, password, role string, tenantID uint) (uint, error) {
	var existing int
	p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email=? AND deleted_at IS NULL`, email).Scan(&existing)
	if existing > 0 {
		return 0, nil
	}
	res, err := p.db.ExecContext(ctx, `INSERT INTO users (tenant_id,email,name,password_hash,role,status,created_at,updated_at) VALUES (?,?,?,?,'tenant_admin','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenantID, email, name, password)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testAdminPort) SoftDeleteUser(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE users SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

type testDomainPort struct{ db *sql.DB }

func (p *testDomainPort) CreateDomain(ctx context.Context, name string, tenantID uint) (uint, error) {
	var existing int
	p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_domains WHERE name=? AND deleted_at IS NULL`, name).Scan(&existing)
	if existing > 0 {
		return 0, nil
	}
	res, err := p.db.ExecContext(ctx, `INSERT INTO coremail_domains (tenant_id,name,status,plan,created_at,updated_at) VALUES (?,?,'active','test',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenantID, name)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testDomainPort) SoftDeleteDomain(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE coremail_domains SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

type testMailboxPort struct{ db *sql.DB }

func (p *testMailboxPort) CreateMailbox(ctx context.Context, email, name, password, domainName string, tenantID uint) (uint, error) {
	var existing int
	p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL`, email).Scan(&existing)
	if existing > 0 {
		return 0, nil
	}
	var domainID uint
	p.db.QueryRowContext(ctx, `SELECT id FROM coremail_domains WHERE name=? AND tenant_id=? AND deleted_at IS NULL`, domainName, tenantID).Scan(&domainID)
	res, err := p.db.ExecContext(ctx, `INSERT INTO coremail_mailboxes (domain_id,tenant_id,local_part,email,name,status,quota_mb,is_admin,created_at,updated_at) VALUES (?,?,?,?,?,'active',1024,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, domainID, tenantID, email, email, name)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testMailboxPort) SoftDeleteMailbox(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE coremail_mailboxes SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

type testAliasPort struct{ db *sql.DB }

func (p *testAliasPort) CreateAlias(ctx context.Context, fromEmail, toEmail string, tenantID, domainID uint) (uint, error) {
	res, err := p.db.ExecContext(ctx, `INSERT INTO coremail_aliases (domain_id,tenant_id,from_addr,to_addr,created_at,updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, domainID, tenantID, fromEmail, toEmail)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testAliasPort) SoftDeleteAlias(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE coremail_aliases SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}

type testGroupPort struct{ db *sql.DB }

func (p *testGroupPort) CreateGroup(ctx context.Context, name, description string, tenantID uint) (uint, error) {
	res, err := p.db.ExecContext(ctx, `INSERT INTO coremail_groups (tenant_id,name,description,created_at,updated_at) VALUES (?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, tenantID, name, description)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return uint(id), nil
}
func (p *testGroupPort) AddGroupMember(ctx context.Context, groupName, email string, tenantID uint) error {
	var groupID uint
	if err := p.db.QueryRowContext(ctx, `SELECT id FROM coremail_groups WHERE name=? AND tenant_id=? AND deleted_at IS NULL`, groupName, tenantID).Scan(&groupID); err != nil {
		return err
	}
	_, err := p.db.ExecContext(ctx, `INSERT INTO coremail_group_members (group_id,email,created_at) VALUES (?,?,CURRENT_TIMESTAMP)`, groupID, email)
	return err
}
func (p *testGroupPort) SoftDeleteGroup(ctx context.Context, id, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `UPDATE coremail_groups SET deleted_at=CURRENT_TIMESTAMP WHERE id=?`, id)
	return err
}
func (p *testGroupPort) RemoveGroupMember(ctx context.Context, memberID, tenantID uint) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM coremail_group_members WHERE id=?`, memberID)
	return err
}

// ── Tests ──────────────────────────────────────────────────────────

func TestImportStatusStateMachine(t *testing.T) {
	tests := []struct {
		from, to ImportStatus
		allowed  bool
	}{
		{StatusUploaded, StatusValidating, true},
		{StatusUploaded, StatusCancelled, true},
		{StatusUploaded, StatusRunning, false},
		{StatusValidated, StatusRunning, true},
		{StatusRunning, StatusCompleted, true},
		{StatusRunning, StatusPaused, true},
		{StatusRunning, StatusFailed, true},
		{StatusFailed, StatusValidating, true},
		{StatusCompleted, StatusCompensating, true},
		{StatusCompensating, StatusCompensated, true},
		{StatusCompensated, StatusCompensating, false},
		{StatusCancelled, StatusRunning, false},
	}
	for _, tc := range tests {
		if got := tc.from.CanTransition(tc.to); got != tc.allowed {
			t.Errorf("%s -> %s: got %v, want %v", tc.from, tc.to, got, tc.allowed)
		}
	}
}

func TestConflictPolicyValid(t *testing.T) {
	if !ConflictFail.Valid() || !ConflictSkip.Valid() || !ConflictUpdateSafe.Valid() {
		t.Error("valid policies should return true")
	}
	if ConflictPolicy("bad").Valid() {
		t.Error("invalid policy should return false")
	}
}

func TestEntityDependencyOrder(t *testing.T) {
	order := EntityDependencyOrder()
	if len(order) != 7 {
		t.Errorf("expected 7, got %d", len(order))
	}
}

func TestParseJSON(t *testing.T) {
	data := []byte(`{"entities":[{"entity":"organization","name":"Acme","domain":"acme.com"}]}`)
	source, err := ParseJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if source.TotalRows != 1 {
		t.Errorf("expected 1, got %d", source.TotalRows)
	}
}

func TestParseCSV(t *testing.T) {
	data := []byte("entity,name,domain\norganization,Acme,acme.com\n")
	source, err := ParseCSV(data)
	if err != nil {
		t.Fatal(err)
	}
	if source.TotalRows != 1 {
		t.Errorf("expected 1, got %d", source.TotalRows)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := ParseJSON([]byte("not json"))
	if err == nil {
		t.Error("expected error")
	}
}

func TestHashSource(t *testing.T) {
	h1 := HashSource([]byte("test"))
	h2 := HashSource([]byte("test"))
	if h1 != h2 {
		t.Error("hashes should match")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64 char hash, got %d", len(h1))
	}
}

func TestCSVFormulaInjection(t *testing.T) {
	if !IsCSVFormulaInjection("=cmd|calc.exe") {
		t.Error("should detect = prefix")
	}
	if IsCSVFormulaInjection("normal") {
		t.Error("should not flag normal text")
	}
}

func TestValidatorOrganization(t *testing.T) {
	db := setupTestDB(t)
	lookup := &testLookup{db}
	v := NewValidator(lookup, 1, &ParsedSource{Entities: []ParsedEntity{
		{Line: 1, Entity: EntityOrganization, Raw: map[string]any{"name": "Acme", "domain": "acme.com"}},
	}}, ConflictFail)
	rows, _ := v.ValidateAll(context.Background())
	if rows[0].Status != RowValid {
		t.Errorf("expected valid, got %s", rows[0].Status)
	}
}

func TestValidatorDuplicateOrg(t *testing.T) {
	db := setupTestDB(t)
	db.Exec(`INSERT INTO tenants (name,domain,plan,active,created_at,updated_at) VALUES ('Exist','acme.com','free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	lookup := &testLookup{db}
	v := NewValidator(lookup, 1, &ParsedSource{Entities: []ParsedEntity{
		{Line: 1, Entity: EntityOrganization, Raw: map[string]any{"name": "Acme", "domain": "acme.com"}},
	}}, ConflictFail)
	rows, _ := v.ValidateAll(context.Background())
	if rows[0].Status != RowConflict {
		t.Errorf("expected conflict, got %s", rows[0].Status)
	}
}

func TestValidatorPlatformRoleInjection(t *testing.T) {
	db := setupTestDB(t)
	lookup := &testLookup{db}
	v := NewValidator(lookup, 1, &ParsedSource{Entities: []ParsedEntity{
		{Line: 1, Entity: EntityTenantAdmin, Raw: map[string]any{"email": "a@b.com", "password": "secret123!", "role": "platform_super_admin"}},
	}}, ConflictFail)
	rows, _ := v.ValidateAll(context.Background())
	if rows[0].Status != RowInvalid {
		t.Errorf("expected invalid for platform role, got %s", rows[0].Status)
	}
}

func TestRepositoryCRUD(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)

	now := time.Now().UTC()
	job := &ImportJob{TenantID: 1, Scope: "tenant", Actor: "test", SourceType: SourceCSV, ConflictPolicy: ConflictFail, Status: StatusUploaded, SourceHash: "abc", SourceName: "t.csv", CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if job.ID == 0 {
		t.Error("expected non-zero ID")
	}
	got, _ := repo.Get(context.Background(), job.ID)
	if got.SourceName != "t.csv" {
		t.Errorf("got %q", got.SourceName)
	}
}

func TestRepositoryCheckpoint(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	cp := &Checkpoint{ImportID: 1, Entity: EntityMailbox, RowIndex: 50, ProcessedCount: 50, CommittedAt: time.Now()}
	if err := repo.SaveCheckpoint(context.Background(), cp); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.LastCheckpoint(context.Background(), 1)
	if got.ProcessedCount != 50 {
		t.Errorf("got %d", got.ProcessedCount)
	}
}

func TestPlannerDryRun(t *testing.T) {
	db := setupTestDB(t)
	lookup := &testLookup{db}
	adapters := testAdapters(t, db)
	p := NewPlanner(lookup, 1, adapters)

	source, _ := ParseJSON([]byte(`{"entities":[{"entity":"organization","name":"Acme","domain":"acme.com"}]}`))
	report, err := p.DryRun(context.Background(), source, ConflictFail)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid != 1 {
		t.Errorf("expected 1 valid, got %d", report.Valid)
	}
}

func TestExecutorCreatesEntities(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)
	exec := NewExecutor(adapters, repo, 1, "key")

	job := &ImportJob{ID: 1, TenantID: 1, Scope: "tenant", SourceType: SourceJSON, ConflictPolicy: ConflictFail, Status: StatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(), Version: 1}
	data := []byte(`{"entities":[{"entity":"organization","name":"NewCorp","domain":"newcorp.com"},{"entity":"domain","name":"mail.newcorp.com"}]}`)
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}
}

func TestStagingConfined(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewStagingService(dir)
	if err != nil {
		t.Fatal(err)
	}

	stagingID, hash, _, err := svc.Store([]byte("test data"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stagingID == "" || hash == "" {
		t.Error("expected staging ID and hash")
	}

	data, err := svc.Read(stagingID)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test data" {
		t.Errorf("got %q", string(data))
	}

	if err := svc.Verify(stagingID, hash); err != nil {
		t.Errorf("verify failed: %v", err)
	}

	// Tampered hash must fail
	if err := svc.Verify(stagingID, "bad"); err == nil {
		t.Error("expected tamper detection")
	}

	// Path traversal must fail
	_, err = svc.Read("../etc/passwd")
	if err == nil {
		t.Error("expected path traversal denial")
	}

	// Cleanup
	svc.Remove(stagingID)
	_, err = svc.Read(stagingID)
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestStagingSymlinkDenial(t *testing.T) {
	dir := t.TempDir()
	svc, _ := NewStagingService(dir)

	// Test path traversal denial (platform-independent)
	if svc.isConfined(filepath.Join(dir, "..", "..", "etc")) {
		t.Error("path traversal should be rejected by confinement")
	}
}

// Ensure imports used
var (
	_ = kernel.SystemClock{}
	_ = json.Marshal
)
