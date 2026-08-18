package support_test

// Focused diagnostic test for the FINAL-ENTERPRISE-COMPLETION
// Support round-trip invariant:
//
//	TenantAdmin creates Ticket X
//	  → DB persists Ticket X
//	  → response returns Ticket X
//	  → TenantAdmin can read own Ticket X
//	  → Platform Support Inbox reads SAME Ticket X
//	  → Tenant B cannot see Tenant A's Ticket X
//
// The repository must:
//   1. Create a ticket scoped to (tenantA, userA)
//   2. Look it up by reference id scoped to tenantA
//   3. Return ErrTicketNotFound when looked up scoped to tenantB
//   4. Return the same ticket (no scope) for the Platform Support
//      Inbox
//   5. Append a tenant reply and drive the canonical status
//      transition (open → customer_replied)
//   6. Append a platform reply and drive the canonical transition
//      (customer_replied → in_progress)
//   7. Reject status transition from "closed" (terminal)

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/support"
)

func newRepo(t *testing.T) (*support.Repository, *sql.DB) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "support.db") + "?_loc=auto&_busy_timeout=5000&_journal_mode=WAL"
	cfg.Auth.JWTSecret = "test-secret-64-bytes-min-support-round-trip-fixture-XXXX"
	logger := zap.NewNop()
	gdb, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if err := models.MigrateAllRaw(gdb); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return support.NewRepository(sqlDB, gdb), sqlDB
}

func seedTenants(t *testing.T, sqlDB *sql.DB) (tenantA, tenantB uint) {
	t.Helper()
	now := time.Now().UTC()
	for i, name := range []string{"a.example", "b.example"} {
		res, err := sqlDB.Exec(
			`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active)
			 VALUES (?, ?, ?, ?, ?, 'smb', 1)`,
			now, now, name, name, name,
		)
		if err != nil {
			t.Fatalf("seed tenant %s: %v", name, err)
		}
		id, _ := res.LastInsertId()
		if i == 0 {
			tenantA = uint(id)
		} else {
			tenantB = uint(id)
		}
	}
	return
}

func TestSupportRoundTripInvariant(t *testing.T) {
	repo, sqlDB := newRepo(t)
	tenantA, tenantB := seedTenants(t, sqlDB)

	ctx := context.Background()

	// 1. TenantAdmin of tenantA creates Ticket X.
	ticket, err := repo.CreateTicket(ctx, support.CreateTicketInput{
		TenantID:    tenantA,
		UserID:      42,
		UserEmail:   "alice@a.example",
		Category:    "general",
		Subject:     "Cannot send",
		Description: "Outbound fails on port 587.",
		Priority:    "high",
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if ticket.ID == 0 {
		t.Fatal("ticket id is 0")
	}
	if ticket.ReferenceID == "" {
		t.Fatal("ticket reference_id is empty")
	}
	if ticket.Status != models.SupportTicketStatusOpen {
		t.Fatalf("ticket status=%q, want open", ticket.Status)
	}

	// 2. TenantAdmin of tenantA reads own Ticket X by reference id
	// (tenant-scoped lookup).
	got, err := repo.GetTicketByReference(ctx, ticket.ReferenceID, tenantA)
	if err != nil {
		t.Fatalf("tenantA read: %v", err)
	}
	if got.ID != ticket.ID {
		t.Fatalf("tenantA read: id=%d, want %d", got.ID, ticket.ID)
	}

	// 3. Tenant B (cross-tenant) is denied.
	_, err = repo.GetTicketByReference(ctx, ticket.ReferenceID, tenantB)
	if err == nil {
		t.Fatal("tenantB read: expected ErrTicketNotFound, got nil")
	}

	// 4. Platform Support Inbox reads the SAME Ticket X (no scope).
	platformView, err := repo.GetTicketByReference(ctx, ticket.ReferenceID, 0)
	if err != nil {
		t.Fatalf("platform read: %v", err)
	}
	if platformView.ID != ticket.ID || platformView.ReferenceID != ticket.ReferenceID {
		t.Fatalf("platform read: id/ref mismatch (%d/%s) vs (%d/%s)",
			platformView.ID, platformView.ReferenceID,
			ticket.ID, ticket.ReferenceID)
	}
	if platformView.TenantID != tenantA {
		t.Fatalf("platform read: tenant_id=%d, want %d", platformView.TenantID, tenantA)
	}

	// 5. TenantAdmin replies. Status transitions open → customer_replied.
	tenantMsg, err := repo.AddReply(ctx, support.ReplyInput{
		TicketID:     ticket.ID,
		AuthorUserID: 42,
		AuthorEmail:  "alice@a.example",
		AuthorKind:   "tenant",
		Body:         "Logs say SMTP 587 refused.",
	})
	if err != nil {
		t.Fatalf("tenant reply: %v", err)
	}
	if tenantMsg.ID == 0 {
		t.Fatal("tenant reply id is 0")
	}
	updated, err := repo.GetTicketByID(ctx, ticket.ID, tenantA)
	if err != nil {
		t.Fatalf("read after tenant reply: %v", err)
	}
	if updated.Status != models.SupportTicketStatusCustomerReplied {
		t.Fatalf("status after tenant reply: %q, want customer_replied", updated.Status)
	}

	// 6. Platform replies. Status transitions customer_replied → in_progress.
	_, err = repo.AddReply(ctx, support.ReplyInput{
		TicketID:     ticket.ID,
		AuthorUserID: 1,
		AuthorEmail:  "psa@orvix.email",
		AuthorKind:   "platform",
		Body:         "We see the same on our side; investigating.",
	})
	if err != nil {
		t.Fatalf("platform reply: %v", err)
	}
	updated2, err := repo.GetTicketByID(ctx, ticket.ID, 0)
	if err != nil {
		t.Fatalf("read after platform reply: %v", err)
	}
	if updated2.Status != models.SupportTicketStatusInProgress {
		t.Fatalf("status after platform reply: %q, want in_progress", updated2.Status)
	}

	// 7. Status transitions: in_progress → resolved → closed (terminal).
	resolved, err := repo.UpdateTicketStatus(ctx, ticket.ID, 0, models.SupportTicketStatusResolved)
	if err != nil {
		t.Fatalf("update → resolved: %v", err)
	}
	if resolved.Status != models.SupportTicketStatusResolved {
		t.Fatalf("status after resolve: %q, want resolved", resolved.Status)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved_at not set")
	}
	closed, err := repo.UpdateTicketStatus(ctx, ticket.ID, 0, models.SupportTicketStatusClosed)
	if err != nil {
		t.Fatalf("update → closed: %v", err)
	}
	if closed.Status != models.SupportTicketStatusClosed {
		t.Fatalf("status after close: %q, want closed", closed.Status)
	}
	if closed.ClosedAt == nil {
		t.Fatal("closed_at not set")
	}

	// 8. Closed is terminal: any further transition fails with
	// ErrInvalidTransition.
	_, err = repo.UpdateTicketStatus(ctx, ticket.ID, 0, models.SupportTicketStatusInProgress)
	if err == nil {
		t.Fatal("update from closed: expected ErrInvalidTransition, got nil")
	}

	// 9. ListMessages returns the reply thread in order.
	msgs, err := repo.ListMessages(ctx, ticket.ID, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages: len=%d, want 2", len(msgs))
	}
	if msgs[0].AuthorKind != "tenant" || msgs[1].AuthorKind != "platform" {
		t.Fatalf("messages: order [0]=%s [1]=%s, want tenant,platform",
			msgs[0].AuthorKind, msgs[1].AuthorKind)
	}

	// 10. Tenant B cannot enumerate tenant A's messages.
	_, err = repo.ListMessages(ctx, ticket.ID, tenantB)
	if err == nil {
		t.Fatal("tenantB list messages: expected ErrTicketNotFound, got nil")
	}
}
