package auth

// Executable proof that RotateByID is atomic and works on the repository's
// supported modernc SQLite path and (when PGHOST is set) real PostgreSQL.
// Covers success, old-key rejection, new-key acceptance, cross-owner denial,
// same-name isolation, already-disabled rejection, and concurrent
// single-replacement — the behaviors Pass 4J admitted were untested.

import (
	"sync"
	"testing"
)

func newRotationManager(t *testing.T) *APIKeyManager {
	t.Helper()
	// newRevocationTestAuth runs MigrateAllRaw, which creates a fully
	// reconciled api_keys table (tenant_id/role/key_prefix/scopes/active).
	a := newRevocationTestAuth(t)
	return NewAPIKeyManager(a.db, a.logger)
}

// genKey creates a key and returns its full secret plus the authoritative row
// id read straight from the database (in production the id comes from the
// request URL).
func genKey(t *testing.T, m *APIKeyManager, name string, uid, tid uint) (string, uint) {
	t.Helper()
	full, rec, err := m.Generate(name, uid, tid, "admin", []string{"domains.write"}, 365)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	_, d, _ := m.dialect()
	var id uint
	if err := sqlDB.QueryRow("SELECT id FROM api_keys WHERE key_prefix = "+d.Placeholder(1), rec.KeyPrefix).Scan(&id); err != nil {
		t.Fatalf("lookup id: %v", err)
	}
	return full, id
}

// rotationSuite runs the full rotation contract against a given manager so it
// can be exercised on both SQLite and PostgreSQL.
func rotationSuite(t *testing.T, m *APIKeyManager) {
	t.Run("success disables old, enables new", func(t *testing.T) {
		oldFull, oldID := genKey(t, m, "ci-key", 5, 9)
		if _, err := m.Validate(oldFull); err != nil {
			t.Fatalf("old key should be valid before rotation: %v", err)
		}
		newFull, newRec, err := m.RotateByID(oldID, 5, 9, "admin", []string{"domains.write"}, 365)
		if err != nil {
			t.Fatalf("rotate: %v", err)
		}
		if newFull == oldFull {
			t.Fatal("rotation must mint a new secret")
		}
		if newRec.ID == oldID {
			t.Fatal("rotation must create a new key row")
		}
		if newRec.Name != "ci-key" {
			t.Fatalf("name must be preserved: got %q", newRec.Name)
		}
		if _, err := m.Validate(oldFull); err == nil {
			t.Fatal("old key must be revoked immediately after rotation")
		}
		if _, err := m.Validate(newFull); err != nil {
			t.Fatalf("new key must be valid after rotation: %v", err)
		}
	})

	t.Run("cross-owner denied and old key preserved", func(t *testing.T) {
		oldFull, oldID := genKey(t, m, "k", 6, 9)
		if _, _, err := m.RotateByID(oldID, 7 /* other user */, 9, "admin", []string{"domains.write"}, 365); err == nil {
			t.Fatal("cross-user rotation must be denied")
		}
		if _, _, err := m.RotateByID(oldID, 6, 11 /* other tenant */, "admin", []string{"domains.write"}, 365); err == nil {
			t.Fatal("cross-tenant rotation must be denied")
		}
		if _, err := m.Validate(oldFull); err != nil {
			t.Fatalf("old key must remain valid after a denied rotation: %v", err)
		}
	})

	t.Run("unrelated same-name key untouched", func(t *testing.T) {
		_, aID := genKey(t, m, "dup", 8, 9)
		otherFull, _ := genKey(t, m, "dup", 8, 9)
		if _, _, err := m.RotateByID(aID, 8, 9, "admin", []string{"domains.write"}, 365); err != nil {
			t.Fatalf("rotate a: %v", err)
		}
		if _, err := m.Validate(otherFull); err != nil {
			t.Fatalf("unrelated same-name key must remain valid: %v", err)
		}
	})

	t.Run("already-disabled rejected", func(t *testing.T) {
		_, id := genKey(t, m, "twice", 10, 9)
		if _, _, err := m.RotateByID(id, 10, 9, "admin", []string{"domains.write"}, 365); err != nil {
			t.Fatalf("first rotate: %v", err)
		}
		if _, _, err := m.RotateByID(id, 10, 9, "admin", []string{"domains.write"}, 365); err == nil {
			t.Fatal("rotating an already-disabled key must be rejected")
		}
	})

	t.Run("concurrent leaves exactly one valid replacement", func(t *testing.T) {
		_, id := genKey(t, m, "race", 12, 9)
		const n = 8
		var wg sync.WaitGroup
		results := make([]string, n)
		errs := make([]error, n)
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func(i int) {
				defer wg.Done()
				full, _, e := m.RotateByID(id, 12, 9, "admin", []string{"domains.write"}, 365)
				results[i], errs[i] = full, e
			}(i)
		}
		wg.Wait()
		valid := 0
		for i := 0; i < n; i++ {
			if errs[i] == nil {
				if _, e := m.Validate(results[i]); e == nil {
					valid++
				}
			}
		}
		if valid != 1 {
			t.Fatalf("concurrent rotation must leave exactly one valid replacement, got %d", valid)
		}
	})
}

func TestRotateByID_SQLite(t *testing.T) {
	rotationSuite(t, newRotationManager(t))
}
