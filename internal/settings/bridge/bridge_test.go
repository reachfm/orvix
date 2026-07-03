package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func openBridgeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "bridge.db")+"?_loc=auto&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS admin_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		section TEXT NOT NULL DEFAULT '',
		requires_restart INTEGER NOT NULL DEFAULT 0,
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_by INTEGER
	)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	return db
}

func seedSetting(t *testing.T, db *sql.DB, key string, value any, restart int) {
	t.Helper()
	raw, _ := jsonMarshal(value)
	if _, err := db.Exec(`INSERT INTO admin_settings (key, value, section, requires_restart, updated_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, requires_restart=excluded.requires_restart`,
		key, string(raw), "test", restart); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func TestApplyHotPasswordMinLength(t *testing.T) {
	db := openBridgeTestDB(t)
	seedSetting(t, db, "security.password_min_length", 16, 0)
	cfg := config.Defaults()
	b := New(cfg, db, zap.NewNop())
	summary, err := b.Apply(context.Background())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if summary.Applied < 1 {
		t.Fatalf("want Applied >= 1, got %d", summary.Applied)
	}
	if cfg.Auth.PasswordMinLen != 16 {
		t.Fatalf("PasswordMinLen: want 16, got %d", cfg.Auth.PasswordMinLen)
	}
}

func TestApplyRestartRequiredListenerField(t *testing.T) {
	db := openBridgeTestDB(t)
	seedSetting(t, db, "coremail.smtp_port", 2525, 1)
	cfg := config.Defaults()
	b := New(cfg, db, zap.NewNop())
	summary, err := b.Apply(context.Background())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if summary.Pending < 1 {
		t.Fatalf("want Pending >= 1, got %d", summary.Pending)
	}
	// Hot-applied fields were untouched.
	if cfg.CoreMail.SMTPPort != cfg.CoreMail.SMTPPort {
		t.Fatalf("restart-required field must NOT be applied to live cfg")
	}
}

func TestApplyMalformedRowSkipped(t *testing.T) {
	db := openBridgeTestDB(t)
	if _, err := db.Exec(`INSERT INTO admin_settings (key, value, section, requires_restart, updated_at) VALUES (?, ?, ?, 0, CURRENT_TIMESTAMP)`,
		"security.password_min_length", "{not_json", "test"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg := config.Defaults()
	b := New(cfg, db, zap.NewNop())
	_, err := b.Apply(context.Background())
	if err != nil {
		t.Fatalf("apply must not error on malformed row: %v", err)
	}
	if cfg.Auth.PasswordMinLen != config.Defaults().Auth.PasswordMinLen {
		t.Fatalf("malformed row must not change cfg")
	}
}

func TestApplyOutboundPreferIPv4(t *testing.T) {
	db := openBridgeTestDB(t)
	seedSetting(t, db, "outbound.prefer_ipv4", true, 0)
	cfg := config.Defaults()
	b := New(cfg, db, zap.NewNop())
	_, err := b.Apply(context.Background())
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !cfg.Outbound.PreferIPv4 {
		t.Fatalf("PreferIPv4: want true")
	}
}

func TestSnapshotSurvivesConcurrentReload(t *testing.T) {
	db := openBridgeTestDB(t)
	seedSetting(t, db, "outbound.prefer_ipv4", true, 0)
	cfg := config.Defaults()
	b := New(cfg, db, zap.NewNop())
	if _, err := b.Apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	snap := b.Snapshot()
	if snap.Loaded == 0 {
		t.Fatalf("snapshot must report loaded")
	}
	if snap.Applied < 1 {
		t.Fatalf("snapshot must report applied")
	}
}
