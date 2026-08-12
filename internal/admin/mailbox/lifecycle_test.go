package mailbox

import (
	"context"
	"testing"
)

func createTestMailbox(t *testing.T, svc *Service, tenantID uint, email string) *AdminMailbox {
	t.Helper()
	resp, err := svc.CreateMailbox(context.Background(), CreateMailboxRequest{
		Email: email, Password: "InitialPassword123!",
	}, tenantID)
	if err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	return &resp.Mailbox
}

func TestSoftDeleteMailbox_TransitionsToDeletedAndHidesFromList(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "todelete@example.test")

	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "no longer needed"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if _, err := svc.GetMailbox(ctx, m.ID, 10); err != ErrMailboxNotFound {
		t.Fatalf("expected soft-deleted mailbox to be hidden from GetMailbox, got %v", err)
	}

	var status string
	var deletedAt any
	if err := db.QueryRow(`SELECT status, deleted_at FROM coremail_mailboxes WHERE id = ?`, m.ID).Scan(&status, &deletedAt); err != nil {
		t.Fatal(err)
	}
	if status != string(AdminMailboxDeleted) {
		t.Fatalf("status = %q, want deleted", status)
	}
	if deletedAt == nil {
		t.Fatal("expected deleted_at to be stamped")
	}
}

func TestSoftDeleteMailbox_AlreadyDeletedIsRejected(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "twice@example.test")

	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "first"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "second")
	if err == nil {
		t.Fatal("expected the second delete to fail — deleted is a terminal status for plain transitions")
	}
}

func TestRestoreMailbox_ReactivatesAndIsVisibleAgain(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "restoreme@example.test")

	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "oops"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	restored, err := svc.RestoreMailbox(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Status != AdminMailboxActive {
		t.Fatalf("restored status = %q, want active", restored.Status)
	}

	got, err := svc.GetMailbox(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}
	if got.Email != "restoreme@example.test" {
		t.Fatalf("unexpected restored mailbox: %#v", got)
	}
}

func TestRestoreMailbox_NotDeletedIsRejected(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "active@example.test")

	if _, err := svc.RestoreMailbox(ctx, m.ID, 10); err != ErrMailboxNotDeleted {
		t.Fatalf("expected ErrMailboxNotDeleted, got %v", err)
	}
}

// TestRestoreMailbox_EmailConflictIsRejected proves the restore path
// re-checks uniqueness rather than blindly reviving a row: if another
// mailbox has since taken the same address, restore must fail instead
// of creating a duplicate-address collision.
func TestRestoreMailbox_EmailConflictIsRejected(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "taken@example.test")
	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "reassigning address"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// A new mailbox now legitimately holds the same address.
	createTestMailbox(t, svc, 10, "taken@example.test")

	if _, err := svc.RestoreMailbox(ctx, m.ID, 10); err != ErrMailboxEmailConflict {
		t.Fatalf("expected ErrMailboxEmailConflict, got %v", err)
	}
}

func TestPurgeMailbox_RemovesRowPermanently(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "purgeme@example.test")
	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "cleanup"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if err := svc.PurgeMailbox(ctx, m.ID, 10, "gdpr request"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE id = ?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("expected the row to be permanently removed after purge")
	}
}

func TestPurgeMailbox_RefusesLiveMailbox(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "live@example.test")

	if err := svc.PurgeMailbox(ctx, m.ID, 10, "should not work"); err != ErrMailboxNotDeleted {
		t.Fatalf("expected ErrMailboxNotDeleted, got %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM coremail_mailboxes WHERE id = ?`, m.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("a live mailbox must never be purged")
	}
}

func TestPurgeMailbox_CrossTenantIsNotFound(t *testing.T) {
	db := newMailboxTestDB(t)
	svc := NewService(NewAdminMailboxRepo(db), newTestHasher(), nil, nil)
	ctx := context.Background()
	seedDomain(t, db, 10, "example.test", "active", false)
	m := createTestMailbox(t, svc, 10, "isolated@example.test")
	if err := svc.SoftDeleteMailbox(ctx, m.ID, 10, "x"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	if err := svc.PurgeMailbox(ctx, m.ID, 99, "wrong tenant"); err != ErrMailboxNotFound {
		t.Fatalf("expected ErrMailboxNotFound for a different tenant, got %v", err)
	}
}
