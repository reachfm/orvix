package supportaccess

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestService(t *testing.T) (*Service, context.Context) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_txlock=immediate")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	repo := NewRepository(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return NewService(repo), ctx
}

func TestSupportAccess_FullLifecycle(t *testing.T) {
	svc, ctx := newTestService(t)
	g, err := svc.RequestGrant(ctx, "TICKET-1", "investigating issue", 1, 100, "read_only", 4*time.Hour, false)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if g.Status != StatusRequested {
		t.Fatalf("expected requested, got %s", g.Status)
	}
	if _, err := svc.ApproveGrant(ctx, g.ID, 100); err != nil {
		t.Fatalf("approve: %v", err)
	}
	g2, err := svc.ActivateGrant(ctx, g.ID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if g2.Status != StatusActive {
		t.Fatalf("expected active, got %s", g2.Status)
	}
	if err := svc.ValidateAccess(ctx, 1); err != nil {
		t.Fatalf("validate active: %v", err)
	}
	if _, err := svc.RevokeGrant(ctx, g.ID, "done"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.ValidateAccess(ctx, 1); err == nil {
		t.Fatal("expected error after revocation")
	}
}

func TestSupportAccess_CrossTenantDenied(t *testing.T) {
	svc, ctx := newTestService(t)
	g, _ := svc.RequestGrant(ctx, "TICKET-1", "investigating", 1, 100, "read_only", 4*time.Hour, false)
	_, _ = svc.ApproveGrant(ctx, g.ID, 100)
	_, _ = svc.ActivateGrant(ctx, g.ID)
	if err := svc.ValidateAccess(ctx, 2); err == nil {
		t.Fatal("expected cross-tenant access to be denied")
	}
}

func TestSupportAccess_ExpiredDenied(t *testing.T) {
	svc, ctx := newTestService(t)
	g, _ := svc.RequestGrant(ctx, "TICKET-1", "investigating", 1, 100, "read_only", 1*time.Nanosecond, false)
	_, _ = svc.ApproveGrant(ctx, g.ID, 100)
	time.Sleep(50 * time.Millisecond)
	if _, err := svc.ActivateGrant(ctx, g.ID); err == nil {
		t.Fatal("expected expired grant to be rejected")
	}
}

func TestSupportAccess_InvalidScope(t *testing.T) {
	svc, ctx := newTestService(t)
	if _, err := svc.RequestGrant(ctx, "TICKET", "reason", 1, 100, "invalid_scope", time.Hour, false); err == nil {
		t.Fatal("expected invalid scope to be rejected")
	}
}

func TestSupportAccess_DuplicateActiveGrant(t *testing.T) {
	svc, ctx := newTestService(t)
	g, _ := svc.RequestGrant(ctx, "T1", "r", 1, 100, "read_only", time.Hour, false)
	_, _ = svc.ApproveGrant(ctx, g.ID, 100)
	_, _ = svc.ActivateGrant(ctx, g.ID)
	// Now try to create a second grant for the same tenant
	if _, err := svc.RequestGrant(ctx, "T2", "r", 1, 100, "read_only", time.Hour, false); err == nil {
		t.Fatal("expected duplicate active grant to be rejected")
	}
}

func TestSupportAccess_PendingGrantDenied(t *testing.T) {
	svc, ctx := newTestService(t)
	g, _ := svc.RequestGrant(ctx, "TICKET", "reason", 1, 100, "read_only", time.Hour, false)
	// Try to activate a pending (not approved) grant
	if _, err := svc.ActivateGrant(ctx, g.ID); err == nil {
		t.Fatal("expected pending grant to be rejected for activation")
	}
}
