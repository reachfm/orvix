package auth

// H-9 regression coverage: an access token revoked on logout must be rejected
// immediately by ValidateAccessToken, instead of remaining valid until its
// (short) expiry. Legacy tokens without a jti stay valid (they expire within
// the access TTL), and a nil/failing revocation store must fail safe rather
// than lock everyone out.

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
)

// newRevocationTestAuth builds an Authenticator backed by a real migrated
// SQLite DB (so the revoked_tokens table exists) with a fresh RSA key.
func newRevocationTestAuth(t *testing.T) *Authenticator {
	t.Helper()
	logger := testLogger(t)

	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "revocation.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("database: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Close the DB at test end so Windows can remove the temp DB file
	// during t.TempDir cleanup (an open handle blocks RemoveAll).
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		db:         db,
		logger:     logger,
		accessTTL:  15 * time.Minute,
		refreshTTL: 30 * 24 * time.Hour,
	}
}

// TestRevokedTokenRejected is the core H-9 guarantee.
func TestRevokedTokenRejected(t *testing.T) {
	a := newRevocationTestAuth(t)

	token, err := a.GenerateAccessToken(7, RoleAdmin)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// Before revocation the token is valid.
	if _, _, err := a.ValidateAccessToken(token); err != nil {
		t.Fatalf("expected valid token before revocation, got %v", err)
	}

	// Revoke (as logout does) — token must now be rejected immediately.
	if err := a.RevokeAccessToken(token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := a.ValidateAccessToken(token); err == nil {
		t.Fatal("expected revoked token to be rejected, but it validated")
	}
}

// TestNonRevokedTokenStillValid ensures revoking one token does not affect a
// different, un-revoked token for the same user.
func TestNonRevokedTokenStillValid(t *testing.T) {
	a := newRevocationTestAuth(t)

	revoked, _ := a.GenerateAccessToken(7, RoleAdmin)
	kept, _ := a.GenerateAccessToken(7, RoleAdmin)

	if err := a.RevokeAccessToken(revoked); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := a.ValidateAccessToken(kept); err != nil {
		t.Fatalf("un-revoked token should remain valid, got %v", err)
	}
	if _, _, err := a.ValidateAccessToken(revoked); err == nil {
		t.Fatal("revoked token should be rejected")
	}
}

// TestLegacyTokenWithoutJTIStillValid ensures tokens minted before H-9 (no
// jti claim) are treated as non-revocable and continue to validate, so the
// change does not invalidate in-flight sessions at rollout.
func TestLegacyTokenWithoutJTIStillValid(t *testing.T) {
	a := newRevocationTestAuth(t)

	// Craft a token with the pre-H-9 claim set (no jti), signed by the
	// same key so only the missing jti distinguishes it.
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  fmt.Sprintf("%d", 7),
		"role": string(RoleAdmin),
		"iat":  now.Unix(),
		"exp":  now.Add(a.accessTTL).Unix(),
	}
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(a.privateKey)
	if err != nil {
		t.Fatalf("sign legacy: %v", err)
	}

	if _, _, err := a.ValidateAccessToken(legacy); err != nil {
		t.Fatalf("legacy no-jti token should validate, got %v", err)
	}
	// RevokeAccessToken on a no-jti token is a harmless no-op.
	if err := a.RevokeAccessToken(legacy); err != nil {
		t.Fatalf("revoke legacy: %v", err)
	}
	if _, _, err := a.ValidateAccessToken(legacy); err != nil {
		t.Fatalf("legacy token still valid after no-op revoke, got %v", err)
	}
}

// TestRevocationFailsSafeWithoutStore proves a nil DB (no revocation store)
// degrades to "not revoked" rather than panicking or rejecting valid tokens.
func TestRevocationFailsSafeWithoutStore(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	a := &Authenticator{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
		db:         nil, // no store
		logger:     testLogger(t),
		accessTTL:  15 * time.Minute,
	}
	token, err := a.GenerateAccessToken(7, RoleAdmin)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, _, err := a.ValidateAccessToken(token); err != nil {
		t.Fatalf("with no store, token must validate (fail-safe), got %v", err)
	}
}

// TestExpiredRevocationsPruned ensures the opportunistic prune removes
// already-expired revocations so the table stays bounded.
func TestExpiredRevocationsPruned(t *testing.T) {
	a := newRevocationTestAuth(t)
	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatalf("DB(): %v", err)
	}

	// Seed an already-expired revocation directly (raw SQL — GORM writes
	// no-op under the custom SQLite dialector).
	if _, err := sqlDB.Exec("INSERT INTO revoked_tokens (jti, expires_at) VALUES (?, ?)", "stale", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Any revoke call triggers the prune.
	if err := a.revokeToken("fresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var count int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM revoked_tokens WHERE jti = ?", "stale").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected expired revocation to be pruned, still present")
	}
}
