package coremail

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestVerifyPasswordBcryptFallback proves legacy bcrypt mailbox hashes still
// verify through the coremail authentication service so existing mailboxes
// are not locked out after the Argon2id migration.
func TestVerifyPasswordBcryptFallback(t *testing.T) {
	cfg := DefaultAuthConfig()
	s := NewAuthService(nil, nil, nil, cfg)

	bcryptHash, err := bcrypt.GenerateFromPassword([]byte("LegacyPass123!"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}

	if !s.VerifyPassword("LegacyPass123!", string(bcryptHash)) {
		t.Fatal("bcrypt hash must verify through coremail AuthService")
	}
	if s.VerifyPassword("wrong", string(bcryptHash)) {
		t.Fatal("wrong password must fail against bcrypt hash")
	}

	valid, needsRehash := s.VerifyPasswordWithRehash("LegacyPass123!", string(bcryptHash))
	if !valid || !needsRehash {
		t.Fatalf("bcrypt match must report needsRehash=true, got valid=%v needsRehash=%v", valid, needsRehash)
	}
}

// TestHashCompatibility proves hashes produced by the shared hasher (the
// same one the admin mailbox service uses) verify through coremail auth with
// the parameters embedded in the hash string.
func TestHashCompatibility(t *testing.T) {
	cfg := DefaultAuthConfig()
	s := NewAuthService(nil, nil, nil, cfg)

	hash, err := s.HashPassword("CompatiblePass123!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format: %q", hash)
	}
	if !s.VerifyPassword("CompatiblePass123!", hash) {
		t.Fatal("hash must verify through the same AuthService")
	}

	// Hashes created with different embedded parameters still verify because
	// verification reads m/t/p from the hash string (bounded).
	other := NewAuthService(nil, nil, nil, AuthConfig{Argon2Time: 3, Argon2Memory: 64 * 1024, Argon2Threads: 2, Argon2KeyLen: 32})
	if !other.VerifyPassword("CompatiblePass123!", hash) {
		t.Fatal("hash with p=4 must verify under an AuthService configured for p=2 (params read from hash)")
	}
}

// TestAuthenticateMailboxRehashOnLogin proves a legacy bcrypt mailbox can
// log in and its stored hash is transparently upgraded to Argon2id.
func TestAuthenticateMailboxRehashOnLogin(t *testing.T) {
	db := testDB(t)
	domRepo := NewDomainSQLRepo(db)
	mboxRepo := NewMailboxSQLRepo(db)
	cfg := DefaultAuthConfig()
	svc := NewAuthService(mboxRepo, domRepo, nil, cfg)
	ctx := context.Background()

	dom := &Domain{Name: "rehash.example.com", TenantID: 1, Status: DomainActive}
	if err := domRepo.Create(ctx, dom, nil); err != nil {
		t.Fatal(err)
	}

	bcryptHash, _ := bcrypt.GenerateFromPassword([]byte("OldPass123!"), bcrypt.DefaultCost)
	mbox := &Mailbox{
		DomainID: dom.ID, TenantID: 1, LocalPart: "legacy", Email: "legacy@rehash.example.com",
		PasswordHash: string(bcryptHash), Status: MailboxActive,
	}
	if err := mboxRepo.Create(ctx, mbox, nil); err != nil {
		t.Fatal(err)
	}

	authed, err := svc.AuthenticateMailbox(ctx, "legacy@rehash.example.com", "OldPass123!")
	if err != nil {
		t.Fatalf("legacy bcrypt mailbox must authenticate: %v", err)
	}
	if authed == nil {
		t.Fatal("expected a mailbox")
	}

	// Stored hash upgraded to Argon2id.
	stored, err := mboxRepo.GetByEmail(ctx, "legacy@rehash.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("rehash-on-login must upgrade stored hash, got %q", stored.PasswordHash)
	}
	// New hash still verifies.
	if !svc.VerifyPassword("OldPass123!", stored.PasswordHash) {
		t.Fatal("upgraded hash must still verify the password")
	}
}

// TestVerifyMailboxPasswordRehash proves the SMTP identity path (which uses
// VerifyMailboxPassword) also upgrades legacy bcrypt hashes.
func TestVerifyMailboxPasswordRehash(t *testing.T) {
	db := testDB(t)
	domRepo := NewDomainSQLRepo(db)
	mboxRepo := NewMailboxSQLRepo(db)
	cfg := DefaultAuthConfig()
	svc := NewAuthService(mboxRepo, domRepo, nil, cfg)
	ctx := context.Background()

	dom := &Domain{Name: "smtprehash.example.com", TenantID: 1, Status: DomainActive}
	if err := domRepo.Create(ctx, dom, nil); err != nil {
		t.Fatal(err)
	}
	bcryptHash, _ := bcrypt.GenerateFromPassword([]byte("SmtpPass123!"), bcrypt.DefaultCost)
	mbox := &Mailbox{
		DomainID: dom.ID, TenantID: 1, LocalPart: "smtpuser", Email: "smtpuser@smtprehash.example.com",
		PasswordHash: string(bcryptHash), Status: MailboxActive,
	}
	if err := mboxRepo.Create(ctx, mbox, nil); err != nil {
		t.Fatal(err)
	}

	if !svc.VerifyMailboxPassword(ctx, mbox, "SmtpPass123!") {
		t.Fatal("smtp identity must authenticate legacy bcrypt mailbox")
	}
	stored, _ := mboxRepo.GetByEmail(ctx, "smtpuser@smtprehash.example.com", nil)
	if !strings.HasPrefix(stored.PasswordHash, "$argon2id$") {
		t.Fatalf("smtp rehash-on-login must upgrade stored hash, got %q", stored.PasswordHash)
	}
}
