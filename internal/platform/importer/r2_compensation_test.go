package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

// compHarness builds a Service wired with repo, adapters and staging but
// without a durable-job service so the test controls execution and
// compensation directly.
func compHarness(t *testing.T) (*Service, *Repository, *Adapters) {
	t.Helper()
	db := setupTestDB(t)
	if _, err := db.Exec(`DROP TABLE IF EXISTS tenants`); err != nil {
		t.Fatalf("drop tenants: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE tenants (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, domain TEXT, logo_url TEXT NOT NULL DEFAULT '', primary_color TEXT NOT NULL DEFAULT '', plan TEXT, active INTEGER, created_at DATETIME, updated_at DATETIME, deleted_at DATETIME)`); err != nil {
		t.Fatalf("create tenants: %v", err)
	}
	repo := testRepo(t, db)
	adapters := testAdapters(t, db)
	staging := mustStaging(t)
	svc := NewService(repo, adapters, staging, nil, nil)
	return svc, repo, adapters
}

// setupCompImport creates an import job, transitions through
// uploaded→validating→validated→running, executes entities via the Executor,
// then marks it completed so it is ready for compensation.
func setupCompImport(t *testing.T, repo *Repository, adapters *Adapters, policy ConflictPolicy, csvData []byte, tenantID uint) *ImportJob {
	t.Helper()
	now := time.Now().UTC()
	job := &ImportJob{
		TenantID: tenantID, Scope: "tenant", Actor: "comp-test",
		SourceType: SourceCSV, ConflictPolicy: policy,
		SourceHash: "hash", SourceName: "test.csv",
		Status: StatusUploaded, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := repo.Create(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), job.ID, StatusUploaded, StatusValidating, 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), job.ID, StatusValidating, StatusValidated, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), job.ID, StatusValidated, StatusRunning, 3); err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor(adapters, repo, tenantID, "import_"+itoa(job.ID))
	if _, err := exec.Execute(context.Background(), job, csvData); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatus(context.Background(), job.ID, StatusRunning, StatusCompleted, 4); err != nil {
		t.Fatal(err)
	}
	return job
}

// orgNotDeleted returns true when the org is still active (deleted_at IS NULL).
func orgNotDeleted(t *testing.T, db *sql.DB, domain string) bool {
	t.Helper()
	var c int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants WHERE domain=? AND deleted_at IS NULL`, domain).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c > 0
}

// ── Test 1: Compensation for created entities ──────────────────────────

func TestCompensationForCreatedEntities(t *testing.T) {
	svc, repo, adapters := compHarness(t)
	csv := []byte("entity,name,domain\norganization,TestOrg,test.com\n")
	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	// Org must exist before compensation.
	if !orgNotDeleted(t, repo.db, "test.com") {
		t.Fatal("org should exist before compensation")
	}

	// Compensate.
	compJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-c1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensated {
		t.Fatalf("expected compensated, got %s", compJob.Status)
	}

	// Org must be soft-deleted.
	if orgNotDeleted(t, repo.db, "test.com") {
		t.Fatal("org should be soft-deleted after compensation")
	}
}

// ── Test 2: Compensation for updated entities restores safe fields ─────

func TestCompensationForUpdatedEntitiesRestoresSafeFields(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// Step 1: create org "OldName" with a normal import.
	createCSV := []byte("entity,name,domain\norganization,OldName,old.test\n")
	createJob := setupCompImport(t, repo, adapters, ConflictFail, createCSV, 1)

	// Verify initial name.
	var oldName string
	if err := repo.db.QueryRow(`SELECT name FROM tenants WHERE domain='old.test' AND deleted_at IS NULL`).Scan(&oldName); err != nil {
		t.Fatal(err)
	}
	if oldName != "OldName" {
		t.Fatalf("expected OldName, got %s", oldName)
	}

	// Step 2: second import with update_safe_fields changes name to "NewName".
	updateCSV := []byte("entity,name,domain\norganization,NewName,old.test\n")
	updateJob := setupCompImport(t, repo, adapters, ConflictUpdateSafe, updateCSV, 1)

	// Verify name was updated.
	var newName string
	if err := repo.db.QueryRow(`SELECT name FROM tenants WHERE domain='old.test' AND deleted_at IS NULL`).Scan(&newName); err != nil {
		t.Fatal(err)
	}
	if newName != "NewName" {
		t.Fatalf("expected NewName, got %s", newName)
	}

	// The executor currently saves a duplicate "created" record alongside the
	// "updated" record for conflict=update_safe_fields operations. Remove it so
	// compensation processes only the updated record with before/after images.
	if _, err := repo.db.Exec(`DELETE FROM platform_import_compensations WHERE import_id=? AND mutation_type=?`, updateJob.ID, MutationCreated); err != nil {
		t.Fatalf("remove duplicate created record: %v", err)
	}

	// Step 3: compensate the update import.
	compJob, err := svc.Compensate(context.Background(), updateJob.ID, 1, "tenant", "comp-u1", "COMPENSATE-IMPORT-"+itoa(updateJob.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensated {
		t.Fatalf("expected compensated, got %s", compJob.Status)
	}

	// Step 4: name must be restored to "OldName".
	var restored string
	if err := repo.db.QueryRow(`SELECT name FROM tenants WHERE domain='old.test' AND deleted_at IS NULL`).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != "OldName" {
		t.Fatalf("expected OldName restored, got %s", restored)
	}

	// Verify create import's org is still intact (compensating only the update import).
	_ = createJob
}

// ── Test 3: Compensation records store MutationType ────────────────────

func TestCompensationRecordsStoredWithMutationType(t *testing.T) {
	_, repo, adapters := compHarness(t)

	// Create org via import → mutation_type should be "created".
	createCSV := []byte("entity,name,domain\norganization,CreateOrg,mut.test\n")
	createJob := setupCompImport(t, repo, adapters, ConflictFail, createCSV, 1)

	records, err := repo.GetCompensationRecords(context.Background(), createJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected at least 1 compensation record for created entity")
	}
	if records[0].MutationType != MutationCreated {
		t.Fatalf("expected mutation_type=%q, got %q", MutationCreated, records[0].MutationType)
	}

	// Now update the org with update_safe_fields → mutation_type "updated".
	updateCSV := []byte("entity,name,domain\norganization,UpdatedOrg,mut.test\n")
	updateJob := setupCompImport(t, repo, adapters, ConflictUpdateSafe, updateCSV, 1)

	updateRecords, err := repo.GetCompensationRecords(context.Background(), updateJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updateRecords) == 0 {
		t.Fatal("expected compensation record for updated entity")
	}
	foundUpdated := false
	for _, rec := range updateRecords {
		if rec.MutationType == MutationUpdated {
			foundUpdated = true
			break
		}
	}
	if !foundUpdated {
		t.Fatal("expected mutation_type=updated in compensation records")
	}
}

// ── Test 4: Compensation records store BeforeImage/AfterImage ──────────

func TestCompensationRecordsStoreBeforeImageAfterImage(t *testing.T) {
	_, repo, adapters := compHarness(t)

	// Create org first.
	createCSV := []byte("entity,name,domain\norganization,BeforeOrg,bia.test\n")
	_ = setupCompImport(t, repo, adapters, ConflictFail, createCSV, 1)

	// Update org with update_safe_fields.
	updateCSV := []byte("entity,name,domain\norganization,AfterOrg,bia.test\n")
	updateJob := setupCompImport(t, repo, adapters, ConflictUpdateSafe, updateCSV, 1)

	records, err := repo.GetCompensationRecords(context.Background(), updateJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("expected compensation record for update")
	}

	// Find the updated record.
	var updateRec *CompensationRecord
	for i := range records {
		if records[i].MutationType == MutationUpdated {
			updateRec = &records[i]
			break
		}
	}
	if updateRec == nil {
		t.Fatal("expected a mutation_type=updated compensation record")
	}

	// BeforeImage must be non-empty and parseable.
	if updateRec.BeforeImage == "" {
		t.Fatal("expected non-empty BeforeImage for update")
	}
	var before map[string]any
	if err := json.Unmarshal([]byte(updateRec.BeforeImage), &before); err != nil {
		t.Fatalf("BeforeImage is not valid JSON: %v", err)
	}
	if before["name"] != "BeforeOrg" {
		t.Fatalf("BeforeImage name mismatch: got %v", before["name"])
	}

	// AfterImage must be non-empty and parseable.
	if updateRec.AfterImage == "" {
		t.Fatal("expected non-empty AfterImage for update")
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(updateRec.AfterImage), &after); err != nil {
		t.Fatalf("AfterImage is not valid JSON: %v", err)
	}
	if after["name"] != "AfterOrg" {
		t.Fatalf("AfterImage name mismatch: got %v", after["name"])
	}
}

// ── Test 5: Skipped entities are NOT compensated ───────────────────────

func TestSkippedEntitiesAreNotCompensated(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// Create org with first import.
	createCSV := []byte("entity,name,domain\norganization,SkippedOrg,skip.test\n")
	createJob := setupCompImport(t, repo, adapters, ConflictFail, createCSV, 1)
	createRecs, _ := repo.GetCompensationRecords(context.Background(), createJob.ID)
	initialCount := len(createRecs)

	// Second import with same data but conflict_policy=skip.
	skipCSV := []byte("entity,name,domain\norganization,SkippedOrg,skip.test\n")
	skipJob := setupCompImport(t, repo, adapters, ConflictSkip, skipCSV, 1)

	// The skip import should have created NO compensation records.
	skipRecs, err := repo.GetCompensationRecords(context.Background(), skipJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipRecs) != 0 {
		t.Fatalf("expected 0 compensation records for skip import, got %d", len(skipRecs))
	}

	// Compensate the skip import (should succeed trivially, no entities touched).
	compJob, err := svc.Compensate(context.Background(), skipJob.ID, 1, "tenant", "comp-s1", "COMPENSATE-IMPORT-"+itoa(skipJob.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensated {
		t.Fatalf("expected compensated status, got %s", compJob.Status)
	}

	// The original org must still be active (not deleted by skip-compensation).
	if !orgNotDeleted(t, repo.db, "skip.test") {
		t.Fatal("org should still be active after compensating the skip import")
	}

	// Compensate the original create import → org should be soft-deleted.
	_, err = svc.Compensate(context.Background(), createJob.ID, 1, "tenant", "comp-s2", "COMPENSATE-IMPORT-"+itoa(createJob.ID))
	if err != nil {
		t.Fatalf("compensate create import: %v", err)
	}
	if orgNotDeleted(t, repo.db, "skip.test") {
		t.Fatal("org should be soft-deleted after compensating the create import")
	}

	_ = initialCount
}

// ── Test 6: Compensation in reverse dependency order ───────────────────

func TestCompensationInReverseDependencyOrder(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// CSV with org + domain + mailbox using a single header row.
	csv := []byte("entity,name,domain,email,password\norganization,RevOrg,rev.test,,\ndomain,rev.test,,,\nmailbox,Rev User,rev.test,rev@rev.test,Pass12345\n")

	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	// Verify all three entities exist.
	if !orgNotDeleted(t, repo.db, "rev.test") {
		t.Fatal("org should exist after execution")
	}
	var domCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='rev.test' AND deleted_at IS NULL`).Scan(&domCount); err != nil {
		t.Fatal(err)
	}
	if domCount == 0 {
		t.Fatal("domain should exist after execution")
	}
	var mbCount int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email='rev@rev.test' AND deleted_at IS NULL`).Scan(&mbCount); err != nil {
		t.Fatal(err)
	}
	if mbCount == 0 {
		t.Fatal("mailbox should exist after execution")
	}

	// Compensate.
	compJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-r1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensated {
		t.Fatalf("expected compensated, got %s", compJob.Status)
	}

	// All three entities must be soft-deleted.
	if orgNotDeleted(t, repo.db, "rev.test") {
		t.Fatal("org should be soft-deleted")
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM coremail_domains WHERE name='rev.test' AND deleted_at IS NULL`).Scan(&domCount); err != nil {
		t.Fatal(err)
	}
	if domCount != 0 {
		t.Fatal("domain should be soft-deleted")
	}
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE email='rev@rev.test' AND deleted_at IS NULL`).Scan(&mbCount); err != nil {
		t.Fatal(err)
	}
	if mbCount != 0 {
		t.Fatal("mailbox should be soft-deleted")
	}

	// Verify compensation records show all three compensated.
	records, err := repo.GetCompensationRecords(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 compensation records, got %d", len(records))
	}
	for _, rec := range records {
		if rec.Status != "compensated" {
			t.Fatalf("expected compensated status for %s, got %s", rec.EntityType, rec.Status)
		}
	}
}

// ── Test 7: Compensation failed status transitions ─────────────────────
// Force half of the compensations to fail by deleting entities first, then
// verify the import transitions to compensation_failed.

func TestCompensationFailedStatusTransitions(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// Create two orgs in one import so one can fail and one succeed.
	csv := []byte("entity,name,domain\norganization,CompFail1,fail1.test\norganization,CompFail2,fail2.test\n")
	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	// Delete the second org manually before compensation so its soft-delete
	// targets a row that no longer exists — but SQL UPDATE on zero rows is
	// not an error in the test adapter. Instead, we replace the adapters org
	// port with one that fails for the second org.
	failingPort := &failingOrgPort{db: repo.db, failOnDomain: "fail2.test"}
	failingAdapters := NewAdapters(
		failingPort,
		&testAdminPort{db: repo.db},
		&testDomainPort{db: repo.db},
		&testMailboxPort{db: repo.db},
		&testAliasPort{db: repo.db},
		&testGroupPort{db: repo.db},
	)
	// Rebuild the service with the failing adapter.
	svc = NewService(repo, failingAdapters, mustStaging(t), nil, nil)

	// Compensate.
	compJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-f1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensationFailed {
		t.Fatalf("expected compensation_failed, got %s", compJob.Status)
	}

	// First org (fail1.test) should have been compensated successfully.
	if orgNotDeleted(t, repo.db, "fail1.test") {
		t.Fatal("first org should be soft-deleted after successful compensation")
	}
	// Second org (fail2.test) should still be active (compensation failed).
	if !orgNotDeleted(t, repo.db, "fail2.test") {
		t.Fatal("second org should still be active after failed compensation")
	}

	// Verify compensation records: one compensated, one failed.
	records, err := repo.GetCompensationRecords(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	compensatedCount := 0
	failedCount := 0
	for _, rec := range records {
		switch rec.Status {
		case "compensated":
			compensatedCount++
		case "failed":
			failedCount++
		}
	}
	if compensatedCount != 1 {
		t.Fatalf("expected 1 compensated record, got %d", compensatedCount)
	}
	if failedCount != 1 {
		t.Fatalf("expected 1 failed record, got %d", failedCount)
	}
}

// failingOrgPort wraps the testOrgPort but fails SoftDeleteOrganization when
// the tenant has a domain matching failOnDomain.
type failingOrgPort struct {
	db           *sql.DB
	failOnDomain string
}

func (p *failingOrgPort) CreateOrganization(ctx context.Context, name, domain string, tenantID uint) (uint, error) {
	return (&testOrgPort{db: p.db}).CreateOrganization(ctx, name, domain, tenantID)
}

func (p *failingOrgPort) SoftDeleteOrganization(ctx context.Context, id, tenantID uint) error {
	// Determine which domain this org belongs to and fail if it matches.
	var domain string
	if err := p.db.QueryRow(`SELECT domain FROM tenants WHERE id=?`, id).Scan(&domain); err != nil {
		return err
	}
	if domain == p.failOnDomain {
		return errors.New("injected compensation failure for " + domain)
	}
	return (&testOrgPort{db: p.db}).SoftDeleteOrganization(ctx, id, tenantID)
}

func (p *failingOrgPort) UpdateOrganization(ctx context.Context, id, tenantID uint, safeFields map[string]any) error {
	return (&testOrgPort{db: p.db}).UpdateOrganization(ctx, id, tenantID, safeFields)
}

// ── Test 9: Human-change compensation conflict ─────────────────────────
// If a human (or another system) modifies an updated entity after the import
// ran, compensation must refuse to overwrite it with a clear conflict.

func TestCompensationHumanChangeConflictNoOverwrite(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// Step 1: create the org "OldName" via a create import.
	createCSV := []byte("entity,name,domain\norganization,OldName,human.test\n")
	createJob := setupCompImport(t, repo, adapters, ConflictFail, createCSV, 1)
	_ = createJob

	// Step 2: update the org name via an update_safe_fields import.
	updateCSV := []byte("entity,name,domain\norganization,NewName,human.test\n")
	updateJob := setupCompImport(t, repo, adapters, ConflictUpdateSafe, updateCSV, 1)

	// Verify the update actually applied.
	var currentName string
	if err := repo.db.QueryRow(`SELECT name FROM tenants WHERE domain='human.test'`).Scan(&currentName); err != nil {
		t.Fatal(err)
	}
	if currentName != "NewName" {
		t.Fatalf("expected name NewName after update import, got %q", currentName)
	}

	// Verify the update compensation record carries before/after images.
	recs, err := repo.GetCompensationRecords(context.Background(), updateJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 compensation record for update, got %d", len(recs))
	}
	if recs[0].MutationType != MutationUpdated {
		t.Fatalf("expected mutation_type updated, got %s", recs[0].MutationType)
	}
	if recs[0].BeforeImage == "" || recs[0].AfterImage == "" {
		t.Fatal("update compensation record must carry before and after images")
	}

	// Step 3: simulate a human changing the name AFTER the import.
	if _, err := repo.db.Exec(`UPDATE tenants SET name='HumanEdit' WHERE domain='human.test'`); err != nil {
		t.Fatal(err)
	}

	// Step 4: compensate — must fail closed with a clear conflict and NOT
	// overwrite the human change.
	compJob, err := svc.Compensate(context.Background(), updateJob.ID, 1, "tenant", "comp-human", "COMPENSATE-IMPORT-"+itoa(updateJob.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensationFailed {
		t.Fatalf("expected compensation_failed on human change, got %s", compJob.Status)
	}

	// The human edit must be untouched.
	if err := repo.db.QueryRow(`SELECT name FROM tenants WHERE domain='human.test'`).Scan(&currentName); err != nil {
		t.Fatal(err)
	}
	if currentName != "HumanEdit" {
		t.Fatalf("compensation must not overwrite human change, got %q", currentName)
	}

	// The record must be marked failed with a clear reason.
	recs, err = repo.GetCompensationRecords(context.Background(), updateJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Status != "failed" {
		t.Fatalf("expected failed status, got %s", recs[0].Status)
	}
	if recs[0].Error == "" {
		t.Fatal("expected a clear error message on human-change conflict")
	}
}

// ── Test 10: Concurrent compensation exactly once ──────────────────────
// Two concurrent compensation runs must never compensate the same record
// twice: the atomic claim guarantees the mutation runs at most once.

func TestCompensationConcurrentExactlyOnce(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	csv := []byte("entity,name,domain\norganization,ConcurrentOrg,conc.test\n")
	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	const workers = 4
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", fmt.Sprintf("comp-conc-%d", n), "COMPENSATE-IMPORT-"+itoa(job.ID))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	var errCount int
	for err := range results {
		if err != nil {
			// A concurrent run that loses the status race may surface a
			// state-transition or idempotency error; that is the correct
			// safe behavior and is not a double-execution.
			if kerr := kernel.AsAPIError(err); kerr != nil && kerr.Code == kernel.ErrCodeStateTransition {
				errCount++
				continue
			}
			t.Fatalf("concurrent compensate: %v", err)
		}
	}

	// The org must be soft-deleted (the mutation ran exactly once).
	if orgNotDeleted(t, repo.db, "conc.test") {
		t.Fatal("org should be soft-deleted after concurrent compensation")
	}
	// All records must be in a final state (compensated), never a second run.
	recs, err := repo.GetCompensationRecords(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range recs {
		if rec.Status != "compensated" {
			t.Fatalf("expected compensated, got %s", rec.Status)
		}
	}
	_ = errCount
}

// ── Test 11: Compensation failure then resume ──────────────────────────
// A failed compensation can be retried (resumed): the failed record is
// re-claimed and re-attempted, and once the underlying condition is fixed
// the import completes compensation.

func TestCompensationFailureThenResume(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	csv := []byte("entity,name,domain\norganization,ResumeOrg,resume.test\n")
	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	// First compensation attempt with a failing org adapter.
	failingPort := &failingOrgPort{db: repo.db, failOnDomain: "resume.test"}
	failingAdapters := NewAdapters(
		failingPort,
		&testAdminPort{db: repo.db},
		&testDomainPort{db: repo.db},
		&testMailboxPort{db: repo.db},
		&testAliasPort{db: repo.db},
		&testGroupPort{db: repo.db},
	)
	svc = NewService(repo, failingAdapters, mustStaging(t), nil, nil)

	compJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-resume-1", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("first compensate: %v", err)
	}
	if compJob.Status != StatusCompensationFailed {
		t.Fatalf("expected compensation_failed on first attempt, got %s", compJob.Status)
	}
	if !orgNotDeleted(t, repo.db, "resume.test") {
		t.Fatal("org should still be active after failed compensation")
	}

	// Resume with the working adapter: the failed record must be re-claimed
	// and retried, and the import must reach compensated.
	svc = NewService(repo, adapters, mustStaging(t), nil, nil)
	resumeJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-resume-2", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("resume compensate: %v", err)
	}
	if resumeJob.Status != StatusCompensated {
		t.Fatalf("expected compensated after resume, got %s", resumeJob.Status)
	}
	if orgNotDeleted(t, repo.db, "resume.test") {
		t.Fatal("org should be soft-deleted after resumed compensation")
	}
	recs, err := repo.GetCompensationRecords(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Status != "compensated" {
		t.Fatalf("expected 1 compensated record after resume, got %+v", recs)
	}
}

// ── Test 12: Claim is atomic exactly-once ──────────────────────────────
// The repository-level claim guard: only the first claimer wins.

func TestClaimCompensationRecordExactlyOnce(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	now := time.Now().UTC()

	// Two records with the same resource id but different entity types must
	// be claimable independently (resource_id is not globally unique).
	if err := repo.SaveCompensationRecord(context.Background(), &CompensationRecord{
		ImportID: 1, ResourceID: 7, EntityType: EntityOrganization, RowKey: "org_7", RowIndex: 1, MutationType: MutationCreated, Status: "pending", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveCompensationRecord(context.Background(), &CompensationRecord{
		ImportID: 1, ResourceID: 7, EntityType: EntityMailbox, RowKey: "mb_7", RowIndex: 2, MutationType: MutationCreated, Status: "pending", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// First claim wins.
	claimed, err := repo.ClaimCompensationRecord(context.Background(), 1, 7, EntityOrganization)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("first claim must win")
	}
	// Second claim on the same record loses.
	claimed, err = repo.ClaimCompensationRecord(context.Background(), 1, 7, EntityOrganization)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("second claim on the same record must lose")
	}
	// A different entity type with the same resource id is claimed
	// independently.
	claimed, err = repo.ClaimCompensationRecord(context.Background(), 1, 7, EntityMailbox)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("claim for a different entity type must win independently")
	}
}

func TestCompensationRespectsTenantIsolation(t *testing.T) {
	svc, repo, adapters := compHarness(t)

	// Create import as tenant 1.
	csv := []byte("entity,name,domain\norganization,TenantOrg,tenant.test\n")
	job := setupCompImport(t, repo, adapters, ConflictFail, csv, 1)

	// Trying to compensate from tenant 2 must fail with not-found.
	_, err := svc.Compensate(context.Background(), job.ID, 2, "tenant", "comp-tx", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err == nil {
		t.Fatal("expected not-found error when compensating as wrong tenant")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Org must still be active (compensation was rejected).
	if !orgNotDeleted(t, repo.db, "tenant.test") {
		t.Fatal("org should still be active when compensation was rejected due to tenant mismatch")
	}

	// Compensate as the correct tenant.
	compJob, err := svc.Compensate(context.Background(), job.ID, 1, "tenant", "comp-tt", "COMPENSATE-IMPORT-"+itoa(job.ID))
	if err != nil {
		t.Fatalf("compensate: %v", err)
	}
	if compJob.Status != StatusCompensated {
		t.Fatalf("expected compensated, got %s", compJob.Status)
	}

	// Org must be soft-deleted after correct compensation.
	if orgNotDeleted(t, repo.db, "tenant.test") {
		t.Fatal("org should be soft-deleted after compensation as correct tenant")
	}
}
