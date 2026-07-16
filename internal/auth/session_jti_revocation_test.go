package auth

// Pass-4J remediation proof for targeted session/JWT revocation, exercised on
// SQLite and (when PGHOST is set) PostgreSQL:
//   - access-token issuance persists its JTI on the session row,
//   - revoking that session's JTI makes ValidateAccessToken reject the bearer
//     token immediately (not after natural expiry),
//   - an unrelated session's token stays valid (no wrong-session revoke),
//   - opaque sessions still invalidate by row deletion,
//   - legacy sessions without a JTI are a safe no-op.

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

func sessionRevocationSuite(t *testing.T, a *Authenticator) {
	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	d := a.dbDialect()

	t.Run("bearer JWT target revoke, unrelated stays valid", func(t *testing.T) {
		tokenA, jtiA, _, err := a.GenerateAccessTokenWithJTI(5, RoleAdmin)
		if err != nil {
			t.Fatalf("issue A: %v", err)
		}
		if _, _, err := a.GenerateRefreshToken(5, jtiA); err != nil {
			t.Fatalf("refresh A: %v", err)
		}
		tokenB, jtiB, _, err := a.GenerateAccessTokenWithJTI(5, RoleAdmin)
		if err != nil {
			t.Fatalf("issue B: %v", err)
		}
		if _, _, err := a.GenerateRefreshToken(5, jtiB); err != nil {
			t.Fatalf("refresh B: %v", err)
		}

		var count int
		if err := sqlDB.QueryRow("SELECT COUNT(*) FROM sessions WHERE jti = "+d.Placeholder(1), jtiA).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count == 0 {
			t.Fatal("session A JTI was not persisted on the session row")
		}

		if _, _, err := a.ValidateAccessToken(tokenA); err != nil {
			t.Fatalf("A should validate before revoke: %v", err)
		}
		if _, _, err := a.ValidateAccessToken(tokenB); err != nil {
			t.Fatalf("B should validate before revoke: %v", err)
		}

		if err := a.RevokeJTI(jtiA, time.Now().Add(a.refreshTTL)); err != nil {
			t.Fatalf("revoke A: %v", err)
		}

		if _, _, err := a.ValidateAccessToken(tokenA); err == nil {
			t.Fatal("session A bearer token must be rejected immediately after revocation")
		}
		if _, _, err := a.ValidateAccessToken(tokenB); err != nil {
			t.Fatalf("session B bearer token must remain valid: %v", err)
		}
	})

	t.Run("opaque session invalidated by row deletion", func(t *testing.T) {
		token := fmt.Sprintf("opaque-%d", time.Now().UnixNano())
		tokenHash := fmt.Sprintf("%x", sha256.Sum256([]byte(token)))
		now := time.Now().UTC()
		ins := "INSERT INTO sessions (created_at, updated_at, user_id, token_hash, role, email, ip, jti, expires_at) VALUES (" +
			d.Placeholders(9) + ")"
		if _, err := sqlDB.Exec(ins, now, now, uint(7), tokenHash, "admin", "a@b.c", "", "", now.Add(time.Hour)); err != nil {
			t.Fatalf("seed opaque session: %v", err)
		}
		if _, role, _, err := a.ValidateOpaqueSession(token); err != nil || role != RoleAdmin {
			t.Fatalf("opaque session should be valid: role=%q err=%v", role, err)
		}
		if _, err := sqlDB.Exec("DELETE FROM sessions WHERE token_hash = "+d.Placeholder(1), tokenHash); err != nil {
			t.Fatalf("delete opaque session: %v", err)
		}
		if _, _, _, err := a.ValidateOpaqueSession(token); err == nil {
			t.Fatal("opaque session must be invalid after its row is deleted")
		}
	})

	t.Run("legacy no-JTI revoke is a safe no-op", func(t *testing.T) {
		if err := a.RevokeJTI("", time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("empty JTI (legacy session) revoke must be a safe no-op: %v", err)
		}
	})
}

func TestSessionRevocation_SQLite(t *testing.T) {
	sessionRevocationSuite(t, newRevocationTestAuth(t))
}
