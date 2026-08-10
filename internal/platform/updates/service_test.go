package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestService(t *testing.T, pub ed25519.PublicKey) *Service {
	t.Helper()
	db := testDB(t)
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	v := NewVerifier(pub)
	return NewService(repo, v, t.TempDir(), "1.0.0", nil, nil, nil)
}

func buildSignedManifest(t *testing.T, priv ed25519.PrivateKey, artifact []byte, version, platform, arch string) (manifestJSON, signature []byte, m Manifest) {
	t.Helper()
	m = Manifest{Version: version, Platform: platform, Arch: arch, SHA256: sha256Hex(artifact)}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	sig := ed25519.Sign(priv, b)
	return b, sig, m
}

func TestSubmitArtifact_ValidSignedArtifact_Staged(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("orvix-binary-contents-v2")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "linux", "amd64")

	rec, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if rec.State != StateStaged {
		t.Fatalf("expected Staged, got %s", rec.State)
	}
	if rec.ArtifactPath == "" {
		t.Fatal("expected artifact to be staged to disk")
	}
	if filepath.Ext(rec.ArtifactPath) != ".artifact" {
		t.Fatalf("unexpected staged path: %s", rec.ArtifactPath)
	}
}

func TestSubmitArtifact_Unsigned_Rejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, _, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "linux", "amd64")

	_, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, nil, "2.0.0", "linux", "amd64", 1)
	if err != ErrUnsigned {
		t.Fatalf("expected ErrUnsigned, got %v", err)
	}
}

func TestSubmitArtifact_TamperedArtifact_Rejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "linux", "amd64")

	tampered := append([]byte{}, artifact...)
	tampered[0] ^= 0xFF // flip a byte after signing

	_, err := svc.SubmitArtifact(context.Background(), tampered, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != ErrHashMismatch {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
}

func TestSubmitArtifact_InvalidSignature_Rejected(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)       // trusted key
	_, wrongPriv, _ := ed25519.GenerateKey(nil) // signer NOT trusted
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, wrongPriv, artifact, "2.0.0", "linux", "amd64")

	_, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestSubmitArtifact_WrongVersion_Rejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "9.9.9", "linux", "amd64")

	_, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != ErrVersionMismatch {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}
}

func TestSubmitArtifact_WrongPlatform_Rejected(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "windows", "amd64")

	_, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != ErrPlatformMismatch {
		t.Fatalf("expected ErrPlatformMismatch, got %v", err)
	}
}

func TestTriggerApply_NoCoordinator_LeavesStaged(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "linux", "amd64")
	rec, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	_, _, err = svc.TriggerApply(context.Background(), rec.ID, nil, 1)
	if err != ErrNoCoordinator {
		t.Fatalf("expected ErrNoCoordinator, got %v", err)
	}
	got, err := svc.GetStatus(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if got.State != StateStaged {
		t.Fatalf("expected record to remain Staged without a coordinator, got %s", got.State)
	}
}

type fakeCoordinator struct{ jobID string }

func (f fakeCoordinator) Submit(ctx context.Context, artifactPath, version string) (string, error) {
	return f.jobID, nil
}

func TestTriggerApply_WithCoordinator_TransitionsToApplied(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	svc := newTestService(t, pub)
	artifact := []byte("payload")
	manifestJSON, sig, _ := buildSignedManifest(t, priv, artifact, "2.0.0", "linux", "amd64")
	rec, err := svc.SubmitArtifact(context.Background(), artifact, manifestJSON, sig, "2.0.0", "linux", "amd64", 1)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	applied, jobID, err := svc.TriggerApply(context.Background(), rec.ID, fakeCoordinator{jobID: "job-1"}, 1)
	if err != nil {
		t.Fatalf("trigger apply: %v", err)
	}
	if jobID != "job-1" {
		t.Fatalf("unexpected job id: %s", jobID)
	}
	if applied.State != StateApplied {
		t.Fatalf("expected Applied, got %s", applied.State)
	}

	rolledBack, err := svc.Rollback(context.Background(), rec.ID, "regression found", 1)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rolledBack.State != StateRolledBack {
		t.Fatalf("expected RolledBack, got %s", rolledBack.State)
	}
	if rolledBack.PrevVersion != "1.0.0" {
		t.Fatalf("expected prev version 1.0.0, got %s", rolledBack.PrevVersion)
	}
}
