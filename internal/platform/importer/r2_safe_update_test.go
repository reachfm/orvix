package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// setupTestDBForExec extends the minimal test schema with columns that
// Repository.GetOrg and Repository.GetUser require for their COALESCE
// lookups (logo_url, primary_color, full_name).
func setupTestDBForExec(t *testing.T) *sql.DB {
	t.Helper()
	db := setupTestDB(t)
	// Repository.GetOrg queries COALESCE(logo_url,'') and COALESCE(primary_color,'').
	db.Exec(`ALTER TABLE tenants ADD COLUMN logo_url TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE tenants ADD COLUMN primary_color TEXT NOT NULL DEFAULT ''`)
	// Repository.GetUser queries COALESCE(full_name,'').
	db.Exec(`ALTER TABLE users ADD COLUMN full_name TEXT NOT NULL DEFAULT ''`)
	return db
}

// ── Dry-run tests (Validator / Planner path) ─────────────────────────

// TestDryRunUpdateSafeFields_RowUpdated verifies that when a group
// (whose name is both the lookup key and a safe field) gets a different
// description via update_safe_fields, the validator returns RowUpdated
// with proper BeforeImage/AfterImage diff images.
func TestDryRunUpdateSafeFields_RowUpdated(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	adapters := testAdapters(t, db)

	_, err := adapters.Group.CreateGroup(context.Background(), "Admins", "Old description", 1)
	if err != nil {
		t.Fatal(err)
	}

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)

	csv := []byte("entity,name,description\ngroup,Admins,New description\n")
	source, err := ParseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}

	report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
	if err != nil {
		t.Fatal(err)
	}

	if report.Total != 1 {
		t.Fatalf("expected 1 row, got %d", report.Total)
	}
	if report.Updated != 1 {
		t.Errorf("expected Updated=1, got %d", report.Updated)
	}

	row := report.Rows[0]
	if row.Status != RowUpdated {
		t.Errorf("expected RowUpdated, got %s", row.Status)
	}
	if len(row.BeforeImage) == 0 {
		t.Error("expected non-empty BeforeImage")
	}
	if len(row.AfterImage) == 0 {
		t.Error("expected non-empty AfterImage")
	}
}

// TestDryRunUpdateSafeFields_RowSkipped verifies that when a group is
// submitted with the same name and description via update_safe_fields
// the validator returns RowSkipped (nothing changed).
func TestDryRunUpdateSafeFields_RowSkipped(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	adapters := testAdapters(t, db)

	_, err := adapters.Group.CreateGroup(context.Background(), "Admins", "Old description", 1)
	if err != nil {
		t.Fatal(err)
	}

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)

	csv := []byte("entity,name,description\ngroup,Admins,Old description\n")
	source, err := ParseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}

	report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
	if err != nil {
		t.Fatal(err)
	}

	if report.Total != 1 {
		t.Fatalf("expected 1 row, got %d", report.Total)
	}
	if report.Unchanged != 1 {
		t.Errorf("expected Unchanged=1, got %d", report.Unchanged)
	}

	row := report.Rows[0]
	if row.Status != RowSkipped {
		t.Errorf("expected RowSkipped, got %s", row.Status)
	}
}

// TestDryRunUpdateSafeFields_ForbiddenField verifies that when an
// organization CSV contains a non-safe field like "plan" under
// update_safe_fields, the validator returns RowConflict with a
// FORBIDDEN_FIELD error.
func TestDryRunUpdateSafeFields_ForbiddenField(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	adapters := testAdapters(t, db)

	_, err := adapters.Org.CreateOrganization(context.Background(), "Acme", "acme.test", 1)
	if err != nil {
		t.Fatal(err)
	}

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)

	// "plan" and "domain" are not safe fields for org; the validator must flag them.
	csv := []byte("entity,name,domain,plan\norganization,Acme,acme.test,premium\n")
	source, err := ParseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}

	report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
	if err != nil {
		t.Fatal(err)
	}

	row := report.Rows[0]
	if row.Status != RowConflict {
		t.Errorf("expected RowConflict, got %s", row.Status)
	}
	found := false
	for _, e := range row.Errors {
		if e.Code == string(CodeForbiddenField) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected FORBIDDEN_FIELD error, got %+v", row.Errors)
	}
}

// TestDryRunUpdateSafeFields_AliasNoSafeFields verifies that when an
// alias CSV is processed with update_safe_fields and the alias already
// exists (as a mailbox), the validator returns RowConflict because
// aliases have no safe fields.
func TestDryRunUpdateSafeFields_AliasNoSafeFields(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	adapters := testAdapters(t, db)

	_, err := adapters.Domain.CreateDomain(context.Background(), "example.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapters.Mailbox.CreateMailbox(context.Background(), "user@example.com", "User", "secretpass1!", "example.com", 1)
	if err != nil {
		t.Fatal(err)
	}

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)

	csv := []byte("entity,from_addr,to_addr\nalias,user@example.com,other@example.com\n")
	source, err := ParseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}

	report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
	if err != nil {
		t.Fatal(err)
	}

	row := report.Rows[0]
	if row.Status != RowConflict {
		t.Errorf("expected RowConflict for alias (no safe fields), got %s", row.Status)
	}
	foundForbidden := false
	for _, e := range row.Errors {
		if e.Code == string(CodeForbiddenField) {
			foundForbidden = true
			break
		}
	}
	if !foundForbidden {
		t.Errorf("expected FORBIDDEN_FIELD error, got %+v", row.Errors)
	}
}

// ── Executor tests (actual mutation path) ─────────────────────────────

// TestExecutorUpdateSafeFields_OrgUpdatesName verifies that the executor
// actually applies safe-field updates for organizations when
// conflict_policy=update_safe_fields.
func TestExecutorUpdateSafeFields_OrgUpdatesName(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Org.CreateOrganization(context.Background(), "OldName", "upd.test", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-org")
	job := &ImportJob{
		ID:             100,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	data := []byte("entity,name,domain\norganization,NewName,upd.test\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}

	var got string
	if err := db.QueryRow(`SELECT name FROM tenants WHERE domain='upd.test' AND deleted_at IS NULL`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "NewName" {
		t.Errorf("expected org name 'NewName', got %q", got)
	}
}

// TestExecutorUpdateSafeFields_SkipsWhenNothingChanged verifies that
// when the CSV matches the existing entity exactly, the executor skips
// the row and the org remains unchanged.
func TestExecutorUpdateSafeFields_SkipsWhenNothingChanged(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Org.CreateOrganization(context.Background(), "Acme", "acme.test", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-skip")
	job := &ImportJob{
		ID:             101,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	data := []byte("entity,name,domain\norganization,Acme,acme.test\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded > 0 {
		t.Errorf("expected 0 successes (nothing changed), got %d", result.Succeeded)
	}

	var got string
	if err := db.QueryRow(`SELECT name FROM tenants WHERE domain='acme.test' AND deleted_at IS NULL`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "Acme" {
		t.Errorf("expected org name 'Acme' unchanged, got %q", got)
	}
}

// TestExecutorUpdateSafeFields_FailsClosedOnForbiddenField verifies that
// when a CSV tries to change a non-safe field (plan) for an existing org
// under update_safe_fields, the executor does NOT apply the forbidden
// change. The plan remains unchanged because only safe fields are
// considered for updates.
func TestExecutorUpdateSafeFields_FailsClosedOnForbiddenField(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Org.CreateOrganization(context.Background(), "Acme", "acme.test", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-forbid")
	job := &ImportJob{
		ID:             102,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	// plan is a forbidden field — the executor must ignore it.
	data := []byte("entity,name,domain,plan\norganization,Acme,acme.test,premium\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing safe changed → no success. The plan change is silently dropped.
	if result.Succeeded > 0 {
		t.Errorf("expected 0 successes (forbidden field only), got %d", result.Succeeded)
	}

	var plan string
	if err := db.QueryRow(`SELECT plan FROM tenants WHERE domain='acme.test' AND deleted_at IS NULL`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan != "free" {
		t.Errorf("expected plan to remain 'free', got %q", plan)
	}
}

// TestExecutorUpdateSafeFields_MailboxUpdatesName verifies that a mailbox
// name is updated when conflict_policy=update_safe_fields and the name
// differs from the existing mailbox.
func TestExecutorUpdateSafeFields_MailboxUpdatesName(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Domain.CreateDomain(context.Background(), "mbx.test", 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapters.Mailbox.CreateMailbox(context.Background(), "alice@mbx.test", "Alice Old", "secret123!", "mbx.test", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-mbx")
	job := &ImportJob{
		ID:             103,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	data := []byte("entity,name,email,password,domain\nmailbox,Alice New,alice@mbx.test,secret123!,mbx.test\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}

	var got string
	if err := db.QueryRow(`SELECT name FROM coremail_mailboxes WHERE email='alice@mbx.test' AND deleted_at IS NULL`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "Alice New" {
		t.Errorf("expected mailbox name 'Alice New', got %q", got)
	}
}

// TestExecutorUpdateSafeFields_GroupUpdatesNameAndDescription verifies
// that a group's name and description are updated when
// conflict_policy=update_safe_fields.
func TestExecutorUpdateSafeFields_GroupUpdatesNameAndDescription(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Group.CreateGroup(context.Background(), "Devs", "Dev Team", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-grp")
	job := &ImportJob{
		ID:             104,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	data := []byte("entity,name,description\ngroup,Devs,Engineering Team\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}

	var desc string
	if err := db.QueryRow(`SELECT description FROM coremail_groups WHERE name='Devs' AND tenant_id=1 AND deleted_at IS NULL`).Scan(&desc); err != nil {
		t.Fatal(err)
	}
	if desc != "Engineering Team" {
		t.Errorf("expected group description 'Engineering Team', got %q", desc)
	}
}

// TestExecutorUpdateSafeFields_AdminUpdatesName verifies that a tenant
// admin's name is updated when conflict_policy=update_safe_fields.
func TestExecutorUpdateSafeFields_AdminUpdatesName(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)

	_, err := adapters.Admin.CreateTenantAdmin(context.Background(), "admin@test.org", "Old Admin", "secret123!", "tenant_admin", 1)
	if err != nil {
		t.Fatal(err)
	}

	exec := NewExecutor(adapters, repo, 1, "key-upd-admin")
	job := &ImportJob{
		ID:             105,
		TenantID:       1,
		Scope:          "tenant",
		SourceType:     SourceCSV,
		ConflictPolicy: ConflictUpdateSafe,
		Status:         StatusRunning,
		CreatedAt:      time.Now(), UpdatedAt: time.Now(), Version: 1,
	}

	data := []byte("entity,name,email,password\ntenant_admin,New Admin,admin@test.org,secret123!\n")
	result, err := exec.Execute(context.Background(), job, data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Succeeded < 1 {
		t.Errorf("expected at least 1 success, got %d", result.Succeeded)
	}

	var got string
	if err := db.QueryRow(`SELECT name FROM users WHERE email='admin@test.org' AND deleted_at IS NULL`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "New Admin" {
		t.Errorf("expected admin name 'New Admin', got %q", got)
	}
}

// TestDryRunUpdateSafeFields_ZeroMutation proves the dry-run validator
// performs NO writes: it only reads existing state to produce before/after
// diffs. Row counts for every entity table must be unchanged afterwards.
func TestDryRunUpdateSafeFields_ZeroMutation(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	adapters := testAdapters(t, db)

	// Pre-seed entities the dry-run will report as updates.
	if _, err := adapters.Org.CreateOrganization(context.Background(), "OldOrg", "zero.test", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := adapters.Domain.CreateDomain(context.Background(), "zero.test", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := adapters.Group.CreateGroup(context.Background(), "ZeroGrp", "old", 1); err != nil {
		t.Fatal(err)
	}

	count := func(table string) int {
		t.Helper()
		var c int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	beforeOrgs := count("tenants")
	beforeDomains := count("coremail_domains")
	beforeGroups := count("coremail_groups")

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)

	// Each entity type uses its own header so no row carries a column that is
	// forbidden for it (a shared header would make the org row carry
	// "description", which is forbidden for orgs).
	updated := 0
	for _, csv := range [][]byte{
		[]byte("entity,name,domain\norganization,NewOrg,zero.test\n"),
		[]byte("entity,name,description\ngroup,ZeroGrp,Engineering Team\n"),
		[]byte("entity,name\ndomain,zero.test\n"),
	} {
		source, err := ParseCSV(csv)
		if err != nil {
			t.Fatal(err)
		}
		report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
		if err != nil {
			t.Fatal(err)
		}
		updated += report.Updated
	}
	if updated != 2 {
		t.Fatalf("expected 2 updates (org name, group description), got %d", updated)
	}

	// Dry-run must not have mutated anything.
	if got := count("tenants"); got != beforeOrgs {
		t.Fatalf("dry-run mutated tenants: %d -> %d", beforeOrgs, got)
	}
	if got := count("coremail_domains"); got != beforeDomains {
		t.Fatalf("dry-run mutated domains: %d -> %d", beforeDomains, got)
	}
	if got := count("coremail_groups"); got != beforeGroups {
		t.Fatalf("dry-run mutated groups: %d -> %d", beforeGroups, got)
	}
}

// TestReportRedactsSensitiveData proves the dry-run report never leaks
// secret-like field values in SafeData/BeforeImage/AfterImage.
func TestReportRedactsSensitiveData(t *testing.T) {
	t.Parallel()
	db := setupTestDBForExec(t)
	adapters := testAdapters(t, db)

	// Seed an admin with a name; the update row will carry name + a
	// secret-looking forbidden field (token) that must be redacted.
	if _, err := adapters.Admin.CreateTenantAdmin(context.Background(), "redact@test.org", "Old Admin", "secret123!", "tenant_admin", 1); err != nil {
		t.Fatal(err)
	}

	lookup := &testLookup{db}
	planner := NewPlanner(lookup, 1, adapters)
	// The token field is forbidden for tenant_admin; its value must never
	// appear in the report.
	csv := []byte("entity,name,email,password,token\ntenant_admin,New Admin,redact@test.org,secret123!,SENSITIVE_TOKEN_12345\n")
	source, err := ParseCSV(csv)
	if err != nil {
		t.Fatal(err)
	}
	report, err := planner.DryRun(context.Background(), source, ConflictUpdateSafe)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SENSITIVE_TOKEN_12345") {
		t.Fatal("report leaked a forbidden token value")
	}
	if strings.Contains(string(raw), "secret123!") {
		t.Fatal("report leaked a password value")
	}
}
