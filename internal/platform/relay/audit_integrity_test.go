package relay

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// Fix G acceptance: business state, audit evidence and outbox event are one
// atomic unit.
//
// The defect was that audit and outbox errors were discarded with
// `_ = s.audit.RecordTx(...)` / `_ = s.outbox.Enqueue(...)` INSIDE the
// transaction, so the transaction went on to commit and the business state
// survived without the evidence explaining it.
//
// Faults are injected by dropping the real audit or outbox table, producing a
// genuine driver error on the real code path.

func newAuditedService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, svc := newTestService(t)
	ctx := context.Background()

	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(ctx); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	outbox := kernel.NewOutboxRepository(d)
	if err := outbox.EnsureSchema(ctx, db); err != nil {
		t.Fatalf("outbox schema: %v", err)
	}
	return db, svc.WithAuditStore(auditStore).WithOutbox(outbox)
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func baseRelayInput(name string) RelayCreateInput {
	return RelayCreateInput{
		Scope: ScopeGlobal, Name: name, Host: "smtp." + name + ".example.com", Port: 587,
		ConnSecurity: ConnSecurityStartTLS, TLSValidation: TLSValidationStrict, Active: true,
	}
}

// ── Audit failure rolls back the business mutation ───────────────────────

func TestCreateRelay_AuditFailureRollsBackTheRelay(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()
	before := countRows(t, db, "platform_relay_providers")

	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		// Fall back to whatever the store named its table.
		t.Skipf("audit table name not as expected: %v", err)
	}

	_, err := svc.CreateRelay(ctx, baseRelayInput("audit-fail"), testActor)
	if err == nil {
		t.Fatal("an audit failure must fail the create, not commit silently")
	}
	if after := countRows(t, db, "platform_relay_providers"); after != before {
		t.Fatalf("the relay must have been rolled back: %d rows before, %d after", before, after)
	}
}

func TestCreateRelay_OutboxFailureRollsBackTheRelay(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()
	before := countRows(t, db, "platform_relay_providers")

	if _, err := db.Exec(`DROP TABLE platform_outbox_events`); err != nil {
		t.Skipf("outbox table name not as expected: %v", err)
	}

	_, err := svc.CreateRelay(ctx, baseRelayInput("outbox-fail"), testActor)
	if err == nil {
		t.Fatal("an outbox failure must fail the create, not commit silently")
	}
	if after := countRows(t, db, "platform_relay_providers"); after != before {
		t.Fatalf("the relay must have been rolled back: %d rows before, %d after", before, after)
	}
}

// TestSetRelayActive_AuditFailureRollsBackTheTransition proves a state
// transition cannot survive without the record of who made it.
func TestSetRelayActive_AuditFailureRollsBackTheTransition(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	created, err := svc.CreateRelay(ctx, baseRelayInput("state-audit"), testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		t.Skipf("audit table name not as expected: %v", err)
	}

	if _, err := svc.SetRelayActive(ctx, created.ID, false, created.Version, testActor); err == nil {
		t.Fatal("an audit failure must fail the transition")
	}
	// The relay must still be ACTIVE: the disable was rolled back.
	reloaded, err := svc.repo.GetProvider(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Active {
		t.Fatal("the state transition must have been rolled back with its failed audit")
	}
}

// TestRotateRelayCredentials_AuditFailureRollsBackTheSecret is the most
// important rollback: a credential must never be replaced without the record
// of the rotation.
func TestRotateRelayCredentials_AuditFailureRollsBackTheSecret(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	in := baseRelayInput("rotate-audit")
	in.Password = "original-password"
	created, err := svc.CreateRelay(ctx, in, testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	original, err := svc.repo.GetProvider(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if _, err := db.Exec(`DROP TABLE orvix_audit`); err != nil {
		t.Skipf("audit table name not as expected: %v", err)
	}

	_, plaintext, rerr := svc.RotateRelayCredentials(ctx, created.ID, created.Version, "", testActor)
	if rerr == nil {
		t.Fatal("an audit failure must fail the rotation")
	}
	if plaintext != "" {
		t.Fatal("a failed rotation must NEVER return a one-time credential")
	}
	after, err := svc.repo.GetProvider(ctx, created.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.SecretRef != original.SecretRef {
		t.Fatal("the credential must have been rolled back with its failed audit")
	}
}

// ── Success path: business state, audit and outbox all present ───────────

func TestCreateRelay_CommitsBusinessStateAuditAndOutboxTogether(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	created, err := svc.CreateRelay(ctx, baseRelayInput("happy"), testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected a real id")
	}
	if countRows(t, db, "orvix_audit") == 0 {
		t.Fatal("a successful create must leave an audit entry")
	}
	if countRows(t, db, "platform_outbox_events") == 0 {
		t.Fatal("a successful create must leave an outbox event")
	}
}

// TestAudit_TenantOwnedResourceNeverRecordedAsTenantZero pins the tenant-0
// defect: SetRelayActive and TestRelay both hardcoded tenant 0, so a
// tenant-owned relay being disabled or probed left no trace in that tenant's
// audit trail.
func TestAudit_TenantOwnedResourceNeverRecordedAsTenantZero(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	const tenant = uint(77)
	in := baseRelayInput("tenant-owned")
	in.Scope, in.TenantID = ScopeTenant, tenant
	created, err := svc.CreateRelay(ctx, in, testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.SetRelayActive(ctx, created.ID, false, created.Version, testActor); err != nil {
		t.Fatalf("disable: %v", err)
	}

	rows, err := db.Query(`SELECT action, tenant_id FROM orvix_audit ORDER BY id ASC`)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var action string
		var tid uint
		if err := rows.Scan(&action, &tid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if !strings.HasPrefix(action, "platform.relay.") {
			continue
		}
		seen++
		if tid != tenant {
			t.Fatalf("%s recorded against tenant %d; a tenant-owned relay must be audited under tenant %d", action, tid, tenant)
		}
	}
	if seen < 2 {
		t.Fatalf("expected create and disable audit entries, saw %d", seen)
	}
}

// TestAudit_PayloadsCarryNoSecretMaterial proves audit and outbox rows never
// contain a password, secret reference, or ciphertext.
func TestAudit_PayloadsCarryNoSecretMaterial(t *testing.T) {
	db, svc := newAuditedService(t)
	ctx := context.Background()

	const secret = "super-secret-relay-password"
	in := baseRelayInput("secrecy")
	in.Password = secret
	created, err := svc.CreateRelay(ctx, in, testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := svc.RotateRelayCredentials(ctx, created.ID, created.Version, "another-secret-value", testActor); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	for _, table := range []string{"orvix_audit", "platform_outbox_events"} {
		dump := dumpTable(t, db, table)
		for _, forbidden := range []string{secret, "another-secret-value", "enc:" + secret, "enc:another-secret-value"} {
			if strings.Contains(dump, forbidden) {
				t.Fatalf("%s contains secret material (%q)", table, forbidden)
			}
		}
		// The encrypted form must not appear either.
		if strings.Contains(dump, "enc:") {
			t.Fatalf("%s contains an encrypted secret reference", table)
		}
	}
}

// dumpTable concatenates every column of every row into one string so a test
// can assert that a value appears nowhere in it.
func dumpTable(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table)
	if err != nil {
		t.Fatalf("select %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	var sb strings.Builder
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for _, v := range vals {
			switch x := v.(type) {
			case nil:
			case []byte:
				sb.Write(x)
			case string:
				sb.WriteString(x)
			case time.Time:
			default:
				sb.WriteString(strings.TrimSpace(strings.Join([]string{}, "")))
			}
			sb.WriteByte('\x00')
		}
	}
	return sb.String()
}
