package compliance

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/comp_test.db", t.TempDir()))
	if err != nil { t.Fatalf("open db: %v", err) }
	t.Cleanup(func() { db.Close() })
	svc := NewService(db)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return svc
}

func TestCreatePolicy(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	p := &Policy{Name: "Block Spam", Enabled: true, Action: ActionReject, Scope: ScopeSender, Value: "spam@test.com"}
	if err := svc.CreatePolicy(ctx, p); err != nil { t.Fatalf("create: %v", err) }
	if p.ID == 0 { t.Fatal("expected non-zero ID") }
}

func TestListPolicies(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.CreatePolicy(ctx, &Policy{Name: "P1", Action: ActionReject, Scope: ScopeSender, Value: "a@b.com"})
	svc.CreatePolicy(ctx, &Policy{Name: "P2", Action: ActionQuarantine, Scope: ScopeDomain, Value: "bad.com"})
	policies, err := svc.ListPolicies(ctx)
	if err != nil { t.Fatalf("list: %v", err) }
	if len(policies) != 2 { t.Fatalf("expected 2, got %d", len(policies)) }
}

func TestUpdatePolicy(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	p := &Policy{Name: "Original", Action: ActionReject, Scope: ScopeSender, Value: "old@test.com"}
	svc.CreatePolicy(ctx, p)
	if err := svc.UpdatePolicy(ctx, p.ID, &Policy{Name: "Updated", Action: ActionQuarantine, Scope: ScopeSender, Value: "new@test.com"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, _ := svc.GetPolicy(ctx, p.ID)
	if updated.Name != "Updated" { t.Fatalf("expected Updated, got %s", updated.Name) }
	if updated.Action != ActionQuarantine { t.Fatalf("expected quarantine, got %s", updated.Action) }
}

func TestDeletePolicy(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	p := &Policy{Name: "Delete Me", Action: ActionReject, Scope: ScopeSender, Value: "del@test.com"}
	svc.CreatePolicy(ctx, p)
	if err := svc.DeletePolicy(ctx, p.ID); err != nil { t.Fatalf("delete: %v", err) }
	got, _ := svc.GetPolicy(ctx, p.ID)
	if got != nil { t.Fatal("expected nil after delete") }
}

func TestQuarantineLifecycle(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	q, err := svc.QuarantineMessage(ctx, "msg-001", "sender@bad.com", "rcpt@good.com", "Matched spam policy")
	if err != nil { t.Fatalf("quarantine: %v", err) }
	if q.Status != QStatusQuarantined { t.Fatalf("expected quarantined, got %s", q.Status) }

	released, err := svc.ReleaseMessage(ctx, q.ID, "admin")
	if err != nil { t.Fatalf("release: %v", err) }
	if released.Status != QStatusReleased { t.Fatalf("expected released, got %s", released.Status) }
}

func TestQuarantineDelete(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	q, _ := svc.QuarantineMessage(ctx, "msg-002", "s@b.com", "r@b.com", "test")
	if err := svc.DeleteQuarantine(ctx, q.ID); err != nil { t.Fatalf("delete quarantine: %v", err) }
	got, _ := svc.GetQuarantine(ctx, q.ID)
	if got.Status != QStatusDeleted { t.Fatalf("expected deleted, got %s", got.Status) }
}

func TestListQuarantine(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.QuarantineMessage(ctx, "m1", "s1@a.com", "r1@a.com", "policy")
	svc.QuarantineMessage(ctx, "m2", "s2@a.com", "r2@a.com", "policy")
	msgs, err := svc.ListQuarantine(ctx)
	if err != nil { t.Fatalf("list: %v", err) }
	if len(msgs) != 2 { t.Fatalf("expected 2, got %d", len(msgs)) }
}

func TestInvalidPolicyRejected(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	tests := []struct{ name string; p *Policy }{
		{"empty name", &Policy{Action: ActionReject, Scope: ScopeSender, Value: "v"}},
		{"empty action", &Policy{Name: "n", Scope: ScopeSender, Value: "v"}},
		{"invalid action", &Policy{Name: "n", Action: "invalid", Scope: ScopeSender, Value: "v"}},
		{"invalid scope", &Policy{Name: "n", Action: ActionReject, Scope: "invalid", Value: "v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := svc.CreatePolicy(ctx, tt.p); err == nil { t.Fatal("expected error") }
		})
	}
}

func TestAbuseEvents(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()

	// Create the audit table so the query doesn't fail.
	svc.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coremail_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '', user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL
	)`)

	events, err := svc.ListAbuseEvents(ctx)
	if err != nil { t.Fatalf("list abuse: %v", err) }
	_ = events
}
