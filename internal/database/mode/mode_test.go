package mode

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	_ "modernc.org/sqlite"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
	}{
		{"sqlite", ModeSQLite},
		{"sqlite3", ModeSQLite},
		{"SQLite", ModeSQLite},
		{"  sqlite ", ModeSQLite},
		{"postgres", ModePostgres},
		{"postgresql", ModePostgres},
		{"Postgres", ModePostgres},
		{"", ModeUnknown},
		{"mysql", ModeUnknown},
		{"cockroachdb", ModeUnknown},
		{"  ", ModeUnknown},
	}
	for _, c := range cases {
		got := Parse(c.in)
		if got != c.want {
			t.Errorf("Parse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModeString(t *testing.T) {
	if ModeSQLite.String() != "sqlite" {
		t.Errorf("ModeSQLite.String() = %q, want sqlite", ModeSQLite.String())
	}
	if ModePostgres.String() != "postgres" {
		t.Errorf("ModePostgres.String() = %q, want postgres", ModePostgres.String())
	}
	if ModeUnknown.String() != "unknown" {
		t.Errorf("ModeUnknown.String() = %q, want unknown", ModeUnknown.String())
	}
}

func TestValidateDriverDSN_DevAcceptsAnything(t *testing.T) {
	positive := []struct{ driver, dsn string }{
		{"sqlite", "/tmp/db"},
		{"postgres", "host=localhost user=u dbname=d"},
	}
	for _, c := range positive {
		if err := ValidateDriverDSN(c.driver, c.dsn, false); err != nil {
			t.Errorf("dev mode should accept (%q, %q), got: %v", c.driver, c.dsn, err)
		}
	}
	// Negative cases still error in dev mode.
	if err := ValidateDriverDSN("sqlite", "", false); err == nil {
		t.Errorf("empty DSN must still error")
	}
	if err := ValidateDriverDSN("", "", false); err == nil {
		t.Errorf("unknown driver must still error")
	}
}

func TestValidateDriverDSN_RejectsUnknownDriver(t *testing.T) {
	err := ValidateDriverDSN("mysql", "anything", false)
	if err == nil {
		t.Fatal("expected error for mysql driver")
	}
	if !strings.Contains(err.Error(), "unsupported database driver") {
		t.Errorf("error should mention unsupported driver, got: %v", err)
	}
}

func TestValidateDriverDSN_RejectsEmptyDSN(t *testing.T) {
	err := ValidateDriverDSN("sqlite", "", false)
	if err == nil {
		t.Fatal("expected error for empty DSN")
	}
	if !strings.Contains(err.Error(), "DSN is empty") {
		t.Errorf("error should mention empty DSN, got: %v", err)
	}
}

func TestValidateDriverDSN_PostgresAcceptedShapes(t *testing.T) {
	cases := []string{
		"postgres://user:pw@host:5432/db",
		"postgresql://user:pw@host/db",
		"host=localhost port=5432 user=u dbname=d sslmode=disable",
	}
	for _, dsn := range cases {
		if err := ValidateDriverDSN("postgres", dsn, false); err != nil {
			t.Errorf("DSN %q should validate, got: %v", dsn, err)
		}
	}
}

func TestValidateDriverDSN_PostgresRejectsEmptyPayload(t *testing.T) {
	err := ValidateDriverDSN("postgres", "garbage no host no nothing", false)
	if err == nil {
		t.Fatal("expected error for postgres DSN without host= or postgres://")
	}
}

func TestValidateDriverDSN_SQLiteAcceptsBarePath(t *testing.T) {
	err := ValidateDriverDSN("sqlite", "/var/lib/orvix/orvix.db", false)
	if err != nil {
		t.Errorf("bare sqlite path should validate, got: %v", err)
	}
}

func TestValidateDriverDSN_SQLiteRejectsNUL(t *testing.T) {
	err := ValidateDriverDSN("sqlite", "abc\x00def", false)
	if err == nil {
		t.Fatal("expected error for NUL byte in sqlite DSN")
	}
	if !strings.Contains(err.Error(), "NUL") {
		t.Errorf("error should mention NUL, got: %v", err)
	}
}

func TestValidateDriverDSN_ProductionAcceptsPostgres(t *testing.T) {
	if err := ValidateDriverDSN("postgres", "host=localhost user=u dbname=d", true); err != nil {
		t.Errorf("production with postgres should be accepted, got: %v", err)
	}
}

func TestValidateDriverDSN_ProductionRejectsSQLite(t *testing.T) {
	err := ValidateDriverDSN("sqlite", "/var/lib/orvix/orvix.db", true)
	if err == nil {
		t.Fatal("production with sqlite MUST be rejected")
	}
	if !strings.Contains(err.Error(), "production") {
		t.Errorf("error should mention production, got: %v", err)
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("error should mention postgres, got: %v", err)
	}
}

func TestValidateDriverDSN_ProductionRejectsUnknown(t *testing.T) {
	err := ValidateDriverDSN("mysql", "user:pw@tcp(host)/db", true)
	if err == nil {
		t.Fatal("production with mysql MUST be rejected")
	}
}

func TestValidateDriverDSN_RunsBothChecks(t *testing.T) {
	// Order: DSN first, then production safety.
	if err := ValidateDriverDSN("mysql", "", true); err == nil {
		t.Fatal("expected error for unknown driver")
	}
	// SQLite + production: production safety error wins (DSN is fine).
	err := ValidateDriverDSN("sqlite", "/tmp/x.db", true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "production") {
		t.Errorf("error should mention production, got: %v", err)
	}
}

func TestPoolDefaults(t *testing.T) {
	open, idle, life := PoolDefaults(ModePostgres)
	if open == 0 || idle == 0 || life == 0 {
		t.Errorf("postgres defaults should be non-zero, got open=%d idle=%d life=%d", open, idle, life)
	}
	open, idle, life = PoolDefaults(ModeSQLite)
	if open != 1 || idle != 1 {
		t.Errorf("sqlite defaults should be 1/1, got open=%d idle=%d life=%d", open, idle, life)
	}
}

func TestCheckHealth_NilDB(t *testing.T) {
	h := CheckHealth(context.Background(), nil, HealthInputs{Driver: "sqlite"})
	if h.Connected {
		t.Errorf("Connected should be false when db is nil")
	}
	if h.PingError == "" {
		t.Errorf("PingError should be set when db is nil")
	}
}

func TestCheckHealth_NilDriverIsUnknown(t *testing.T) {
	db := openTestSQLite(t, "")
	defer closeDB(t, db)
	h := CheckHealth(context.Background(), db, HealthInputs{})
	if h.Mode != ModeUnknown {
		t.Errorf("Mode should be Unknown when Driver is empty, got %q", h.Mode)
	}
}

func TestCheckHealth_LiveSQLite(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "health.db")
	db := openTestSQLite(t, tmp)
	defer closeDB(t, db)
	h := CheckHealth(context.Background(), db, HealthInputs{
		Driver:       "sqlite",
		IsProduction: false,
		MaxOpen:      1,
		MaxIdle:      1,
	})

	if !h.Connected {
		t.Errorf("Connected should be true for a live sqlite db, ping error: %s", h.PingError)
	}
	if h.Mode != ModeSQLite {
		t.Errorf("Mode = %q, want sqlite", h.Mode)
	}
	if h.Driver != "sqlite" {
		t.Errorf("Driver = %q, want sqlite", h.Driver)
	}
	if h.Production {
		t.Errorf("Production should be false in dev mode")
	}
	if h.CheckedAt.IsZero() {
		t.Errorf("CheckedAt must be set")
	}
	if h.SchemaVersion != 0 {
		t.Errorf("SchemaVersion should be 0 when schema_migrations is missing, got %d", h.SchemaVersion)
	}
}

func TestCheckHealth_SchemaVersionFromMigrations(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "schema.db")
	db := openTestSQLite(t, tmp)
	defer closeDB(t, db)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB(): %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := sqlDB.Exec(`INSERT INTO schema_migrations (version) VALUES (1), (3), (2)`); err != nil {
		t.Fatalf("insert versions: %v", err)
	}
	h := CheckHealth(context.Background(), db, HealthInputs{Driver: "sqlite"})
	if h.SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3 (max of 1,3,2)", h.SchemaVersion)
	}
}

func TestSafeErr(t *testing.T) {
	if safeErr(nil) != "" {
		t.Errorf("safeErr(nil) should return empty string")
	}
	if got := safeErr(sql.ErrNoRows); !strings.Contains(got, "no rows") {
		t.Errorf("safeErr should pass through normal errors, got: %q", got)
	}
}

// openTestSQLite opens a real sqlite database for health-check testing.
// Uses modernc.org/sqlite directly to avoid importing internal/config
// (which would create an import cycle).
func openTestSQLite(t *testing.T, path string) *gorm.DB {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "test.db")
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		t.Fatalf("ping sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	gdb, err := gorm.Open(internalDialector{}, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		sqlDB.Close()
		t.Fatalf("gorm.Open: %v", err)
	}
	gdb.ConnPool = sqlDB
	return gdb
}

// internalDialector is a minimal GORM dialector for sqlite so the
// test can build a *gorm.DB without pulling in internal/config (which
// would import this package and create a cycle).
type internalDialector struct{}

func (internalDialector) Name() string                          { return "sqlite" }
func (internalDialector) Initialize(*gorm.DB) error             { return nil }
func (internalDialector) Migrator(*gorm.DB) gorm.Migrator       { return nil }
func (internalDialector) DataTypeOf(field *schema.Field) string { return "text" }
func (internalDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}
func (internalDialector) BindVarTo(w clause.Writer, _ *gorm.Statement, _ interface{}) {
	w.WriteByte('?')
}
func (internalDialector) QuoteTo(w clause.Writer, str string) {
	w.WriteByte('"')
	w.WriteString(str)
	w.WriteByte('"')
}
func (internalDialector) Explain(sql string, _ ...interface{}) string { return sql }

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	sqlDB.Close()
}
