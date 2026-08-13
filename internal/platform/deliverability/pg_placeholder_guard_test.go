package deliverability

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// H-5/B-4 regression guard.
//
// The verified defect was that the deliverability aggregation queries embedded
// raw SQLite-style `?` placeholders. PostgreSQL requires $1..$n, so those
// queries failed at the driver and the Deliverability API returned 500. SQLite
// tests could never catch it, because `?` is correct there.
//
// This suite drives the repository with a PostgreSQL dialect against a
// capturing driver, so it asserts the SQL TEXT THAT WOULD BE SENT TO
// PostgreSQL — without needing a PostgreSQL server. It is the hermetic
// counterpart to the live PostgreSQL suite (pg_portability_test.go /
// dialect_equivalence_test.go), which additionally proves the results are
// semantically identical when a server is available.

// ── capturing database/sql driver ──────────────────────────────────────────

type captureDriver struct{ store *captureStore }

type captureStore struct {
	mu      sync.Mutex
	queries []string
	args    [][]driver.NamedValue
}

func (s *captureStore) record(q string, args []driver.NamedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queries = append(s.queries, q)
	s.args = append(s.args, args)
}

func (s *captureStore) last() (string, []driver.NamedValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		return "", nil
	}
	return s.queries[len(s.queries)-1], s.args[len(s.args)-1]
}

func (d captureDriver) Open(string) (driver.Conn, error) { return &captureConn{store: d.store}, nil }

type captureConn struct{ store *captureStore }

func (c *captureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (c *captureConn) Close() error                        { return nil }
func (c *captureConn) Begin() (driver.Tx, error)           { return nil, io.EOF }

// QueryContext records the statement and returns an empty result set, so the
// caller's Scan reports sql.ErrNoRows rather than a driver error. The SQL text
// is what this suite asserts on.
func (c *captureConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.store.record(q, args)
	return &captureRows{}, nil
}

func (c *captureConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.store.record(q, args)
	return driver.RowsAffected(0), nil
}

type captureRows struct{}

func (r *captureRows) Columns() []string         { return nil }
func (r *captureRows) Close() error              { return nil }
func (r *captureRows) Next([]driver.Value) error { return io.EOF }

var (
	captureRegisterOnce sync.Once
	captureShared       = &captureStore{}
)

// newPostgresDialectRepo returns a Repository whose dialect is PostgreSQL and
// whose statements are captured instead of executed.
func newPostgresDialectRepo(t *testing.T) (*Repository, *captureStore) {
	t.Helper()
	// database/sql allows a driver name to be registered only once per
	// process, so the capture store is process-wide. Assertions read only the
	// LAST captured statement, which keeps each call unambiguous even when
	// tests share the store.
	captureRegisterOnce.Do(func() {
		sql.Register("orvix_capture", captureDriver{store: captureShared})
	})
	db, err := sql.Open("orvix_capture", "capture")
	if err != nil {
		t.Fatalf("open capture db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// The dialect is set explicitly: dbdialect.Detect cannot identify a fake
	// driver, and the point of this suite is to exercise the PostgreSQL
	// rendering path.
	return &Repository{db: db, dialect: dbdialect.FromDriver("postgres")}, captureShared
}

// rawQuestionMark matches a `?` used as a bind placeholder. It deliberately
// ignores `?` inside string literals (there are none in these queries).
var rawQuestionMark = regexp.MustCompile(`\?`)

// assertPostgresPlaceholders is the core assertion: the rendered SQL must use
// $n placeholders, must contain no bare `?`, and must number placeholders
// 1..want contiguously.
func assertPostgresPlaceholders(t *testing.T, query string, want int) {
	t.Helper()
	if rawQuestionMark.MatchString(query) {
		t.Fatalf("PostgreSQL query still contains a raw `?` placeholder — this is the H-5 defect.\nSQL:\n%s", query)
	}
	for i := 1; i <= want; i++ {
		tok := "$" + itoa(i)
		if !containsPlaceholder(query, tok) {
			t.Fatalf("expected placeholder %s in PostgreSQL query, missing.\nSQL:\n%s", tok, query)
		}
	}
	// One past the end must NOT appear (guards against silent arg drift).
	if containsPlaceholder(query, "$"+itoa(want+1)) {
		t.Fatalf("unexpected placeholder $%d — argument count drifted.\nSQL:\n%s", want+1, query)
	}
}

// containsPlaceholder matches $n exactly, so $1 does not match inside $12.
func containsPlaceholder(q, tok string) bool {
	for i := 0; i+len(tok) <= len(q); i++ {
		if q[i:i+len(tok)] != tok {
			continue
		}
		next := i + len(tok)
		if next < len(q) && q[next] >= '0' && q[next] <= '9' {
			continue // part of a longer number
		}
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestAggregate_RendersPostgresPlaceholders proves the per-dimension
// aggregation (Repository.Aggregate) renders $1..$9 on PostgreSQL. This is the
// exact query that returned 500 before the fix.
func TestAggregate_RendersPostgresPlaceholders(t *testing.T) {
	repo, store := newPostgresDialectRepo(t)
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC()

	// Scan will report no rows; the SQL text is what matters here.
	_, _ = repo.Aggregate(context.Background(), DimensionSendingDomain, "example.test", start, end)

	q, args := store.last()
	if q == "" {
		t.Fatal("no statement captured")
	}
	if !strings.Contains(q, "platform_deliverability_signals") {
		t.Fatalf("unexpected statement captured:\n%s", q)
	}
	assertPostgresPlaceholders(t, q, 9)
	if len(args) != 9 {
		t.Fatalf("expected 9 bound arguments, got %d", len(args))
	}
}

// TestAggregateTenant_RendersPostgresPlaceholders proves the tenant-wide
// aggregation (Repository.AggregateTenant) renders $1..$14 on PostgreSQL.
func TestAggregateTenant_RendersPostgresPlaceholders(t *testing.T) {
	repo, store := newPostgresDialectRepo(t)
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC()

	_, _ = repo.AggregateTenant(context.Background(), 42, start, end)

	q, args := store.last()
	if q == "" {
		t.Fatal("no statement captured")
	}
	if !strings.Contains(q, "platform_deliverability_signals") {
		t.Fatalf("unexpected statement captured:\n%s", q)
	}
	assertPostgresPlaceholders(t, q, 14)
	if len(args) != 14 {
		t.Fatalf("expected 14 bound arguments, got %d", len(args))
	}
	// The tenant predicate must be bound, never interpolated.
	if strings.Contains(q, "tenant_id=42") {
		t.Fatalf("tenant id must be a bound parameter, not concatenated:\n%s", q)
	}
}

// TestDeliverabilityRepository_NoRawPlaceholdersOnPostgres sweeps the
// repository's read/write surface and asserts that NO statement rendered for
// PostgreSQL contains a raw `?`. This is the broad guard: a future query added
// with `?` fails here even if nobody remembers to test it on a real server.
func TestDeliverabilityRepository_NoRawPlaceholdersOnPostgres(t *testing.T) {
	repo, store := newPostgresDialectRepo(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC()
	now := time.Now().UTC()

	// Exercise every statement-producing method. Errors are irrelevant: the
	// capturing driver returns empty results; the SQL text is the subject.
	type call struct {
		name string
		run  func()
	}
	calls := []call{
		{"RecordSignal", func() {
			_, _ = repo.RecordSignal(ctx, &Signal{EventKey: "k", TenantID: 1, Dimension: DimensionTenant, DimensionValue: "1", Type: SignalDelivered, RecordedAt: now})
		}},
		{"Aggregate", func() { _, _ = repo.Aggregate(ctx, DimensionTenant, "1", start, end) }},
		{"AggregateTenant", func() { _, _ = repo.AggregateTenant(ctx, 1, start, end) }},
		{"ListEvents", func() {
			_, _, _ = repo.ListEvents(ctx, EventFilter{TenantID: 1, Domain: "d.test", Type: SignalDelivered, Provider: "p", Start: &start, End: &end, Limit: 10})
		}},
		{"GetSignal", func() { _, _ = repo.GetSignal(ctx, 1, 1) }},
		{"DimensionBreakdown", func() { _, _ = repo.DimensionBreakdown(ctx, 1, DimensionTenant, start, end, 10) }},
		{"CategoryBreakdown", func() { _, _ = repo.CategoryBreakdown(ctx, 1, start, end) }},
		{"TimeBuckets", func() { _, _ = repo.TimeBuckets(ctx, 1, start, end, "hour", 10) }},
		{"PurgeSignalsBefore", func() { _, _ = repo.PurgeSignalsBefore(ctx, start) }},
		{"AddSuppression", func() {
			_ = repo.AddSuppression(ctx, &Suppression{TenantID: 1, Address: "a@b.test", Reason: SuppressionReason("bounce"), CreatedAt: now, UpdatedAt: now})
		}},
		{"GetSuppression", func() { _, _ = repo.GetSuppression(ctx, 1, 1) }},
		{"GetSuppressionByAddress", func() { _, _ = repo.GetSuppressionByAddress(ctx, 1, "a@b.test") }},
		{"ReleaseSuppression", func() { _, _ = repo.ReleaseSuppression(ctx, 1, 1, 2, "r", now) }},
		{"ReactivateSuppression", func() {
			_, _ = repo.ReactivateSuppression(ctx, 1, 1, 2, SuppressionReason("bounce"), "s", "n", nil, now)
		}},
		{"ReconcileExpired", func() { _, _ = repo.ReconcileExpired(ctx, now) }},
		{"RecordSuppressionEvent", func() { _ = repo.RecordSuppressionEvent(ctx, 1, 1, 2, "released", "r", now) }},
		{"ListSuppressionEvents", func() { _, _ = repo.ListSuppressionEvents(ctx, 1, 1, 10) }},
		{"RemoveSuppression", func() { _, _ = repo.RemoveSuppression(ctx, 1, "a@b.test", now) }},
		{"IsSuppressed", func() { _, _, _ = repo.IsSuppressed(ctx, 1, "a@b.test", now) }},
		{"ListSuppressions", func() {
			_, _, _ = repo.ListSuppressions(ctx, SuppressionFilter{TenantID: 1, Domain: "b.test", Search: "a", Limit: 10})
		}},
	}

	for _, c := range calls {
		c.run()
		q, _ := store.last()
		if q == "" {
			t.Fatalf("%s: no statement captured", c.name)
		}
		if rawQuestionMark.MatchString(q) {
			t.Errorf("%s renders a raw `?` placeholder on PostgreSQL (H-5 defect class).\nSQL:\n%s", c.name, q)
		}
	}
}
