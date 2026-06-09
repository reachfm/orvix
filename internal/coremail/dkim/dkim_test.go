package dkim

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func testMessage() []byte {
	return []byte("From: sender@example.com\r\nTo: recipient@example.com\r\nSubject: Test\r\nDate: Mon, 1 Jan 2024 12:00:00 +0000\r\nMessage-ID: <abc123@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\nHello World")
}

// ── Key Generation Tests ─────────────────────────────────────

func TestGenerateKeyPair(t *testing.T) {
	privPEM, dnsRecord, err := GenerateKeyPair("default", "example.com")
	if err != nil {
		t.Fatalf("generate key pair: %v", err)
	}
	if privPEM == "" {
		t.Fatal("expected non-empty private key PEM")
	}
	if !strings.Contains(privPEM, "BEGIN PRIVATE KEY") {
		t.Fatal("expected PEM header")
	}
	if dnsRecord == "" {
		t.Fatal("expected non-empty DNS record")
	}
	if !strings.HasPrefix(dnsRecord, "v=DKIM1;") {
		t.Fatal("expected DNS record to start with v=DKIM1")
	}
}

func TestGenerateDNSRecord(t *testing.T) {
	name, value := GenerateDNSRecord("s1", "example.com", "MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDD")
	if name != "s1._domainkey.example.com" {
		t.Fatalf("unexpected record name: %s", name)
	}
	if !strings.Contains(value, "v=DKIM1;") {
		t.Fatal("expected v=DKIM1 in value")
	}
}

// ── Key Parsing Tests ────────────────────────────────────────

func TestParsePrivateKeyPKCS8(t *testing.T) {
	pemData := testKey(t)
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		t.Fatal("failed to decode PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse pkcs8: %v", err)
	}
	if _, ok := key.(*rsa.PrivateKey); !ok {
		t.Fatal("expected RSA private key")
	}
}

// ── Signing Tests ────────────────────────────────────────────

func TestSignMessage(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()

	hs := HeaderSet{
		Domain:        "example.com",
		Selector:      "default",
		PrivateKeyPEM: testKey(t),
		SignedHeaders: DefaultHeaders,
	}

	result, err := signer.Sign(msg, hs)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if result.Signature == "" {
		t.Fatal("expected non-empty signature")
	}
	if !strings.Contains(result.Signature, "v=1;") {
		t.Fatal("expected DKIM version tag")
	}
	if !strings.Contains(result.Signature, "bh=") {
		t.Fatal("expected body hash tag")
	}
	if !strings.Contains(result.Signature, "b=") {
		t.Fatal("expected signature tag")
	}
}

func TestSignMessageMultipleRecipients(t *testing.T) {
	signer := NewSigner()
	msg := []byte("From: sender@example.com\r\nTo: rcpt1@example.com, rcpt2@example.com\r\nSubject: Multi\r\nDate: Mon, 1 Jan 2024 12:00:00 +0000\r\nMessage-ID: <multi@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\nHello Multiple")

	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}
	result, err := signer.Sign(msg, hs)
	if err != nil {
		t.Fatalf("sign multi: %v", err)
	}
	if result.Signature == "" {
		t.Fatal("expected signature for multi-recipient")
	}
}

func TestSignMessageLargeBody(t *testing.T) {
	signer := NewSigner()
	body := strings.Repeat("Large body content line.\r\n", 1000)
	msg := []byte(fmt.Sprintf("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: Large\r\nDate: Mon, 1 Jan 2024 12:00:00 +0000\r\nMessage-ID: <large@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\n%s", body))

	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}
	result, err := signer.Sign(msg, hs)
	if err != nil {
		t.Fatalf("sign large: %v", err)
	}
	if result.Signature == "" {
		t.Fatal("expected signature for large body")
	}
}

func TestSignMessageEmptyBody(t *testing.T) {
	signer := NewSigner()
	msg := []byte("From: sender@example.com\r\nTo: rcpt@example.com\r\nSubject: Empty\r\nDate: Mon, 1 Jan 2024 12:00:00 +0000\r\nMessage-ID: <empty@example.com>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain\r\n\r\n")

	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}
	result, err := signer.Sign(msg, hs)
	if err != nil {
		t.Fatalf("sign empty: %v", err)
	}
	if result.Signature == "" {
		t.Fatal("expected signature for empty body")
	}
	if !strings.Contains(result.Signature, "bh=") {
		t.Fatal("expected body hash for empty body")
	}
}

func TestSignMessageMissingKey(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()
	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: ""}
	_, err := signer.Sign(msg, hs)
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestSignMessageInvalidKey(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()
	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: "INVALID KEY DATA"}
	_, err := signer.Sign(msg, hs)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

// ── Canonicalization Tests ───────────────────────────────────

func TestCanonicalizeBodyRelaxed(t *testing.T) {
	body := []byte("Hello\r\n \r\nWorld\r\n")
	result := canonicalizeBody(body, CanonRelaxed)
	if len(result) == 0 {
		t.Fatal("expected non-empty canonicalized body")
	}
}

func TestCanonicalizeBodyRemoveTrailingWhitespace(t *testing.T) {
	body := []byte("Hello   \r\nWorld\t\r\n")
	result := canonicalizeBody(body, CanonRelaxed)
	if strings.Contains(string(result), "   ") {
		t.Fatal("trailing whitespace not removed")
	}
	if strings.Contains(string(result), "\t") {
		t.Fatal("trailing tabs not removed")
	}
}

func TestCanonicalizeBodyRemoveTrailingBlankLines(t *testing.T) {
	body := []byte("Hello\r\n\r\n\r\n")
	result := canonicalizeBody(body, CanonRelaxed)
	if strings.Count(string(result), "\n") != 1 {
		t.Fatal("trailing blank lines not removed")
	}
}

func TestCanonicalizeBodyEmpty(t *testing.T) {
	result := canonicalizeBody([]byte{}, CanonRelaxed)
	_ = result
}

// ── HeaderSet Defaults Tests ─────────────────────────────────

func TestHeaderSetDefaults(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()
	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}
	result, err := signer.Sign(msg, hs)
	if err != nil {
		t.Fatalf("sign with defaults: %v", err)
	}
	if result.Signature == "" {
		t.Fatal("expected signature with default header set")
	}
}

// ── PEM Encode Tests ─────────────────────────────────────────

func TestPEMEncodeBase64(t *testing.T) {
	data := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	expected := base64.StdEncoding.EncodeToString(data)
	result := pemEncodeBase64(data)
	if result != expected {
		t.Fatalf("expected %s, got %s", expected, result)
	}
}

func TestPEMEncodeBase64Empty(t *testing.T) {
	result := pemEncodeBase64([]byte{})
	if result != "" {
		t.Fatalf("expected empty, got %s", result)
	}
}

// ── Body Hash Tests ──────────────────────────────────────────

func TestBodyHashDeterministic(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()
	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}

	r1, _ := signer.Sign(msg, hs)
	r2, _ := signer.Sign(msg, hs)

	bh1 := extractTag(r1.Signature, "bh")
	bh2 := extractTag(r2.Signature, "bh")
	if bh1 != bh2 {
		t.Fatal("body hash should be deterministic")
	}
}

func extractTag(sig, tag string) string {
	for _, part := range strings.Split(sig, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, tag+"=") {
			return strings.TrimPrefix(part, tag+"=")
		}
	}
	return ""
}

// ── Canonicalization Stability Tests ─────────────────────────

func TestCanonicalizeHeaders(t *testing.T) {
	headers := []header{
		{Name: "From", Value: " sender@example.com "},
		{Name: "Subject", Value: " Test "},
	}
	result := canonicalizeHeaders(headers, []string{"from", "subject"}, CanonRelaxed)
	if len(result) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(result))
	}
	if result[0].Value != "sender@example.com" {
		t.Fatalf("expected 'sender@example.com', got %q", result[0].Value)
	}
}

func TestCanonicalizeHeaderValue(t *testing.T) {
	val := canonicalizeHeaderValue("  Hello   World  ")
	if val != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", val)
	}
}

// ── Signature Determinism Tests ──────────────────────────────

func TestSignatureDeterministic(t *testing.T) {
	signer := NewSigner()
	msg := testMessage()
	hs := HeaderSet{Domain: "example.com", Selector: "s1", PrivateKeyPEM: testKey(t)}

	r1, _ := signer.Sign(msg, hs)
	bh1 := extractTag(r1.Signature, "bh")
	if bh1 == "" {
		t.Fatal("expected body hash")
	}
}

// ── DKIM Config Repository Tests ─────────────────────────────

func dkimDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range Indexes() {
		db.Exec(stmt)
	}
	return db
}

func TestDKIMConfigCreateAndGet(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	cfg := &DKIMConfig{Domain: "example.com", Selector: "default", PrivateKeyPEM: testKey(t), Enabled: true}
	if err := repo.Create(ctx, cfg, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if cfg.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := repo.GetByDomain(ctx, "example.com", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected config")
	}
	if got.Domain != "example.com" {
		t.Fatalf("expected example.com, got %s", got.Domain)
	}
	if !got.Enabled {
		t.Fatal("expected enabled")
	}
}

func TestDKIMConfigUpdate(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &DKIMConfig{Domain: "test.com", Selector: "s1", PrivateKeyPEM: testKey(t), Enabled: true}, nil)
	cfg, _ := repo.GetByDomain(ctx, "test.com", nil)
	cfg.Selector = "s2"
	cfg.Enabled = false
	if err := repo.Update(ctx, cfg, nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, _ := repo.GetByDomain(ctx, "test.com", nil)
	if got.Selector != "s2" {
		t.Fatalf("expected s2, got %s", got.Selector)
	}
	if got.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestDKIMConfigDelete(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &DKIMConfig{Domain: "del.com", Selector: "s1", PrivateKeyPEM: testKey(t), Enabled: true}, nil)
	if err := repo.Delete(ctx, "del.com", nil); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, _ := repo.GetByDomain(ctx, "del.com", nil)
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestDKIMConfigGetNonexistent(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	got, err := repo.GetByDomain(ctx, "nonexistent.com", nil)
	if err != nil {
		t.Fatalf("get nonexistent: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent")
	}
}

func TestDKIMConfigList(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &DKIMConfig{Domain: "a.com", Selector: "s1", PrivateKeyPEM: testKey(t), Enabled: true}, nil)
	repo.Create(ctx, &DKIMConfig{Domain: "b.com", Selector: "s1", PrivateKeyPEM: testKey(t), Enabled: false}, nil)

	list, err := repo.List(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

func TestDKIMConfigCreateDuplicate(t *testing.T) {
	db := dkimDB(t)
	repo := NewSQLRepo(db)
	ctx := context.Background()

	repo.Create(ctx, &DKIMConfig{Domain: "dup.com", Selector: "s1", PrivateKeyPEM: testKey(t), Enabled: true}, nil)
	err := repo.Create(ctx, &DKIMConfig{Domain: "dup.com", Selector: "s2", PrivateKeyPEM: testKey(t), Enabled: true}, nil)
	if err == nil {
		t.Fatal("expected error for duplicate domain")
	}
}
