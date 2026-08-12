package relay

// PostgreSQL portability suite for the Phase B relay administration
// SQL. Mirrors the repo's established PostgreSQL test convention:
// gated on PGHOST + ORVIX_RUN_POSTGRES_DML_TEST=1 (see
// internal/auth/pg_engine_test.go) so local SQLite runs skip them,
// while the PostgreSQL Runtime DML workflow executes them against a
// real server. This proves the INSERT...RETURNING id path, guarded
// updates, and the additive migrations never use LastInsertId or
// SQLite-only DDL on PostgreSQL.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func newPostgresRelayService(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	host := os.Getenv("PGHOST")
	if host == "" || os.Getenv("ORVIX_RUN_POSTGRES_DML_TEST") != "1" {
		t.Skip("postgres relay: set PGHOST and ORVIX_RUN_POSTGRES_DML_TEST=1 to run")
	}
	port := os.Getenv("PGPORT")
	if port == "" {
		port = "5432"
	}
	dsn := "postgres://" + os.Getenv("PGUSER") + ":" + os.Getenv("PGPASSWORD") + "@" + host + ":" + port + "/" + os.Getenv("PGDATABASE") + "?sslmode=disable"
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db)
	if err := repo.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	auditStore := audit.NewExtendedStore(db)
	if err := auditStore.EnsureTable(context.Background()); err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	svc := NewService(repo, kernel.SystemClock{}, nil).
		WithSecretCodec(fakeSecretCodec{}).
		WithAuditStore(auditStore)
	return db, svc
}

func TestPostgresRelayAdmin_CreateReturnsIDWithoutLastInsertId(t *testing.T) {
	db, svc := newPostgresRelayService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("postgres create must return a real id (RETURNING id)")
	}
	got, err := svc.GetRelay(ctx, r.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.HasCredential != true || got.SecretRef != "" {
		t.Fatal("redaction contract violated on postgres")
	}
	var stored string
	if err := db.QueryRow(`SELECT secret_ref FROM platform_relay_providers WHERE id=$1`, r.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "enc:super-secret" {
		t.Fatalf("expected encrypted-at-rest on postgres, got %q", stored)
	}
}

func TestPostgresRelayAdmin_GuardedTransitionsAndVersion(t *testing.T) {
	_, svc := newPostgresRelayService(t)
	ctx := context.Background()
	r, err := svc.CreateRelay(ctx, baseCreate(), testActor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetRelayActive(ctx, r.ID, false, 1, testActor); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := svc.SetRelayActive(ctx, r.ID, true, 1, testActor); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected version conflict on postgres, got %v", err)
	}
	rotated, generated, err := svc.RotateRelayCredentials(ctx, r.ID, 2, "", testActor)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == nil || generated == "" {
		t.Fatal("rotation must succeed on postgres and generate a credential")
	}
	if err := svc.DeleteRelay(ctx, r.ID, testActor); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetRelay(ctx, r.ID); !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected not-found after delete on postgres, got %v", err)
	}
}

var _ = time.Now
