package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// H-6: an MFA challenge used to be a bare stateless JWT with a 5-minute TTL.
// Anyone holding a stolen password could therefore submit UNLIMITED TOTP
// guesses against the same challenge for the whole window — roughly 10^3–10^4
// online guesses against a 6-digit code is entirely feasible, which defeats
// the second factor. The challenge was also replayable: a code that succeeded
// once could be presented again until the token expired.
//
// MFAChallengeStore adds the durable state a challenge needs to be safe:
// a per-challenge attempt counter, single-use consumption, and an explicit
// expiry that does not depend solely on the JWT's own claim.

// MFAMaxChallengeAttempts is the hard cap on verification attempts for one
// challenge. Exceeding it burns the challenge; the user must re-authenticate
// with their password to obtain a new one.
const MFAMaxChallengeAttempts = 5

var (
	// ErrMFAChallengeNotFound means the challenge is unknown (never issued,
	// already pruned, or forged).
	ErrMFAChallengeNotFound = errors.New("mfa challenge not found")
	// ErrMFAChallengeConsumed means the challenge was already completed.
	// Presenting it again is a replay.
	ErrMFAChallengeConsumed = errors.New("mfa challenge already used")
	// ErrMFAChallengeExpired means the challenge window has passed.
	ErrMFAChallengeExpired = errors.New("mfa challenge expired")
	// ErrMFAChallengeExhausted means the attempt cap was reached.
	ErrMFAChallengeExhausted = errors.New("mfa challenge attempt limit reached")
)

// MFAChallengeStore persists per-challenge verification state.
type MFAChallengeStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
	now     func() time.Time
}

// NewMFAChallengeStore builds a store over an existing *sql.DB.
func NewMFAChallengeStore(db *sql.DB) *MFAChallengeStore {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &MFAChallengeStore{db: db, dialect: d, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the time source. Tests use it to exercise expiry
// deterministically instead of sleeping.
func (s *MFAChallengeStore) SetClock(now func() time.Time) { s.now = now }

// EnsureSchema creates the mfa_challenges table. Additive and idempotent;
// portable across SQLite and PostgreSQL.
func (s *MFAChallengeStore) EnsureSchema(ctx context.Context) error {
	if s.db == nil {
		return errors.New("mfa challenge store: no database")
	}
	d := s.dialect
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mfa_challenges (
		jti TEXT PRIMARY KEY,
		user_id BIGINT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		consumed_at `+d.TimestampType()+`,
		expires_at `+d.TimestampType()+` NOT NULL,
		created_at `+d.TimestampType()+` NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create mfa_challenges: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_mfa_challenges_user ON mfa_challenges (user_id)`)
	return nil
}

// NewChallengeID returns an unguessable challenge identifier.
func NewChallengeID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate mfa challenge id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Issue records a new challenge for userID with the given lifetime.
func (s *MFAChallengeStore) Issue(ctx context.Context, jti string, userID uint, ttl time.Duration) error {
	d := s.dialect
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mfa_challenges (jti, user_id, attempts, consumed_at, expires_at, created_at) VALUES (`+
			d.Placeholder(1)+", "+d.Placeholder(2)+", 0, NULL, "+d.Placeholder(3)+", "+d.Placeholder(4)+")",
		jti, userID, now.Add(ttl), now)
	if err != nil {
		return fmt.Errorf("issue mfa challenge: %w", err)
	}
	return nil
}

// Begin validates that a challenge may be attempted right now and atomically
// records the attempt. It must be called BEFORE verifying the submitted code,
// so a failed verification cannot avoid being counted (a caller that crashes
// mid-verification still burns the attempt — fail closed).
//
// The increment is a guarded UPDATE, so concurrent verification requests
// cannot each read the same count and collectively exceed the cap.
func (s *MFAChallengeStore) Begin(ctx context.Context, jti string, userID uint) error {
	d := s.dialect
	now := s.now()

	res, err := s.db.ExecContext(ctx,
		`UPDATE mfa_challenges SET attempts = attempts + 1 WHERE jti = `+d.Placeholder(1)+
			` AND user_id = `+d.Placeholder(2)+
			` AND consumed_at IS NULL AND expires_at > `+d.Placeholder(3)+
			` AND attempts < `+d.Placeholder(4),
		jti, userID, now, MFAMaxChallengeAttempts)
	if err != nil {
		return fmt.Errorf("begin mfa challenge: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("begin mfa challenge: %w", err)
	}
	if n == 1 {
		return nil
	}

	// The guarded UPDATE matched nothing. Read the row to report precisely
	// WHY, without ever treating "unknown" as permissible.
	return s.explainRejection(ctx, jti, userID)
}

func (s *MFAChallengeStore) explainRejection(ctx context.Context, jti string, userID uint) error {
	d := s.dialect
	var (
		attempts   int
		consumedAt sql.NullTime
		expiresAt  time.Time
		owner      uint
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, attempts, consumed_at, expires_at FROM mfa_challenges WHERE jti = `+d.Placeholder(1),
		jti).Scan(&owner, &attempts, &consumedAt, &expiresAt)
	if err != nil {
		return ErrMFAChallengeNotFound
	}
	if owner != userID {
		// The token's subject does not own this challenge.
		return ErrMFAChallengeNotFound
	}
	if consumedAt.Valid {
		return ErrMFAChallengeConsumed
	}
	if !expiresAt.After(s.now()) {
		return ErrMFAChallengeExpired
	}
	if attempts >= MFAMaxChallengeAttempts {
		return ErrMFAChallengeExhausted
	}
	return ErrMFAChallengeNotFound
}

// Consume marks a challenge completed. It is the single-use gate: the guarded
// UPDATE succeeds exactly once, so two concurrent successful verifications
// cannot both issue tokens, and a replay after success is refused.
func (s *MFAChallengeStore) Consume(ctx context.Context, jti string, userID uint) error {
	d := s.dialect
	now := s.now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE mfa_challenges SET consumed_at = `+d.Placeholder(1)+
			` WHERE jti = `+d.Placeholder(2)+` AND user_id = `+d.Placeholder(3)+
			` AND consumed_at IS NULL AND expires_at > `+d.Placeholder(4),
		now, jti, userID, now)
	if err != nil {
		return fmt.Errorf("consume mfa challenge: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume mfa challenge: %w", err)
	}
	if n != 1 {
		return ErrMFAChallengeConsumed
	}
	return nil
}

// Purge removes challenges that expired before the cutoff. Bounded
// housekeeping so the table cannot grow without limit.
func (s *MFAChallengeStore) Purge(ctx context.Context) error {
	d := s.dialect
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM mfa_challenges WHERE expires_at < `+d.Placeholder(1),
		s.now().Add(-24*time.Hour))
	return err
}
