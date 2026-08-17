package organization

import (
	"context"
	"database/sql"
	"testing"

	"github.com/orvix/orvix/internal/audit"
	_ "modernc.org/sqlite"
)

// newPlatformDeletionTestDB extends newLifecycleTestDB's schema (see
// lifecycle_test.go) with domains/mailboxes tables so
// PlatformScheduleDeletion's dependency guard can be exercised.
func newPlatformDeletionTestDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, svc := newLifecycleTestDB(t)
	_, err := db.Exec(`
		CREATE TABLE domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL,
			domain TEXT NOT NULL, deleted_at DATETIME
		);
		CREATE TABLE mailboxes (
			id INTEGER PRIMARY KEY AUTOINCREMENT, tenant_id INTEGER NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1, deleted_at DATETIME
		);
	`)
	if err != nil {
		t.Fatal(err)
	}
	return db, svc
}

func TestPlatformScheduleDeletion_DependencyGuardBlocksWithDomains(t *testing.T) {
	db, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "blocked", Domain: "blocked.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domains (tenant_id, domain) VALUES (?, 'mail.blocked.test')`, org.ID); err != nil {
		t.Fatal(err)
	}

	_, err = svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "cleanup")
	if err == nil {
		t.Fatal("expected dependency guard to block deletion, got nil error")
	}
	var blocked *DeletionBlockedError
	if !asDeletionBlocked(err, &blocked) {
		t.Fatalf("expected *DeletionBlockedError, got %T: %v", err, err)
	}
	if len(blocked.Blockers) != 1 {
		t.Fatalf("blockers=%v, want exactly 1 (active domains)", blocked.Blockers)
	}

	var pending int
	db.QueryRow(`SELECT COUNT(*) FROM org_deletions WHERE organization_id = ?`, org.ID).Scan(&pending)
	if pending != 0 {
		t.Fatalf("blocked deletion should not have created an org_deletions row, got %d", pending)
	}
}

func TestPlatformScheduleDeletion_DependencyGuardBlocksWithMailboxes(t *testing.T) {
	db, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "blocked2", Domain: "blocked2.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO mailboxes (tenant_id, is_active) VALUES (?, 1)`, org.ID); err != nil {
		t.Fatal(err)
	}

	_, err = svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "cleanup")
	var blocked *DeletionBlockedError
	if !asDeletionBlocked(err, &blocked) {
		t.Fatalf("expected *DeletionBlockedError, got %T: %v", err, err)
	}
}

func TestPlatformScheduleDeletion_ZeroDependenciesSucceeds(t *testing.T) {
	db, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "clean", Domain: "clean.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	idempotent, err := svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "customer offboarded")
	if err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}
	if idempotent {
		t.Fatal("first call reported idempotent=true, want false")
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM org_deletions WHERE organization_id = ?`, org.ID).Scan(&state); err != nil {
		t.Fatalf("read org_deletions: %v", err)
	}
	if state != string(DeletionRequested) {
		t.Fatalf("state=%s, want %s", state, DeletionRequested)
	}
}

func TestPlatformScheduleDeletion_WritesAuditEvent(t *testing.T) {
	db, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "audited", Domain: "audited.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlatformScheduleDeletion(ctx, org.ID, 77, org.Domain, "policy violation"); err != nil {
		t.Fatalf("schedule deletion: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orvix_audit WHERE action = 'organization.deletion_scheduled' AND target_id = ?`, org.ID).Scan(&count); err != nil {
		t.Fatalf("query orvix_audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit events for deletion_scheduled = %d, want 1", count)
	}
}

func TestPlatformScheduleDeletion_DoubleCallIsIdempotent(t *testing.T) {
	db, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "twice", Domain: "twice.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "first call"); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	idempotent, err := svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "second call")
	if err != nil {
		t.Fatalf("second schedule: %v", err)
	}
	if !idempotent {
		t.Fatal("second call reported idempotent=false, want true")
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM org_deletions WHERE organization_id = ?`, org.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("org_deletions rows=%d, want exactly 1 after double-call", count)
	}
}

func TestPlatformScheduleDeletion_ConfirmationMismatchRejected(t *testing.T) {
	_, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "mismatch", Domain: "mismatch.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlatformScheduleDeletion(ctx, org.ID, 42, "wrong.domain", "reason"); err != ErrDeletionConfirmationMismatch {
		t.Fatalf("err=%v, want ErrDeletionConfirmationMismatch", err)
	}
}

func TestPlatformScheduleDeletion_ReasonRequired(t *testing.T) {
	_, svc := newPlatformDeletionTestDB(t)
	ctx := context.Background()
	org, err := svc.CreateOrganization(ctx, CreateOrganizationRequest{Slug: "noreason", Domain: "noreason.test"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PlatformScheduleDeletion(ctx, org.ID, 42, org.Domain, "   "); err != ErrDeletionReasonRequired {
		t.Fatalf("err=%v, want ErrDeletionReasonRequired", err)
	}
}

func asDeletionBlocked(err error, target **DeletionBlockedError) bool {
	if b, ok := err.(*DeletionBlockedError); ok {
		*target = b
		return true
	}
	return false
}

var _ = audit.ExtendedEntry{} // keep audit import if unused elsewhere
