package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	"go.uber.org/zap"
)

// H-6 regression suite for MFA challenge bounding.
//
// Before this fix an MFA challenge was a bare stateless JWT: unlimited TOTP
// guesses were accepted for the whole 5-minute window (defeating the second
// factor given a stolen password), and a challenge could be replayed after a
// successful verification. See ORVIX_FINAL_SECURITY_AUDIT_REPORT H-6.
//
// Time is injected, so expiry is exercised deterministically with no sleeps.

func newChallengeStore(t *testing.T) (*MFAChallengeStore, *sql.DB, *time.Time) {
	t.Helper()
	logger := zap.NewNop()
	cfg := config.Defaults()
	cfg.Database.Driver = "sqlite"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "mfa.db") + "?_loc=auto&_busy_timeout=5000&_txlock=immediate"
	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := models.MigrateAllRaw(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s := NewMFAChallengeStore(sqlDB)
	s.SetClock(func() time.Time { return now })
	if err := s.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return s, sqlDB, &now
}

func issueChallenge(t *testing.T, s *MFAChallengeStore, userID uint) string {
	t.Helper()
	jti, err := NewChallengeID()
	if err != nil {
		t.Fatalf("new id: %v", err)
	}
	if err := s.Issue(context.Background(), jti, userID, 5*time.Minute); err != nil {
		t.Fatalf("issue: %v", err)
	}
	return jti
}

func TestMFAChallenge_AttemptsAreCapped(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 42)

	// The cap counts every attempt, successful or not.
	for i := 0; i < MFAMaxChallengeAttempts; i++ {
		if err := s.Begin(ctx, jti, 42); err != nil {
			t.Fatalf("attempt %d should be allowed, got %v", i+1, err)
		}
	}
	if err := s.Begin(ctx, jti, 42); !errors.Is(err, ErrMFAChallengeExhausted) {
		t.Fatalf("attempt %d must be refused with ErrMFAChallengeExhausted, got %v",
			MFAMaxChallengeAttempts+1, err)
	}
}

func TestMFAChallenge_ExhaustedChallengeCannotBeConsumed(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 42)
	for i := 0; i < MFAMaxChallengeAttempts; i++ {
		_ = s.Begin(ctx, jti, 42)
	}
	if err := s.Begin(ctx, jti, 42); !errors.Is(err, ErrMFAChallengeExhausted) {
		t.Fatalf("expected exhaustion, got %v", err)
	}
	// Even a caller that guessed correctly on a burnt challenge gets nothing:
	// the handler never reaches Consume because Begin already refused.
}

func TestMFAChallenge_ExpiryEnforced(t *testing.T) {
	s, _, now := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 7)

	*now = now.Add(6 * time.Minute) // past the 5-minute TTL
	if err := s.Begin(ctx, jti, 7); !errors.Is(err, ErrMFAChallengeExpired) {
		t.Fatalf("an expired challenge must be refused, got %v", err)
	}
	if err := s.Consume(ctx, jti, 7); err == nil {
		t.Fatal("an expired challenge must not be consumable")
	}
}

func TestMFAChallenge_SingleUseReplayRefused(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 9)

	if err := s.Begin(ctx, jti, 9); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if err := s.Consume(ctx, jti, 9); err != nil {
		t.Fatalf("first consume should succeed: %v", err)
	}
	// Replay of the same challenge must fail at both gates.
	if err := s.Consume(ctx, jti, 9); !errors.Is(err, ErrMFAChallengeConsumed) {
		t.Fatalf("replayed consume must be refused, got %v", err)
	}
	if err := s.Begin(ctx, jti, 9); !errors.Is(err, ErrMFAChallengeConsumed) {
		t.Fatalf("a consumed challenge must not accept further attempts, got %v", err)
	}
}

func TestMFAChallenge_UnknownChallengeRefused(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	unknown, _ := NewChallengeID()
	if err := s.Begin(ctx, unknown, 1); !errors.Is(err, ErrMFAChallengeNotFound) {
		t.Fatalf("an unissued challenge must be refused, got %v", err)
	}
}

// TestMFAChallenge_WrongOwnerRefused proves a challenge issued to one user
// cannot be driven by a token claiming a different subject.
func TestMFAChallenge_WrongOwnerRefused(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 100)
	if err := s.Begin(ctx, jti, 101); !errors.Is(err, ErrMFAChallengeNotFound) {
		t.Fatalf("a challenge must not be usable by another user, got %v", err)
	}
}

// TestMFAChallenge_ConcurrentAttemptsCannotExceedCap is the decisive
// concurrency proof: N goroutines racing on the same challenge must together
// be granted at most MFAMaxChallengeAttempts. A read-then-write counter would
// let them all read the same value and blow past the cap.
func TestMFAChallenge_ConcurrentAttemptsCannotExceedCap(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 55)

	const racers = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Begin(ctx, jti, 55); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if granted > MFAMaxChallengeAttempts {
		t.Fatalf("concurrent attempts exceeded the cap: %d granted, max %d", granted, MFAMaxChallengeAttempts)
	}
	if granted == 0 {
		t.Fatal("expected at least one attempt to be granted")
	}
}

// TestMFAChallenge_ConcurrentConsumeSucceedsOnce proves two simultaneous
// successful verifications cannot both mint a session.
func TestMFAChallenge_ConcurrentConsumeSucceedsOnce(t *testing.T) {
	s, _, _ := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 77)

	const racers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	consumed := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Consume(ctx, jti, 77); err == nil {
				mu.Lock()
				consumed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if consumed != 1 {
		t.Fatalf("exactly one concurrent consume must succeed, got %d", consumed)
	}
}

// TestMFAChallengeToken_RequiresChallengeID pins that a challenge token
// without a jti is refused: such a token predates H-6 tracking and could not
// be bounded, so accepting it would reopen the unlimited-guess hole.
func TestMFAChallengeToken_RequiresChallengeID(t *testing.T) {
	a := newRevocationTestAuth(t)

	token, jti, err := a.GenerateMFAChallengeTokenWithID(31)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if jti == "" {
		t.Fatal("expected a challenge id")
	}
	gotUser, gotJTI, err := a.ValidateMFAChallengeTokenWithID(token)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if gotUser != 31 || gotJTI != jti {
		t.Fatalf("round-trip mismatch: user=%d jti=%q", gotUser, gotJTI)
	}

	// A legacy token minted without a jti must be refused.
	legacy, err := a.generateLegacyMFAChallengeTokenForTest(31)
	if err != nil {
		t.Fatalf("legacy token: %v", err)
	}
	if _, _, err := a.ValidateMFAChallengeTokenWithID(legacy); err == nil {
		t.Fatal("a challenge token without a jti must be refused")
	}
}

func TestMFAChallenge_PurgeRemovesOldRows(t *testing.T) {
	s, sqlDB, now := newChallengeStore(t)
	ctx := context.Background()
	jti := issueChallenge(t, s, 3)

	*now = now.Add(48 * time.Hour)
	if err := s.Purge(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	var n int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM mfa_challenges WHERE jti = ?", jti).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatal("expired challenges must be purged")
	}
}
