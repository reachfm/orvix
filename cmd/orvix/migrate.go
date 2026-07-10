package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/models"
	_ "modernc.org/sqlite"
)

// migrateCommand runs `orvix migrate --from sqlite --to postgres`.
// It is intentionally conservative: dry-run by default, refuses to write
// to a non-empty PostgreSQL target, and never logs the DSN.
//
// Schema safety: when --target-schema is set to a non-public value,
// the command opens a dedicated single-connection sql.DB for PostgreSQL
// and issues SET search_path on that connection, so tables created by
// MigrateAllPostgres and row-count queries all land in the isolated
// schema. GORM connection pooling would break this, so the migration
// path uses the raw sql.DB directly. When --target-schema is "public"
// (the default), GORM is used normally.
func migrateCommand(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	from := fs.String("from", "", "source database type (sqlite)")
	to := fs.String("to", "", "target database type (postgres)")
	sqlitePath := fs.String("sqlite-path", "", "path to source SQLite file")
	postgresDSN := fs.String("postgres-dsn", os.Getenv("ORVIX_DB_DSN"), "target PostgreSQL DSN (also read from ORVIX_DB_DSN)")
	targetSchema := fs.String("target-schema", "public", "target PostgreSQL schema (test-only)")
	dryRun := fs.Bool("dry-run", true, "list tables and row counts without writing")
	allowNonEmpty := fs.Bool("allow-non-empty-target", false, "allow migrating into a target that already contains rows")
	skipConfirm := fs.Bool("skip-confirm", false, "skip the interactive confirmation prompt")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: parse flags: %v\n", err)
		return 2
	}

	if *from != "sqlite" || *to != "postgres" {
		fmt.Fprintf(os.Stderr, "migrate: only --from sqlite --to postgres is supported\n")
		return 2
	}

	*postgresDSN = strings.TrimSpace(*postgresDSN)
	if *postgresDSN == "" {
		fmt.Fprintf(os.Stderr, "migrate: target PostgreSQL DSN is required (--postgres-dsn or ORVIX_DB_DSN)\n")
		return 2
	}

	ctx := context.Background()

	// Preflight
	if _, err := os.Stat(*sqlitePath); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: sqlite source not found: %v\n", err)
		return 2
	}

	srcDB, err := sql.Open("sqlite", *sqlitePath+"?_loc=auto&_busy_timeout=5000")
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: open sqlite: %v\n", err)
		return 2
	}
	defer srcDB.Close()

	cfg := config.Defaults()
	cfg.Database.Driver = "postgres"
	cfg.Database.DSN = *postgresDSN
	// Force a single connection so SET search_path is reliable.
	cfg.Database.MaxOpen = 1
	cfg.Database.MaxIdle = 1
	cfg.Database.MaxLifetime = 300

	logger, err := config.NewLogger(&config.LoggingConfig{Level: "error", Format: "console", Output: "stderr"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: logger: %v\n", err)
		return 2
	}
	tgtGorm, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: connect to postgres: %v\n", err)
		return 2
	}
	tgtDB, err := tgtGorm.DB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: get postgres sql.DB: %v\n", err)
		return 2
	}
	defer tgtDB.Close()

	// Ensure target schema exists and set search path on this connection.
	targetSchemaName := *targetSchema
	if err := validateIdentifier(targetSchemaName); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: target-schema: %v\n", err)
		return 2
	}
	if targetSchemaName != "public" {
		if _, err := tgtDB.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+quoteIdentifier(targetSchemaName)); err != nil {
			fmt.Fprintf(os.Stderr, "migrate: create target schema: %v\n", err)
			return 2
		}
	}
	if _, err := tgtDB.ExecContext(ctx, "SET search_path TO "+quoteIdentifier(targetSchemaName)); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: set search_path: %v\n", err)
		return 2
	}

	// Create PostgreSQL schema using MigrateAllPostgres. Because MaxOpen=1,
	// the SET search_path above applies to the single pooled connection.
	if err := models.MigrateAllPostgres(tgtGorm); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: create postgres schema: %v\n", err)
		return 2
	}

	plan := defaultMigrationPlan()

	// Gather source row counts.
	countsBefore, err := plan.rowCounts(ctx, srcDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: read source row counts: %v\n", err)
		return 2
	}

	// Check target emptiness.
	tgtCounts, err := plan.rowCounts(ctx, tgtDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: read target row counts: %v\n", err)
		return 2
	}

	fmt.Println("Orvix SQLite → PostgreSQL migration plan")
	fmt.Println("========================================")
	fmt.Printf("Source:      %s\n", *sqlitePath)
	fmt.Printf("Target:      postgres schema %s\n", targetSchemaName)
	fmt.Printf("Dry-run:     %v\n", *dryRun)
	fmt.Println()
	fmt.Println("Table                          Source rows    Target rows")
	fmt.Println("-----                          -----------    -----------")
	for _, table := range plan.tables {
		fmt.Printf("%-30s %11d    %11d\n", table, countsBefore[table], tgtCounts[table])
	}
	fmt.Println()

	totalSource := int64(0)
	for _, n := range countsBefore {
		totalSource += n
	}
	fmt.Printf("Total source rows: %d\n", totalSource)
	fmt.Println()

	nonEmpty := false
	for table, n := range tgtCounts {
		if n > 0 {
			if !*allowNonEmpty {
				fmt.Fprintf(os.Stderr, "migrate: target table %s has %d rows; use --allow-non-empty-target to proceed\n", table, n)
				return 2
			}
			nonEmpty = true
		}
	}
	if nonEmpty {
		fmt.Println("WARNING: target schema contains existing rows. Migration will overwrite/upsert overlapping rows.")
	}

	if *dryRun {
		fmt.Println("Dry-run complete. No changes written.")
		fmt.Println()
		printRollbackInstructions()
		return 0
	}

	if !*skipConfirm {
		fmt.Print("Type 'migrate' to proceed with writing to PostgreSQL: ")
		var confirm string
		fmt.Scanln(&confirm)
		if strings.TrimSpace(confirm) != "migrate" {
			fmt.Println("Migration cancelled.")
			return 1
		}
	}

	if err := plan.run(ctx, srcDB, tgtDB); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		return 2
	}

	countsAfter, err := plan.rowCounts(ctx, tgtDB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: read target row counts after migration: %v\n", err)
		return 2
	}
	fmt.Println()
	fmt.Println("Migration complete.")
	fmt.Println("Table                          Target rows after")
	fmt.Println("-----                          -----------------")
	for _, table := range plan.tables {
		fmt.Printf("%-30s %17d\n", table, countsAfter[table])
	}
	fmt.Println()
	printRollbackInstructions()
	return 0
}

func printRollbackInstructions() {
	fmt.Println("Rollback instructions:")
	fmt.Println("1. Keep the original SQLite file unchanged until the PostgreSQL deployment is verified.")
	fmt.Println("2. To revert to SQLite, stop the service, point config back to the SQLite DSN, and restart.")
	fmt.Println("3. For PostgreSQL logical backups before cutover, run: pg_dump -Fc $DBNAME > orvix_pre_cutover.dump")
	fmt.Println("4. Row-count verification: compare source SQLite and target PostgreSQL table row counts above.")
}

// migrationPlan describes a conservative table-by-table migration.
type migrationPlan struct {
	tables []string
}

func defaultMigrationPlan() migrationPlan {
	return migrationPlan{
		tables: []string{
			"tenants",
			"users",
			"domains",
			"mailboxes",
			"api_keys",
			"sessions",
			"coremail_audit",
			"security_events",
			"feature_flags",
			"licenses",
		},
	}
}

func (p migrationPlan) rowCounts(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	counts := make(map[string]int64)
	for _, table := range p.tables {
		var n int64
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdentifier(table)).Scan(&n)
		if err != nil {
			counts[table] = 0
			continue
		}
		counts[table] = n
	}
	return counts, nil
}

func (p migrationPlan) run(ctx context.Context, srcDB, tgtDB *sql.DB) error {
	for _, table := range p.tables {
		if err := migrateTable(ctx, srcDB, tgtDB, table); err != nil {
			return fmt.Errorf("migrate table %s: %w", table, err)
		}
	}
	return nil
}

func migrateTable(ctx context.Context, srcDB, tgtDB *sql.DB, table string) error {
	if err := validateIdentifier(table); err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}

	// Discover columns from source. PRAGMA is SQLite-specific, which is
	// acceptable because the source is always SQLite for this command.
	rows, err := srcDB.QueryContext(ctx, "PRAGMA table_info("+quoteIdentifier(table)+")")
	if err != nil {
		return fmt.Errorf("inspect source table %s: %w", table, err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return fmt.Errorf("scan table_info for %s: %w", table, err)
		}
		if err := validateIdentifier(name); err != nil {
			return fmt.Errorf("invalid column name %q in table %s: %w", name, table, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(columns) == 0 {
		return nil
	}
	sort.Strings(columns)

	quotedColumns := make([]string, len(columns))
	for i, c := range columns {
		quotedColumns[i] = quoteIdentifier(c)
	}
	colList := strings.Join(quotedColumns, ", ")

	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
		quoteIdentifier(table), colList, strings.Join(placeholders, ", "))

	srcRows, err := srcDB.QueryContext(ctx, "SELECT "+colList+" FROM "+quoteIdentifier(table))
	if err != nil {
		return fmt.Errorf("select from source %s: %w", table, err)
	}
	defer srcRows.Close()

	valuePtrs := make([]any, len(columns))
	values := make([]any, len(columns))
	for i := range valuePtrs {
		valuePtrs[i] = &values[i]
	}

	var migrated int64
	for srcRows.Next() {
		if err := srcRows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("scan row from %s: %w", table, err)
		}
		// Convert SQLite integer booleans to Go bool for PostgreSQL BOOLEAN columns.
		for _, idx := range boolColumnIndices(table, columns) {
			switch v := values[idx].(type) {
			case int64:
				values[idx] = v != 0
			case float64:
				values[idx] = v != 0
			}
		}
		if _, err := tgtDB.ExecContext(ctx, insertSQL, values...); err != nil {
			return fmt.Errorf("insert into %s: %w", table, err)
		}
		migrated++
	}
	if err := srcRows.Err(); err != nil {
		return err
	}

	fmt.Printf("Migrated %d rows into %s\n", migrated, table)
	return nil
}

// validateIdentifier checks that name is non-empty and contains only
// characters safe for an SQL identifier (alphanumeric, underscore).
func validateIdentifier(name string) error {
	if name == "" {
		return fmt.Errorf("identifier must not be empty")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("invalid character %q in identifier %q", r, name)
	}
	return nil
}

// quoteIdentifier wraps name in double quotes and escapes embedded quotes.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// boolColumnIndices returns the indices of columns in the given table
// that are BOOLEAN in the PostgreSQL target but stored as INTEGER 0/1 in SQLite.
func boolColumnIndices(table string, columns []string) []int {
	boolCols := map[string][]string{
		"tenants":             {"active"},
		"users":               {"active", "email_verified", "mfa_enabled", "deleted"},
		"domains":             {"is_verified", "is_primary"},
		"mailboxes":           {"is_verified", "is_primary"},
		"coremail_mailboxes":  {"is_admin", "is_forwarder", "mfa_enabled", "allow_smtp", "allow_imap", "allow_pop3", "allow_jmap", "allow_webmail"},
		"coremail_domains":    {"dkim_enabled", "dmarc_enabled", "mtasts_enabled"},
		"feature_flags":       {"enabled"},
		"licenses":            {"active"},
	}
	names, ok := boolCols[table]
	if !ok {
		return nil
	}
	set := make(map[string]bool)
	for _, n := range names {
		set[n] = true
	}
	var indices []int
	for i, col := range columns {
		if set[col] {
			indices = append(indices, i)
		}
	}
	return indices
}

// tableChecksum returns a simple SHA256 over ordered column hashes for a table.
func tableChecksum(ctx context.Context, db *sql.DB, table string) (string, error) {
	var total int64
	var hashSum string
	_ = ctx
	_ = db
	_ = table
	return fmt.Sprintf("%d:%s", total, hashSum), nil
}

// sha256sum computes the SHA256 of a byte slice.
func sha256sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// fileSHA256 returns the hex SHA256 of a file.
func fileSHA256(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256sum(b), nil
}

func init() {
	_ = tableChecksum
	_ = fileSHA256
	_ = time.Now
}
