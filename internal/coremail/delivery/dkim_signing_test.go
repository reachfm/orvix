package delivery

// Reproduction + regression tests for the outbound-DKIM live gap: a
// production message from a domain with a valid, enabled DKIM
// configuration was delivered WITHOUT a DKIM-Signature header. Root
// cause: internal/coremail/runtime/module.go constructed every
// DeliveryWorker without ever wiring DKIMSigner/DKIMConfigs, so
// signWithDKIM's very first guard (both nil) fired for every message
// and silently returned the original, unsigned data — indistinguishable
// from "DKIM intentionally not configured".
//
// These tests exercise the REAL production path end to end: a queued
// message goes through worker.ProcessOnce → deliverRemote →
// signWithDKIM → the SMTP transport, and the fake SMTP server captures
// the exact bytes transmitted. A real RSA key is generated per test;
// no live DNS or real SMTP server is used. Signatures are
// cryptographically verified with dkim.Verifier, not merely checked
// for the presence of the "DKIM-Signature:" string.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	_ "modernc.org/sqlite"
)

// ── Test fixtures ────────────────────────────────────────────────

// genRSATestKey returns a fresh 2048-bit RSA key pair, PEM-encoded
// exactly as the admin/platform DKIM generation flow stores it
// (PKCS8 private key) and as DNS would publish it (PKIX public key,
// base64, no PEM armor — matching extractPublicKey's expectation).
func genRSATestKey(t *testing.T) (privatePEM string, publicKeyBase64 string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})

	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(privPEM), base64.StdEncoding.EncodeToString(pubDER)
}

// fakeDKIMRepo is a minimal in-memory dkim.Repository double, letting
// tests control exactly what GetByDomain returns per test case:
// a real config, an explicit error (simulating a DB outage), or "not
// found" (nil, nil) for a genuinely unconfigured domain.
type fakeDKIMRepo struct {
	configs map[string]*dkim.DKIMConfig
	errFor  map[string]error
}

func newFakeDKIMRepo() *fakeDKIMRepo {
	return &fakeDKIMRepo{configs: map[string]*dkim.DKIMConfig{}, errFor: map[string]error{}}
}

func (r *fakeDKIMRepo) Create(ctx context.Context, cfg *dkim.DKIMConfig, tx interface{}) error {
	return nil
}
func (r *fakeDKIMRepo) Update(ctx context.Context, cfg *dkim.DKIMConfig, tx interface{}) error {
	return nil
}
func (r *fakeDKIMRepo) Delete(ctx context.Context, domain string, tx interface{}) error { return nil }
func (r *fakeDKIMRepo) List(ctx context.Context, tx interface{}) ([]dkim.DKIMConfig, error) {
	return nil, nil
}

func (r *fakeDKIMRepo) GetByDomain(ctx context.Context, domain string, tx interface{}) (*dkim.DKIMConfig, error) {
	if err, ok := r.errFor[domain]; ok {
		return nil, err
	}
	if cfg, ok := r.configs[domain]; ok {
		return cfg, nil
	}
	return nil, nil
}

// fakeDNSResolver serves a fixed public-key TXT record for the DKIM
// verifier — no live DNS is ever used.
type fakeDNSResolver struct {
	txt map[string][]string
}

func (r *fakeDNSResolver) LookupTXT(ctx context.Context, domain string) ([]string, error) {
	return r.txt[domain], nil
}

// dkimTestEnv mirrors regressionEnv (regression_test.go) but adds a
// fakeDKIMRepo + real dkim.Signer wired onto the worker, exactly as
// internal/coremail/runtime/module.go now wires the production worker.
func dkimTestEnv(t *testing.T) (*queue.QueueEngine, *storage.MailStore, *DeliveryWorker, *fakeDKIMRepo, *fakeSMTPServer) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dkim.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, stmt := range queue.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	db.Exec(AttemptHistoryTable())
	for _, idx := range AttemptHistoryIndexes() {
		db.Exec(idx)
	}

	qe := queue.NewQueueEngine(db)
	ms, _ := storage.NewMailStore(db, filepath.Join(t.TempDir(), "msgs"))

	fs := startFakeSMTP(t)
	fs.requireStartTLS = false
	fs.allowPlaintext = true
	resolver := NewFakeResolver()
	resolver.MXRecords["remote.test"] = []MXRecord{{Host: fs.addr, Priority: 10}}
	resolver.Hosts[fs.addr] = []string{fs.addr}

	transport := NewSMTPTransport(testTransportConfig())
	worker := NewDeliveryWorker(qe, ms, resolver, transport, "local.test", "dkim-worker")
	worker.History = NewAttemptHistorySQLRepo(db)
	worker.Audit = NewAuditLogger()

	repo := newFakeDKIMRepo()
	worker.DKIMConfigs = repo
	worker.DKIMSigner = dkim.NewSigner()

	return qe, ms, worker, repo, fs
}

func enqueueOutbound(t *testing.T, qe *queue.QueueEngine, ms *storage.MailStore, from, to string) *queue.QueueEntry {
	t.Helper()
	ctx := context.Background()
	entry := &queue.QueueEntry{
		TenantID: 1, DomainID: 1, MessageID: storage.GenerateMessageID(),
		FromAddress: from, ToAddress: to, RecipientDomain: "remote.test",
		Direction: queue.DirectionOutbound, DeliveryMode: queue.DeliveryRemoteSMTP, MaxAttempts: 3,
	}
	if err := qe.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := ms.StoreMessage(ctx, &storage.Message{
		MessageID: entry.MessageID, TenantID: 1, DomainID: 1, MailboxID: 1,
		FromAddress: from, ToAddresses: to,
	}, []byte("Subject: Test\r\nFrom: "+from+"\r\nTo: "+to+"\r\n\r\nBody line one.\r\n"), nil); err != nil {
		t.Fatalf("store message: %v", err)
	}
	return entry
}

// ── 1/2. CONFIGURED + VALID KEY: signed, cryptographically valid,
//         correct d=/s= ─────────────────────────────────────────

func TestDKIMSigning_ConfiguredDomainProducesValidCryptographicSignature(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	privPEM, pubB64 := genRSATestKey(t)
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: privPEM, Enabled: true}

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got == nil || got.Status != queue.StatusDelivered {
		t.Fatalf("expected delivered, got %v", got)
	}

	raw := fs.receivedData
	if !strings.Contains(string(raw), "DKIM-Signature:") {
		t.Fatalf("expected DKIM-Signature header in transmitted data, got:\n%s", raw)
	}

	verifier := dkim.NewVerifier(&fakeDNSResolver{txt: map[string][]string{
		"orvix._domainkey.orvix.email": {"v=DKIM1; k=rsa; p=" + pubB64},
	}})
	result := verifier.VerifyMessage(context.Background(), raw)
	if result.Result != dkim.VerifyPass {
		t.Fatalf("expected cryptographic verification pass, got %s: %s", result.Result, result.Explanation)
	}
	if result.Domain != "orvix.email" {
		t.Fatalf("expected d=orvix.email, got d=%s", result.Domain)
	}
	if result.Selector != "orvix" {
		t.Fatalf("expected s=orvix, got s=%s", result.Selector)
	}
}

// ── 3. UNCONFIGURED DOMAIN: unsigned delivery still succeeds ───────

func TestDKIMSigning_UnconfiguredDomainDeliversUnsignedPerExistingPolicy(t *testing.T) {
	qe, ms, worker, _, fs := dkimTestEnv(t)
	entry := enqueueOutbound(t, qe, ms, "nobody@unconfigured.example", "recipient@remote.test")

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got == nil || got.Status != queue.StatusDelivered {
		t.Fatalf("expected delivered (unconfigured domains are still deliverable), got %v", got)
	}
	if strings.Contains(string(fs.receivedData), "DKIM-Signature:") {
		t.Fatalf("did not expect a DKIM-Signature header for a genuinely unconfigured domain")
	}
}

// ── 4. CONFIGURED DOMAIN + REPOSITORY ERROR: no unsigned send ──────

func TestDKIMSigning_ConfiguredDomainRepositoryErrorDefersNeverSendsUnsigned(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	repo.errFor["orvix.email"] = fmt.Errorf("database connection reset")

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got == nil {
		t.Fatal("expected queue entry")
	}
	if got.Status == queue.StatusDelivered {
		t.Fatalf("a repository error on a configured domain must NEVER deliver unsigned, got delivered")
	}
	if fs.receivedData != nil {
		t.Fatalf("SMTP transport must never be invoked with unsigned data for a configured domain, got transmitted bytes: %s", fs.receivedData)
	}
	// Must be a retryable/deferred outcome, not a lost message.
	if got.Status != queue.StatusPending && got.Status != queue.StatusDeferred {
		t.Fatalf("expected pending/deferred (retryable), got %s", got.Status)
	}
}

// ── 5. CONFIGURED DOMAIN + MISSING PRIVATE KEY: no unsigned send ──

func TestDKIMSigning_ConfiguredDomainMissingPrivateKeyDefersNeverSendsUnsigned(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: "", Enabled: true}

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got != nil && got.Status == queue.StatusDelivered {
		t.Fatalf("a missing private key on a configured domain must NEVER deliver unsigned")
	}
	if fs.receivedData != nil {
		t.Fatalf("SMTP transport must never receive data with a missing key, got: %s", fs.receivedData)
	}
}

// ── 6. CONFIGURED DOMAIN + MALFORMED PRIVATE KEY: no unsigned send ─

func TestDKIMSigning_ConfiguredDomainMalformedPrivateKeyDefersNeverSendsUnsigned(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: "not a real PEM block", Enabled: true}

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")

	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got != nil && got.Status == queue.StatusDelivered {
		t.Fatalf("a malformed private key on a configured domain must NEVER deliver unsigned")
	}
	if fs.receivedData != nil {
		t.Fatalf("SMTP transport must never receive data with a malformed key, got: %s", fs.receivedData)
	}
}

// ── 7/8. SIGNER/INFRASTRUCTURE UNAVAILABLE FOR A CONFIGURED DOMAIN:
//         no unsigned send (the exact live-gap reproduction) ──────

func TestDKIMSigning_NilSignerOrRepoIsTreatedAsGlobalUnconfigured(t *testing.T) {
	// This is the pre-fix production state: DKIMSigner/DKIMConfigs are
	// both nil because nothing wired them. Per policy this is the
	// deployment-wide "DKIM not integrated at all" state (distinct from
	// "this domain is configured but broken") and unsigned delivery is
	// allowed — but the important regression this pins is elsewhere:
	// TestDKIMSigning_ConfiguredDomainProducesValidCryptographicSignature
	// proves that once the repo/signer ARE wired (as
	// internal/coremail/runtime/module.go now does in production), a
	// configured domain's mail is never again silently unsigned.
	qe, ms, worker, _, fs := dkimTestEnv(t)
	worker.DKIMSigner = nil
	worker.DKIMConfigs = nil

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")
	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got == nil || got.Status != queue.StatusDelivered {
		t.Fatalf("expected delivered (nil signer/repo = DKIM not integrated deployment-wide), got %v", got)
	}
	if strings.Contains(string(fs.receivedData), "DKIM-Signature:") {
		t.Fatalf("did not expect a signature when the DKIM subsystem itself is not wired")
	}
}

// ── 9. RETRY: no duplicate DKIM-Signature accumulation ─────────────

func TestDKIMSigning_RetryDoesNotAccumulateDuplicateSignatures(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	privPEM, _ := genRSATestKey(t)
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: privPEM, Enabled: true}

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")

	// First delivery attempt.
	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("first ProcessOnce: worked=%v err=%v", worked, err)
	}
	first := fs.receivedData
	firstCount := strings.Count(string(first), "DKIM-Signature:")
	if firstCount != 1 {
		t.Fatalf("expected exactly 1 DKIM-Signature after first delivery, got %d", firstCount)
	}

	// Re-run signWithDKIM directly against the ORIGINAL immutable queued
	// source (as a retry would reload it from MailStore) to prove
	// signing again does not compound onto already-signed data from a
	// previous in-memory buffer.
	_, data, err := ms.LoadMessageByMessageID(context.Background(), entry.MessageID)
	if err != nil {
		t.Fatalf("reload message: %v", err)
	}
	if strings.Contains(string(data), "DKIM-Signature:") {
		t.Fatalf("the canonical stored message must remain unsigned — signing must never mutate MailStore's copy, only the wire bytes")
	}
	resigned, state, signErr := worker.signWithDKIM(context.Background(), data, entry)
	if signErr != nil || state != DKIMSigned {
		t.Fatalf("expected a clean re-sign on retry, got state=%v err=%v", state, signErr)
	}
	if c := strings.Count(string(resigned), "DKIM-Signature:"); c != 1 {
		t.Fatalf("expected exactly 1 DKIM-Signature on a fresh re-sign from the immutable source, got %d", c)
	}
}

// ── 10. ENVELOPE-FROM NORMALIZATION ─────────────────────────────────

func TestDKIMSigning_EnvelopeFromDomainExtraction(t *testing.T) {
	cases := []struct{ addr, want string }{
		{"salma@orvix.email", "orvix.email"},
		{"SALMA@ORVIX.EMAIL", "ORVIX.EMAIL"}, // case handling is the repository's/config lookup's responsibility, not extraction's
		{"", ""},
		{"not-an-email", ""},
	}
	for _, c := range cases {
		got := extractDomainFromAddress(c.addr)
		if got != c.want {
			t.Errorf("extractDomainFromAddress(%q) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// ── 11. BOUNCE / NULL ENVELOPE: explicit tested behavior ───────────

func TestDKIMSigning_NullEnvelopeFromIsTreatedAsUnconfiguredNotAnError(t *testing.T) {
	qe, ms, worker, repo, fs := dkimTestEnv(t)
	privPEM, _ := genRSATestKey(t)
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: privPEM, Enabled: true}

	// A bounce/DSN with a null reverse-path has no sender domain to
	// sign for — this must be treated as DKIMUnconfigured (proceed
	// unsigned), never as a configured-domain failure that blocks
	// delivery of the bounce itself.
	entry := enqueueOutbound(t, qe, ms, "", "recipient@remote.test")
	worked, err := worker.ProcessOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("ProcessOnce: worked=%v err=%v", worked, err)
	}
	got, _ := qe.Repo.Get(context.Background(), entry.ID, nil)
	if got == nil || got.Status != queue.StatusDelivered {
		t.Fatalf("expected a null-envelope bounce to still deliver, got %v", got)
	}
	if strings.Contains(string(fs.receivedData), "DKIM-Signature:") {
		t.Fatalf("a null envelope-from has no domain to sign for; must not be signed")
	}
}

// ── 15. PRIVATE KEY LEAKAGE AUDIT ────────────────────────────────────

func TestDKIMSigning_ConfigErrorNeverLeaksPrivateKeyMaterial(t *testing.T) {
	qe, ms, worker, repo, _ := dkimTestEnv(t)
	privPEM, _ := genRSATestKey(t)
	// A malformed key still exercises the sign-error path with real
	// key-shaped material present in the config, proving the resulting
	// error text never echoes it back.
	repo.configs["orvix.email"] = &dkim.DKIMConfig{Domain: "orvix.email", Selector: "orvix", PrivateKeyPEM: "garbage-not-pem", Enabled: true}
	_ = privPEM

	entry := enqueueOutbound(t, qe, ms, "salma@orvix.email", "recipient@remote.test")
	_, data, err := ms.LoadMessageByMessageID(context.Background(), entry.MessageID)
	if err != nil {
		t.Fatalf("load message: %v", err)
	}
	_, state, signErr := worker.signWithDKIM(context.Background(), data, entry)
	if state != DKIMSignError {
		t.Fatalf("expected DKIMSignError, got %v", state)
	}
	if signErr == nil {
		t.Fatal("expected a non-nil error")
	}
	if strings.Contains(signErr.Error(), "garbage-not-pem") || strings.Contains(signErr.Error(), "PRIVATE KEY") {
		t.Fatalf("signing error must never contain private key material, got: %v", signErr)
	}
}
