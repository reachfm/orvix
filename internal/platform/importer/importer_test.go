package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, *Repository) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, domain TEXT, plan TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, email TEXT, name TEXT, password_hash TEXT, role TEXT, status TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_domains (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, name TEXT, status TEXT, plan TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_mailboxes (id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER, tenant_id INTEGER, local_part TEXT, email TEXT, name TEXT, status TEXT, quota_mb INTEGER, is_admin INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_aliases (id INTEGER PRIMARY KEY AUTOINCREMENT, domain_id INTEGER, tenant_id INTEGER, from_addr TEXT, to_addr TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER, name TEXT, description TEXT, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS coremail_group_members (id INTEGER PRIMARY KEY AUTOINCREMENT, group_id INTEGER, email TEXT, created_at DATETIME)`)

	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, repo
}

// ── Domain Tests ───────────────────────────────────────────────────

func TestImportStatusStateMachine(t *testing.T) {
	validTransitions := []struct {
		from, to ImportStatus
		allowed  bool
	}{
		{StatusUploaded, StatusValidating, true},
		{StatusUploaded, StatusCancelled, true},
		{StatusUploaded, StatusRunning, false},
		{StatusUploaded, StatusCompleted, false},

		{StatusValidating, StatusValidated, true},
		{StatusValidating, StatusValidationFailed, true},
		{StatusValidating, StatusCancelled, true},
		{StatusValidating, StatusRunning, false},

		{StatusValidated, StatusRunning, true},
		{StatusValidated, StatusValidating, true},
		{StatusValidated, StatusCancelled, true},

		{StatusRunning, StatusCompleted, true},
		{StatusRunning, StatusPaused, true},
		{StatusRunning, StatusFailed, true},
		{StatusRunning, StatusCancelled, true},
		{StatusRunning, StatusValidating, false},

		{StatusPaused, StatusRunning, true},
		{StatusPaused, StatusCancelled, true},

		{StatusFailed, StatusValidating, true},
		{StatusFailed, StatusCancelled, true},

		{StatusCancelled, StatusRunning, false},
		{StatusCompleted, StatusCompensating, true},
		{StatusCompleted, StatusRunning, false},

		{StatusCompensating, StatusCompensated, true},
		{StatusCompensating, StatusCompensationFailed, true},

		{StatusCompensated, StatusCompensating, false},
		{StatusCompensationFailed, StatusCompensating, true},
	}

	for _, tc := range validTransitions {
		if got := tc.from.CanTransition(tc.to); got != tc.allowed {
			t.Errorf("%s -> %s: got %v, want %v", tc.from, tc.to, got, tc.allowed)
		}
	}
}

func TestConflictPolicyValues(t *testing.T) {
	tests := []struct {
		policy ConflictPolicy
		valid  bool
	}{
		{ConflictFail, true},
		{ConflictSkip, true},
		{ConflictUpdateSafe, true},
		{ConflictPolicy("invalid"), false},
		{ConflictPolicy(""), false},
	}
	for _, tc := range tests {
		if got := tc.policy.Valid(); got != tc.valid {
			t.Errorf("ConflictPolicy %q: got %v, want %v", tc.policy, got, tc.valid)
		}
	}
}

func TestEntityDependencyOrder(t *testing.T) {
	order := EntityDependencyOrder()
	if len(order) != 7 {
		t.Errorf("expected 7 entities, got %d", len(order))
	}
	orgIdx := -1
	domainIdx := -1
	mbIdx := -1
	for i, e := range order {
		switch e {
		case EntityOrganization:
			orgIdx = i
		case EntityDomain:
			domainIdx = i
		case EntityMailbox:
			mbIdx = i
		}
	}
	if orgIdx >= domainIdx {
		t.Error("organization must come before domain")
	}
	if domainIdx >= mbIdx {
		t.Error("domain must come before mailbox")
	}
}

// ── Parser Tests ───────────────────────────────────────────────────

func TestParseJSON(t *testing.T) {
	data := []byte(`{"schema_version":1,"entities":[{"entity":"organization","name":"Acme Inc","domain":"acme.com"},{"entity":"domain","name":"mail.acme.com"},{"entity":"mailbox","email":"user@acme.com","name":"User","domain":"acme.com","password":"secret123"}]}`)

	source, err := ParseJSON(data)
	if err != nil {
		t.Fatalf("ParseJSON failed: %v", err)
	}
	if source.SchemaVersion != 1 {
		t.Errorf("expected schema version 1, got %d", source.SchemaVersion)
	}
	if source.TotalRows != 3 {
		t.Errorf("expected 3 rows, got %d", source.TotalRows)
	}
	if source.SourceType != SourceJSON {
		t.Errorf("expected SourceJSON, got %v", source.SourceType)
	}
	if len(source.Entities) != 3 {
		t.Fatalf("expected 3 entities, got %d", len(source.Entities))
	}
	if source.Entities[0].Entity != EntityOrganization {
		t.Errorf("first entity should be organization, got %s", source.Entities[0].Entity)
	}
}

func TestParseCSV(t *testing.T) {
	data := []byte("entity,name,domain,email,password\norganization,Acme Inc,acme.com,,\ndomain,,mail.acme.com,,\nmailbox,,,user@acme.com,secret123\n")

	source, err := ParseCSV(data)
	if err != nil {
		t.Fatalf("ParseCSV failed: %v", err)
	}
	if source.TotalRows != 3 {
		t.Errorf("expected 3 rows, got %d", source.TotalRows)
	}
	if source.Entities[0].Entity != EntityOrganization {
		t.Errorf("first entity should be organization, got %s", source.Entities[0].Entity)
	}
	if len(source.Entities) != 3 {
		t.Errorf("expected 3 entities, got %d", len(source.Entities))
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := ParseJSON([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseEmptyCSV(t *testing.T) {
	_, err := ParseCSV([]byte(""))
	if err == nil {
		t.Error("expected error for empty CSV")
	}
}

func TestParseTooManyRows(t *testing.T) {
	// Generate a large body exceeding MaxSourceRows
	large := "entity,name,domain\n"
	for i := 0; i < MaxSourceRows+10; i++ {
		large += "organization,Test,example.com\n"
	}
	_, err := ParseCSV([]byte(large))
	if err == nil {
		t.Error("expected error for too many rows")
	}
}

func TestHashSource(t *testing.T) {
	h1 := HashSource([]byte("test"))
	h2 := HashSource([]byte("test"))
	h3 := HashSource([]byte("other"))
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different inputs should produce different hashes")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d", len(h1))
	}
}

func TestDetectSourceType(t *testing.T) {
	csv, err := DetectSourceType([]byte("col1,col2\nval1,val2"))
	if err != nil || csv != SourceCSV {
		t.Errorf("expected SourceCSV, got %v err=%v", csv, err)
	}
	jsonType, err := DetectSourceType([]byte(`{"key":"value"}`))
	if err != nil || jsonType != SourceJSON {
		t.Errorf("expected SourceJSON, got %v err=%v", jsonType, err)
	}
	_, err = DetectSourceType([]byte(""))
	if err == nil {
		t.Error("expected error for empty input")
	}
}

func TestCSVFormulaInjection(t *testing.T) {
	if !IsCSVFormulaInjection("=cmd|calc.exe") {
		t.Error("should detect formula injection")
	}
	if !IsCSVFormulaInjection("+cmd|/c calc.exe") {
		t.Error("should detect formula injection with + prefix")
	}
	if !IsCSVFormulaInjection("@SUM(A1:A10)") {
		t.Error("should detect formula injection with @ prefix")
	}
	if IsCSVFormulaInjection("normal text") {
		t.Error("should not flag normal text")
	}
	if IsCSVFormulaInjection("ex=claim") {
		t.Error("should not flag text ending with =")
	}
	if SanitizeCSVExport("=cmd") != "'=cmd" {
		t.Error("should sanitize formula injection")
	}
}

// ── Validator Tests ────────────────────────────────────────────────

func TestValidatorOrganization(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	validator := NewValidator(db, 1, &ParsedSource{
		Entities: []ParsedEntity{
			{Line: 1, Entity: EntityOrganization, Raw: map[string]any{"name": "Acme", "domain": "acme.com"}, Data: []byte(`{"name":"Acme","domain":"acme.com"}`)},
		},
	}, ConflictFail)

	rows, err := validator.ValidateAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Status != RowValid {
		t.Errorf("expected valid status, got %s", rows[0].Status)
	}
}

func TestValidatorDuplicateOrg(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()
	db.Exec(`INSERT INTO tenants (name, domain, plan, active, created_at, updated_at) VALUES ('Existing','acme.com','free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)

	validator := NewValidator(db, 1, &ParsedSource{
		Entities: []ParsedEntity{
			{Line: 1, Entity: EntityOrganization, Raw: map[string]any{"name": "Acme", "domain": "acme.com"}, Data: []byte(`{}`)},
		},
	}, ConflictFail)

	rows, _ := validator.ValidateAll(context.Background())
	if rows[0].Status != RowConflict {
		t.Errorf("expected conflict status, got %s", rows[0].Status)
	}
}

func TestValidatorDuplicateOrgSkip(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()
	db.Exec(`INSERT INTO tenants (name, domain, plan, active, created_at, updated_at) VALUES ('Existing','acme.com','free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)

	validator := NewValidator(db, 1, &ParsedSource{
		Entities: []ParsedEntity{
			{Line: 1, Entity: EntityOrganization, Raw: map[string]any{"name": "Acme", "domain": "acme.com"}, Data: []byte(`{}`)},
		},
	}, ConflictSkip)

	rows, _ := validator.ValidateAll(context.Background())
	if rows[0].Status != RowSkipped {
		t.Errorf("expected skipped status with skip policy, got %s", rows[0].Status)
	}
}

func TestValidatorPlatformRoleInjection(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	validator := NewValidator(db, 1, &ParsedSource{
		Entities: []ParsedEntity{
			{Line: 1, Entity: EntityTenantAdmin, Raw: map[string]any{"email": "admin@acme.com", "password": "secret123", "name": "Admin", "role": "platform_super_admin"}, Data: []byte(`{}`)},
		},
	}, ConflictFail)

	rows, _ := validator.ValidateAll(context.Background())
	if rows[0].Status != RowInvalid {
		t.Errorf("expected invalid status for platform role injection, got %s", rows[0].Status)
	}
}

func TestValidatorMissingDomain(t *testing.T) {
	db, _ := setupTestDB(t)
	defer db.Close()

	validator := NewValidator(db, 1, &ParsedSource{
		Entities: []ParsedEntity{
			{Line: 1, Entity: EntityMailbox, Raw: map[string]any{"email": "user@acme.com", "name": "User", "domain": "acme.com", "password": "secret123"}, Data: []byte(`{}`)},
		},
	}, ConflictFail)

	rows, _ := validator.ValidateAll(context.Background())
	if rows[0].Status != RowDeferred {
		t.Errorf("expected deferred status for missing parent domain, got %s: %v", rows[0].Status, rows[0].Errors)
	}
}

// ── Repository Tests ───────────────────────────────────────────────

func TestRepositoryCRUD(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	now := time.Now().UTC()
	job := &ImportJob{
		TenantID:       1,
		Scope:          "tenant",
		Actor:          "test",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictFail,
		SchemaVersion:  1,
		Status:         StatusUploaded,
		SourceHash:     "abc123",
		SourceName:     "test.csv",
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}

	err := repo.Create(context.Background(), job)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if job.ID == 0 {
		t.Error("expected non-zero ID after create")
	}

	got, err := repo.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.SourceName != "test.csv" {
		t.Errorf("expected source name 'test.csv', got %q", got.SourceName)
	}

	listed, total, err := repo.List(context.Background(), ImportFilter{Scope: "tenant", TenantID: 1})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 {
		t.Errorf("expected 1 job, got %d", total)
	}
	if len(listed) != 1 {
		t.Errorf("expected 1 item, got %d", len(listed))
	}

	err = repo.UpdateStatus(context.Background(), job.ID, StatusUploaded, StatusValidating, job.Version)
	if err != nil {
		t.Fatalf("UpdateStatus failed: %v", err)
	}
	got, _ = repo.Get(context.Background(), job.ID)
	if got.Status != StatusValidating {
		t.Errorf("expected status validating, got %s", got.Status)
	}
}

func TestRepositoryCheckpoint(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	cp := &Checkpoint{
		ImportID:       1,
		Entity:         EntityMailbox,
		RowIndex:       50,
		SucceededIDs:   []uint{1, 2, 3},
		ProcessedCount: 50,
		CommittedAt:    time.Now().UTC(),
	}

	err := repo.SaveCheckpoint(context.Background(), cp)
	if err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	got, err := repo.LastCheckpoint(context.Background(), 1)
	if err != nil {
		t.Fatalf("LastCheckpoint failed: %v", err)
	}
	if got.Entity != EntityMailbox {
		t.Errorf("expected entity mailbox, got %s", got.Entity)
	}
	if got.RowIndex != 50 {
		t.Errorf("expected row index 50, got %d", got.RowIndex)
	}
	if got.ProcessedCount != 50 {
		t.Errorf("expected processed count 50, got %d", got.ProcessedCount)
	}
}

func TestRepositoryCompensation(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	rec := &CompensationRecord{
		ImportID:   1,
		ResourceID: 100,
		EntityType: EntityMailbox,
		RowKey:     "mb_user@acme.com",
		RowIndex:   10,
		Status:     "pending",
		CreatedAt:  time.Now().UTC(),
	}

	err := repo.SaveCompensationRecord(context.Background(), rec)
	if err != nil {
		t.Fatalf("SaveCompensationRecord failed: %v", err)
	}

	records, err := repo.GetCompensationRecords(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetCompensationRecords failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ResourceID != 100 {
		t.Errorf("expected resource ID 100, got %d", records[0].ResourceID)
	}
}

func TestRepositoryConcurrentStatusUpdate(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	now := time.Now().UTC()
	job := &ImportJob{TenantID: 1, Scope: "tenant", SourceType: SourceCSV, ConflictPolicy: ConflictFail, Status: StatusUploaded, SourceHash: "hash", CreatedAt: now, UpdatedAt: now, Version: 1}
	repo.Create(context.Background(), job)

	// First update succeeds
	err := repo.UpdateStatus(context.Background(), job.ID, StatusUploaded, StatusValidating, job.Version)
	if err != nil {
		t.Fatalf("first UpdateStatus failed: %v", err)
	}

	// Second update with stale version should fail
	err = repo.UpdateStatus(context.Background(), job.ID, StatusUploaded, StatusValidated, job.Version)
	if err == nil {
		t.Error("expected error for stale version update")
	}
}

// ── Service Tests ──────────────────────────────────────────────────

func TestServiceCreateImport(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	svc := NewService(repo, adapters, nil)

	params := CreateImportParams{
		TenantID:       1,
		Scope:          "tenant",
		Actor:          "test",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictFail,
		SchemaVersion:  1,
		SourceHash:     HashSource([]byte("test data")),
		SourceName:     "test.csv",
	}

	job, err := svc.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if job.ID == 0 {
		t.Error("expected non-zero ID")
	}
	if job.Status != StatusUploaded {
		t.Errorf("expected status uploaded, got %s", job.Status)
	}
}

func TestServiceList(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	svc := NewService(repo, adapters, nil)

	svc.Create(context.Background(), CreateImportParams{
		TenantID: 1, Scope: "tenant", Actor: "test", SourceType: SourceCSV, SourceHash: "hash1", SourceName: "test1.csv",
	})
	svc.Create(context.Background(), CreateImportParams{
		TenantID: 1, Scope: "tenant", Actor: "test", SourceType: SourceCSV, SourceHash: "hash2", SourceName: "test2.csv",
	})

	result, err := svc.List(context.Background(), ImportFilter{TenantID: 1, Scope: "tenant"})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if result.TotalCount != 2 {
		t.Errorf("expected 2 jobs, got %d", result.TotalCount)
	}
}

func TestServiceActiveJobRejection(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	svc := NewService(repo, adapters, nil)

	hash := HashSource([]byte("test"))

	params := CreateImportParams{
		TenantID: 1, Scope: "tenant", Actor: "test", SourceType: SourceCSV,
		ConflictPolicy: ConflictFail, SourceHash: hash, SourceName: "test.csv",
	}

	_, err := svc.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}

	// Second with same hash should be rejected
	_, err = svc.Create(context.Background(), params)
	if err == nil {
		t.Error("expected ErrActiveJob for duplicate active import")
	}
}

func TestServiceDryRun(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	svc := NewService(repo, adapters, nil)

	data := []byte(`{"entities":[{"entity":"organization","name":"Acme","domain":"acme.com"},{"entity":"domain","name":"mail.acme.com"}]}`)

	report, err := svc.ExecuteDryRun(context.Background(), data, SourceJSON, 1, ConflictFail)
	if err != nil {
		t.Fatalf("ExecuteDryRun failed: %v", err)
	}
	if report.Total != 2 {
		t.Errorf("expected 2 rows, got %d", report.Total)
	}
	if report.Valid != 2 {
		t.Errorf("expected 2 valid, got %d", report.Valid)
	}
	if report.SourceHash == "" {
		t.Error("expected non-empty source hash")
	}
}

// ── Planner Tests ──────────────────────────────────────────────────

func TestPlannerDryRunOrder(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	svc := NewService(repo, adapters, nil)

	// Entities in wrong order should be reordered
	data := []byte(`{"entities":[{"entity":"mailbox","email":"user@acme.com","name":"User","domain":"acme.com","password":"secret123"},{"entity":"domain","name":"acme.com"},{"entity":"organization","name":"Acme","domain":"acme.com"}]}`)

	report, err := svc.ExecuteDryRun(context.Background(), data, SourceJSON, 1, ConflictFail)
	if err != nil {
		t.Fatalf("ExecuteDryRun failed: %v", err)
	}
	if len(report.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(report.Rows))
	}
	// Rows should be ordered by dependency: org -> domain -> mailbox
	if report.Rows[0].Entity != EntityOrganization {
		t.Errorf("first entity should be organization, got %s", report.Rows[0].Entity)
	}
	if report.Rows[1].Entity != EntityDomain {
		t.Errorf("second entity should be domain, got %s", report.Rows[1].Entity)
	}
	if report.Rows[2].Entity != EntityMailbox {
		t.Errorf("third entity should be mailbox, got %s", report.Rows[2].Entity)
	}
}

// ── Executor Tests ─────────────────────────────────────────────────

func TestExecutorCreatesEntities(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()

	// Seed a tenant
	db.Exec(`INSERT INTO tenants (id, name, domain, plan, active, created_at, updated_at) VALUES (1,'Existing','existing.com','free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)

	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	executor := NewExecutor(adapters, repo, 1, "test-key")

	now := time.Now().UTC()
	job := &ImportJob{
		ID: 1, TenantID: 1, Scope: "tenant", SourceType: SourceJSON,
		ConflictPolicy: ConflictFail, Status: StatusRunning, SourceHash: "hash",
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}

	data := []byte(`{"entities":[{"entity":"organization","name":"NewCorp","domain":"newcorp.com"},{"entity":"domain","name":"mail.newcorp.com"}]}`)

	result, err := executor.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}
}

func TestExecutorSkipsExistingEntities(t *testing.T) {
	db, repo := setupTestDB(t)
	defer db.Close()
	db.Exec(`INSERT INTO tenants (id, name, domain, plan, active, created_at, updated_at) VALUES (1,'Existing','existing.com','free',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)

	adapters := NewServiceAdapters(db, nil, nil, nil, nil, nil)
	executor := NewExecutor(adapters, repo, 1, "test-key")

	job := &ImportJob{ID: 2, TenantID: 1, Scope: "tenant", SourceType: SourceJSON, ConflictPolicy: ConflictFail, Status: StatusRunning, CreatedAt: time.Now(), UpdatedAt: time.Now(), Version: 1}

	data := []byte(`{"entities":[{"entity":"organization","name":"Existing","domain":"existing.com"}]}`)

	// Org already exists but executor should not crash
	_, err := executor.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatalf("Execute should not error on existing entities: %v", err)
	}
}

// ── Dialect Tests ──────────────────────────────────────────────────

func TestRepositoryDialectAware(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewRepository(db)
	if repo.dialect == nil {
		t.Error("expected non-nil dialect")
	}
	if repo.dialect.Dialect != dbdialect.SQLite {
		t.Logf("dialect is %v (may be postgres in CI)", repo.dialect.Dialect)
	}
}

// Helpers to ensure imports used
var (
	_ = json.Marshal
	_ = dbdialect.FromDriver("sqlite")
)
