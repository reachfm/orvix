package relay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
	_ "modernc.org/sqlite"
)

// newConcurrentTestService builds a file-backed SQLite service with a
// real connection pool so goroutine-concurrency tests exercise real
// driver-level locking, not a serialized :memory: handle.
func newConcurrentTestService(t *testing.T) (*sql.DB, *Service, *audit.ExtendedStore) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "relay-conc.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	outbox := kernel.NewOutboxRepository(repo.dialect)
	if err := outbox.EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("outbox schema: %v", err)
	}
	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(context.Background()); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	svc := NewService(repo, nil, outbox).
		WithSecretCodec(fakeSecretCodec{}).
		WithAuditStore(auditStore)
	return db, svc, auditStore
}

var testActor = AuditActor{ID: 42, Role: "platform_super_admin", RequestID: "req-1", IP: "10.0.0.1", UserAgent: "test"}

func baseCreate() RelayCreateInput {
	return RelayCreateInput{
		Scope: ScopeGlobal, Name: "sendgrid", Host: "smtp.sendgrid.test", Port: 587,
		Username: "apikey", Password: "super-secret", ConnSecurity: ConnSecurityStartTLS,
		TLSValidation: TLSValidationStrict, Priority: 10, Active: true,
	}
}

func TestRelayAdmin_CreateEncryptsAndNeverReturnsSecret(t *testing.T) {
	_, svc, auditStore := newConcurrentTestService(t)
	ctx := context.Background()

	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.HasCredential != true {
		t.Fatal("expected has_credential=true")
	}
	if r.SecretRef != "" {
		t.Fatal("SecretRef must never be present on the API shape")
	}

	// The DB must hold only the encrypted form.
	var stored string
	if err := svc.repo.db.QueryRow(`SELECT secret_ref FROM platform_relay_providers WHERE id=?`, r.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if stored != "enc:super-secret" {
		t.Fatalf("expected encrypted-at-rest form, got %q", stored)
	}
	body := strings.ToLower(fmt.Sprintf("%+v", r))
	if strings.Contains(body, "super-secret") || strings.Contains(body, "secret_ref") {
		t.Fatalf("secret material leaked in response shape: %s", body)
	}

	// Audit record must not contain the plaintext or the encrypted ref.
	entries, _, err := auditStore.Search(ctx, &audit.ExtendedQuery{Action: "platform.relay.create"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry, got %d err=%v", len(entries), err)
	}
	for _, e := range entries {
		blob := fmt.Sprintf("%+v", e)
		if strings.Contains(blob, "super-secret") || strings.Contains(blob, "enc:") {
			t.Fatalf("audit record leaked credential material: %s", blob)
		}
		if e.ActorID != 42 || e.ActorRole != "platform_super_admin" || e.RequestID != "req-1" {
			t.Fatalf("audit entry missing actor identity: %+v", e)
		}
	}
}

func TestRelayAdmin_ListAndGetRedacted(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateRelay(ctx, baseCreate(), testActor); err != nil {
		t.Fatal(err)
	}
	list, total, err := svc.ListRelays(ctx, ProviderFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(list) != 1 || list[0].SecretRef != "" {
		t.Fatalf("list redaction wrong: total=%d len=%d %+v", total, len(list), list)
	}
	got, err := svc.GetRelay(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SecretRef != "" || got.HasCredential != true {
		t.Fatal("detail must be redacted and report has_credential")
	}
}

func TestRelayAdmin_FiltersAndSearch(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	tenant := uint(7)
	a := baseCreate()
	a.Name, a.Host, a.Scope, a.TenantID = "tenant-relay", "relay.example.net", ScopeTenant, tenant
	if _, err := svc.CreateRelay(ctx, a, testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRelay(ctx, baseCreate(), testActor); err != nil {
		t.Fatal(err)
	}
	_, total, err := svc.ListRelays(ctx, ProviderFilter{TenantID: &tenant, Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("tenant filter: total=%d err=%v", total, err)
	}
	_, total, err = svc.ListRelays(ctx, ProviderFilter{Search: "sendgrid", Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("search filter: total=%d err=%v", total, err)
	}
	inactive := false
	if _, err := svc.SetRelayActive(ctx, 1, false, 1, testActor); err != nil {
		t.Fatal(err)
	}
	_, total, err = svc.ListRelays(ctx, ProviderFilter{Active: &inactive, Limit: 10})
	if err != nil || total != 1 {
		t.Fatalf("active filter: total=%d err=%v", total, err)
	}
}

func TestRelayAdmin_ValidateTargetRejectsUnsafeHosts(t *testing.T) {
	bad := []string{
		"127.0.0.1", "localhost", "::1", "10.0.0.5", "172.16.0.5", "192.168.1.1",
		"169.254.169.254", "0.0.0.0", "::", "224.0.0.1", "ff02::1", "169.254.1.1",
		"http://smtp.example.com", "smtp.example.com:25", "user@host", "host/path",
		"", "   ", "*.example.com", "sm tp.example.com", "exa mple.com",
	}
	for _, host := range bad {
		if err := ValidateRelayTarget(host, 587); err == nil {
			t.Errorf("host %q must be rejected", host)
		}
	}
	for _, port := range []int{0, -1, 65536} {
		if err := ValidateRelayTarget("smtp.example.com", port); err == nil {
			t.Errorf("port %d must be rejected", port)
		}
	}
	for _, ok := range []struct {
		host string
		port int
	}{
		// A genuine globally-routable IPv6 literal (2001:db8::/32 is
		// documentation and is correctly rejected by the hardened policy).
		{"smtp.example.com", 25}, {"relay.example.net", 587}, {"[2606:2800:220:1:248:1893:25c8:1946]", 465},
		{"smtp.example.com.", 2525}, {"a.b.example.com", 1}, {"relay_example.com", 465},
	} {
		if err := ValidateRelayTarget(ok.host, ok.port); err != nil {
			t.Errorf("host %q port %d must be accepted: %v", ok.host, ok.port, err)
		}
	}
}

func TestRelayAdmin_CreateRejectsUnsafeTarget(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	in := baseCreate()
	in.Host = "169.254.169.254"
	_, err := svc.CreateRelay(ctx, in, testActor)
	if err == nil || !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("expected ErrUnsafeTarget, got %v", err)
	}
	in2 := baseCreate()
	in2.Port = 0
	if _, err := svc.CreateRelay(ctx, in2, testActor); err == nil {
		t.Fatal("port 0 must be rejected")
	}
	in3 := baseCreate()
	in3.Name = ""
	if _, err := svc.CreateRelay(ctx, in3, testActor); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("expected ErrNameRequired, got %v", err)
	}
	in4 := baseCreate()
	in4.ConnSecurity = "bogus"
	if _, err := svc.CreateRelay(ctx, in4, testActor); !errors.Is(err, ErrInvalidConnSecurity) {
		t.Fatalf("expected ErrInvalidConnSecurity, got %v", err)
	}
}

type fakeResolver struct {
	addrs []net.IP
	err   error
}

func (f fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]net.IPAddr, 0, len(f.addrs))
	for _, ip := range f.addrs {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

func TestValidatingDialer_RejectsUnsafeResolvedAddresses(t *testing.T) {
	d := &validatingDialer{timeout: time.Second, resolver: fakeResolver{addrs: []net.IP{net.ParseIP("169.254.169.254")}}}
	_, err := d.DialContext(context.Background(), "tcp", "attacker.example:587")
	if err == nil || !strings.Contains(err.Error(), "unsafe relay target") {
		t.Fatalf("expected unsafe-target rejection, got %v", err)
	}
	d = &validatingDialer{timeout: time.Second, resolver: fakeResolver{addrs: []net.IP{net.ParseIP("192.168.1.1")}}}
	if _, err := d.DialContext(context.Background(), "tcp", "attacker.example:587"); err == nil {
		t.Fatal("private resolved address must be rejected")
	}
	d = &validatingDialer{timeout: time.Second, resolver: fakeResolver{addrs: []net.IP{net.ParseIP("127.0.0.1")}}}
	if _, err := d.DialContext(context.Background(), "tcp", "attacker.example:587"); err == nil {
		t.Fatal("loopback resolved address must be rejected")
	}
	// Mixed safe+unsafe answers must reject (no pass-through).
	d = &validatingDialer{timeout: time.Second, resolver: fakeResolver{addrs: []net.IP{net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")}}}
	if _, err := d.DialContext(context.Background(), "tcp", "mixed.example:587"); err == nil {
		t.Fatal("mixed answers containing an unsafe IP must be rejected")
	}
	// Resolution failure must surface as an error, not a dial.
	d = &validatingDialer{timeout: time.Second, resolver: fakeResolver{err: fmt.Errorf("NXDOMAIN")}}
	if _, err := d.DialContext(context.Background(), "tcp", "nx.example:587"); err == nil {
		t.Fatal("resolution failure must error")
	}
}

// rebindResolver returns `first` on its first call and `second` afterwards.
// It proves the dialer resolves exactly once: if it ever re-resolved, the
// second (unsafe) answer would take effect.
type rebindResolver struct {
	first  []net.IP
	second []net.IP
	calls  int
}

func (r *rebindResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls++
	src := r.first
	if r.calls > 1 {
		src = r.second
	}
	out := make([]net.IPAddr, 0, len(src))
	for _, ip := range src {
		out = append(out, net.IPAddr{IP: ip})
	}
	return out, nil
}

func TestValidatingDialer_DialsValidatedIPNotHostname(t *testing.T) {
	// The dialer must connect to the exact IP the resolver returned, never
	// re-resolve the hostname (rebinding protection). An injected recording
	// connector captures the dialed address so the assertion is hermetic.
	// 93.184.216.34 is a genuine globally-routable unicast address.
	var dialed string
	d := &validatingDialer{
		timeout:  time.Second,
		resolver: fakeResolver{addrs: []net.IP{net.ParseIP("93.184.216.34")}},
		connect: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = addr
			return nil, fmt.Errorf("connect refused (test)")
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "rebind.example:587")
	if err == nil {
		t.Fatal("expected the injected connector to refuse")
	}
	if dialed != "93.184.216.34:587" {
		t.Fatalf("dial must target the validated resolved IP, got %q", dialed)
	}
}

// TestValidatingDialer_RebindAfterValidationCannotBypass proves there is no
// resolve-validate-then-reresolve window: the resolver is consulted once and
// the validated answer is what gets dialed.
func TestValidatingDialer_RebindAfterValidationCannotBypass(t *testing.T) {
	rb := &rebindResolver{
		first:  []net.IP{net.ParseIP("93.184.216.34")}, // safe: validated + dialed
		second: []net.IP{net.ParseIP("127.0.0.1")},     // would be unsafe if re-resolved
	}
	var dialed string
	d := &validatingDialer{
		timeout:  time.Second,
		resolver: rb,
		connect: func(_ context.Context, _, addr string) (net.Conn, error) {
			dialed = addr
			return nil, fmt.Errorf("connect refused (test)")
		},
	}
	if _, err := d.DialContext(context.Background(), "tcp", "rebind.example:587"); err == nil {
		t.Fatal("expected the injected connector to refuse")
	}
	if dialed != "93.184.216.34:587" {
		t.Fatalf("dial must use the first, validated answer, got %q", dialed)
	}
	if rb.calls != 1 {
		t.Fatalf("resolver must be consulted exactly once, got %d calls", rb.calls)
	}
}

func TestValidatingDialer_BoundedByContextTimeout(t *testing.T) {
	// A connector that blocks until the context is done proves the dial is
	// bounded by the caller's context.
	d := &validatingDialer{
		timeout:  time.Hour,
		resolver: fakeResolver{addrs: []net.IP{net.ParseIP("93.184.216.34")}},
		connect: func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := d.DialContext(ctx, "tcp", "timeout.example:25")
	if err == nil {
		t.Fatal("expected an error from the bounded dial")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("dial must respect the context deadline")
	}
}

func TestRelayAdmin_UpdateGuardedByVersion(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != 1 {
		t.Fatalf("expected version 1, got %d", r.Version)
	}
	name := "renamed"
	port := 465
	upd, err := svc.UpdateRelay(ctx, r.ID, 1, RelayUpdateInput{Name: &name, Port: &port}, testActor)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Name != "renamed" || upd.Port != 465 || upd.Version != 2 {
		t.Fatalf("update applied incorrectly: %+v", upd)
	}
	// Stale version must be rejected.
	if _, err := svc.UpdateRelay(ctx, r.ID, 1, RelayUpdateInput{Name: &name}, testActor); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale update, got %v", err)
	}
	// Missing id must yield not-found.
	if _, err := svc.UpdateRelay(ctx, 9999, 1, RelayUpdateInput{Name: &name}, testActor); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

func TestRelayAdmin_EnableDisableTransitionsAndStaleVersion(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := svc.SetRelayActive(ctx, r.ID, false, 1, testActor)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Active {
		t.Fatal("expected relay disabled")
	}
	// Stale version after the disable (now version 2) must conflict.
	if _, err := svc.SetRelayActive(ctx, r.ID, true, 1, testActor); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict on stale disable, got %v", err)
	}
	enabled, err := svc.SetRelayActive(ctx, r.ID, true, 2, testActor)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !enabled.Active {
		t.Fatal("expected relay enabled")
	}
	// A second enable with the SAME version is a stale write (no-op).
	if _, err := svc.SetRelayActive(ctx, r.ID, true, 2, testActor); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected duplicate same-version transition to conflict, got %v", err)
	}
}

func TestRelayAdmin_RotateReturnsGeneratedCredentialOnce(t *testing.T) {
	_, svc, auditStore := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	updated, generated, err := svc.RotateRelayCredentials(ctx, r.ID, 1, "", testActor)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if generated == "" {
		t.Fatal("expected a generated credential")
	}
	if updated.HasCredential != true || updated.SecretRef != "" {
		t.Fatal("rotated response must be redacted with has_credential=true")
	}
	var stored string
	if err := svc.repo.db.QueryRow(`SELECT secret_ref FROM platform_relay_providers WHERE id=?`, r.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "enc:"+generated {
		t.Fatalf("expected encrypted generated credential at rest, got %q", stored)
	}
	// Generated credential must not appear in audit.
	entries, _, _ := auditStore.Search(ctx, &audit.ExtendedQuery{Action: "platform.relay.rotate_credentials"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 rotation audit entry, got %d", len(entries))
	}
	if strings.Contains(fmt.Sprintf("%+v", entries[0]), generated) {
		t.Fatal("generated credential leaked into audit")
	}
	// Re-reading detail never returns it.
	got, _ := svc.GetRelay(ctx, r.ID)
	if strings.Contains(fmt.Sprintf("%+v", got), generated) {
		t.Fatal("generated credential leaked into detail response")
	}
	// Stale version rotation conflicts.
	if _, _, err := svc.RotateRelayCredentials(ctx, r.ID, 1, "x", testActor); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected stale rotation conflict, got %v", err)
	}
}

func TestRelayAdmin_DeleteWithAuditAndOutbox(t *testing.T) {
	db, svc, auditStore := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteRelay(ctx, r.ID, testActor); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetRelay(ctx, r.ID); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
	// Transactional outbox + audit evidence exist and carry no secrets.
	var topics []string
	rows, err := db.Query(`SELECT topic FROM platform_outbox_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var topic string
		rows.Scan(&topic)
		topics = append(topics, topic)
	}
	found := false
	for _, topic := range topics {
		if strings.Contains(topic, "deleted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a delete outbox event, got %v", topics)
	}
	entries, _, _ := auditStore.Search(ctx, &audit.ExtendedQuery{Action: "platform.relay.delete"})
	if len(entries) != 1 {
		t.Fatalf("expected delete audit entry, got %d", len(entries))
	}
}

// ── Concurrency (real goroutines + real SQLite) ────────────────────

func TestRelayAdmin_ConcurrentIdenticalCreateCreatesOneRow(t *testing.T) {
	db, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.CreateRelay(ctx, baseCreate(), testActor)
		}(i)
	}
	wg.Wait()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM platform_relay_providers`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 relay row from %d concurrent identical creates, got %d", n, count)
	}
	winners := 0
	for _, e := range errs {
		if e == nil {
			winners++
		}
		if e != nil && !errors.Is(e, ErrRelayNameConflict) {
			t.Fatalf("unexpected error type: %v", e)
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one identical create must win, got %d", winners)
	}
}

func TestRelayAdmin_ConcurrentEnableDisableLastWriterWinsByVersion(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.SetRelayActive(ctx, r.ID, i%2 == 0, 1, testActor)
		}(i)
	}
	wg.Wait()
	winners := 0
	for _, e := range errs {
		if e == nil {
			winners++
		}
		if e != nil && !errors.Is(e, ErrVersionConflict) {
			t.Fatalf("unexpected error type: %v", e)
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one transition must win the version race, got %d winners", winners)
	}
	final, _ := svc.GetRelay(ctx, r.ID)
	if final.Version != 2 {
		t.Fatalf("expected version 2 after one winning transition, got %d", final.Version)
	}
}

func TestRelayAdmin_ConcurrentRotationSingleWinner(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = svc.RotateRelayCredentials(ctx, r.ID, 1, "", testActor)
		}(i)
	}
	wg.Wait()
	winners := 0
	for _, e := range errs {
		if e == nil {
			winners++
		}
		if e != nil && !errors.Is(e, ErrVersionConflict) {
			t.Fatalf("unexpected error type: %v", e)
		}
	}
	if winners != 1 {
		t.Fatalf("exactly one rotation must win, got %d", winners)
	}
}

func TestRelayAdmin_OutboxAndAuditContainNoPlaintext(t *testing.T) {
	db, svc, auditStore := newConcurrentTestService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.RotateRelayCredentials(ctx, r.ID, 1, "", testActor); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetRelayActive(ctx, r.ID, false, 2, testActor); err != nil {
		t.Fatal(err)
	}

	// Outbox payloads: no plaintext, no encrypted ref.
	rows, err := db.Query(`SELECT topic, aggregate_id, payload FROM platform_outbox_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var topic, agg, payload string
		if err := rows.Scan(&topic, &agg, &payload); err != nil {
			t.Fatal(err)
		}
		blob := topic + agg + payload
		if strings.Contains(blob, "super-secret") || strings.Contains(blob, "enc:") {
			t.Fatalf("outbox event leaked credential material: %s", blob)
		}
	}
	// Audit rows: no plaintext, no encrypted ref.
	entries, _, _ := auditStore.Search(ctx, &audit.ExtendedQuery{Limit: 100})
	for _, e := range entries {
		blob := fmt.Sprintf("%+v", e)
		if strings.Contains(blob, "super-secret") || strings.Contains(blob, "enc:") {
			t.Fatalf("audit entry leaked credential material: %s", blob)
		}
	}
}

func TestRelayAdmin_TestConnectionUnsafeTargetNeverDialed(t *testing.T) {
	_, svc, _ := newConcurrentTestService(t)
	ctx := context.Background()
	in := baseCreate()
	in.Host = "127.0.0.1"
	r, err := svc.CreateRelay(ctx, in, testActor)
	if err == nil {
		t.Fatal("creating a loopback relay must be rejected")
	}
	_ = r
	// A provider already persisted with an unsafe host (e.g. from a
	// pre-policy database) must fail the test without dialing.
	now := time.Now().UTC()
	p := Provider{Host: "10.0.0.5", Port: 25, ConnSecurity: ConnSecurityNone, CreatedAt: now, UpdatedAt: now, Active: true}
	if err := svc.repo.CreateProvider(ctx, &p); err != nil {
		t.Fatal(err)
	}
	res, err := svc.TestRelay(ctx, p.ID, testActor)
	if err != nil {
		t.Fatalf("test should return a result, not an error: %v", err)
	}
	if res.Connected {
		t.Fatal("unsafe target must never connect")
	}
	if !strings.Contains(res.Error, "unsafe relay target") {
		t.Fatalf("expected redacted unsafe-target error, got %q", res.Error)
	}
}

func TestRelayAdmin_GenerateCredentialStrongAndURLSafe(t *testing.T) {
	for i := 0; i < 20; i++ {
		c, err := GenerateRelayCredential()
		if err != nil {
			t.Fatalf("generate credential: %v", err)
		}
		if len(c) < 40 {
			t.Fatalf("generated credential too short: %d", len(c))
		}
		for _, r := range c {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				t.Fatalf("credential contains unsafe char %q", r)
			}
		}
	}
}
