package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ── Staging failure injection (Fix 2) ─────────────────────────────────

func TestStagingStoreFailureInjection(t *testing.T) {
	data := []byte(`{"entities":[{"entity":"organization","name":"Acme","domain":"acme.test"}]}`)

	cases := []struct {
		name   string
		set    func(*StagingService)
		verify func(t *testing.T, dir string, stagingID string, err error)
	}{
		{
			name: "create temp fails",
			set: func(s *StagingService) {
				s.SetTestFailpoints(struct {
					CreateTemp func() error
					Write      func() error
					Sync       func() error
					Close      func() error
					Rename     func() error
					Remove     func() error
				}{CreateTemp: func() error { return errors.New("injected create temp failure") }})
			},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err == nil {
					t.Fatal("expected store error")
				}
				// No temp, no final file, nothing to clean.
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected no leftover files, got %d", len(entries))
				}
			},
		},
		{
			name: "write fails",
			set: func(s *StagingService) {
				s.SetTestFailpoints(struct {
					CreateTemp func() error
					Write      func() error
					Sync       func() error
					Close      func() error
					Rename     func() error
					Remove     func() error
				}{Write: func() error { return errors.New("injected write failure") }})
			},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err == nil {
					t.Fatal("expected store error")
				}
				// The temp file must have been cleaned up.
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected no leftover files, got %d", len(entries))
				}
			},
		},
		{
			name: "fsync fails",
			set: func(s *StagingService) {
				s.SetTestFailpoints(struct {
					CreateTemp func() error
					Write      func() error
					Sync       func() error
					Close      func() error
					Rename     func() error
					Remove     func() error
				}{Sync: func() error { return errors.New("injected fsync failure") }})
			},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err == nil {
					t.Fatal("expected store error")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected no leftover files, got %d", len(entries))
				}
			},
		},
		{
			name: "close fails",
			set: func(s *StagingService) {
				s.SetTestFailpoints(struct {
					CreateTemp func() error
					Write      func() error
					Sync       func() error
					Close      func() error
					Rename     func() error
					Remove     func() error
				}{Close: func() error { return errors.New("injected close failure") }})
			},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err == nil {
					t.Fatal("expected store error")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected no leftover files, got %d", len(entries))
				}
			},
		},
		{
			name: "rename fails",
			set: func(s *StagingService) {
				s.SetTestFailpoints(struct {
					CreateTemp func() error
					Write      func() error
					Sync       func() error
					Close      func() error
					Rename     func() error
					Remove     func() error
				}{Rename: func() error { return errors.New("injected rename failure") }})
			},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err == nil {
					t.Fatal("expected store error")
				}
				// The temp file must be removed on rename failure.
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("expected no leftover files, got %d", len(entries))
				}
			},
		},
		{
			name: "success leaves exactly one file and verifies",
			set:  func(s *StagingService) {},
			verify: func(t *testing.T, dir, stagingID string, err error) {
				if err != nil {
					t.Fatalf("store: %v", err)
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 1 {
					t.Fatalf("expected exactly 1 staged file, got %d", len(entries))
				}
				svc, _ := NewStagingService(dir)
				if verr := svc.Verify(stagingID, HashSource(data)); verr != nil {
					t.Fatalf("verify: %v", verr)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			svc, err := NewStagingService(dir)
			if err != nil {
				t.Fatal(err)
			}
			tc.set(svc)
			stagingID, _, _, err := svc.Store(data, 0)
			tc.verify(t, dir, stagingID, err)
		})
	}
}

func TestStagingRemoveFailureInjection(t *testing.T) {
	dir := t.TempDir()
	svc, _ := NewStagingService(dir)
	stagingID, _, _, err := svc.Store([]byte("hello"), 0)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetTestFailpoints(struct {
		CreateTemp func() error
		Write      func() error
		Sync       func() error
		Close      func() error
		Rename     func() error
		Remove     func() error
	}{Remove: func() error { return errors.New("injected remove failure") }})
	if err := svc.Remove(stagingID); err == nil {
		t.Fatal("expected remove error")
	}
	// File still on disk (remove failed) — nothing was deleted silently.
	full := filepath.Join(dir, stagingID)
	if _, statErr := os.Stat(full); statErr != nil {
		t.Fatalf("file should still exist after failed remove: %v", statErr)
	}
}

// ── Tamper verification (Fix 3) ──────────────────────────────────────

func TestTamperDetectedBeforeEntityCreation(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)

	// Valid CSV source describing a single organization row.
	data := []byte("entity,name,domain\norganization,Acme,acme.test\n")
	svc := NewService(repo, testAdapters(t, db), staging, nil, nil)

	job, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "tester", SourceType: SourceCSV, SourceName: "acme.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Mutate the staged file after acceptance.
	path := filepath.Join(staging.StagingRoot(), job.StagingID)
	if err := os.WriteFile(path, []byte("entity,name,domain\norganization,EvilCorp,evil.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Validation must fail against the persisted SourceHash.
	if _, err := svc.Validate(context.Background(), job.ID, importTestTenantID, "platform"); err == nil {
		t.Fatal("expected validation to fail after tampering")
	} else if !isHashMismatch(err) {
		t.Fatalf("expected hash mismatch, got %v", err)
	}

	// No business entity may have been created by the tampered run.
	var orgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&orgCount); err != nil {
		t.Fatal(err)
	}
	if orgCount != 0 {
		t.Fatalf("tampered import created entities: %d orgs", orgCount)
	}
}

func TestTamperDetectedOnDurableExecution(t *testing.T) {
	db := setupTestDB(t)
	repo := testRepo(t, db)
	staging := mustStaging(t)

	data := []byte("entity,name,domain\norganization,Acme,acme.test\n")
	svc := NewService(repo, testAdapters(t, db), staging, nil, nil)
	job, err := svc.Create(context.Background(), CreateImportParams{
		TenantID: importTestTenantID, Scope: "platform", Actor: "tester", SourceType: SourceCSV, SourceName: "acme.csv",
	}, data)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Tamper with the staged file.
	path := filepath.Join(staging.StagingRoot(), job.StagingID)
	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The durable handler must reject the tampered source before executing.
	_, err = svc.HandleImportJob(context.Background(), &stubExecution{}, marshalJSON(importJobPayload{ImportID: job.ID}))
	if err == nil {
		t.Fatal("expected durable execution to fail after tampering")
	}

	var orgCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&orgCount); err != nil {
		t.Fatal(err)
	}
	if orgCount != 0 {
		t.Fatalf("tampered durable execution created entities: %d orgs", orgCount)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func mustStaging(t *testing.T) *StagingService {
	t.Helper()
	svc, err := NewStagingService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func isHashMismatch(err error) bool {
	var ie *ImportError
	if errors.As(err, &ie) {
		return ie.Code == CodeHashMismatch
	}
	return false
}

// stubExecution satisfies the jobs.Execution interface minimally for the
// tamper test's durable handler invocation.
type stubExecution struct{}

func (s *stubExecution) TenantID() uint { return 0 }
func (s *stubExecution) Heartbeat(context.Context) error {
	return nil
}
func (s *stubExecution) SetProgress(context.Context, int) error { return nil }
func (s *stubExecution) CancellationRequested(context.Context) (bool, error) {
	return false, nil
}
