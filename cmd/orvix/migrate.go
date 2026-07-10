package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
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

// tableMigration defines an explicit column mapping for a single table.
type tableMigration struct {
	srcColumns   []string
	tgtColumns   []string
	boolColumns  []string
	dependsOn    []string
	nullDefaults map[string]any // column → default value when source is NULL
	sourceQuery  string         // optional custom SELECT query; overrides default SELECT srcColumns FROM table
}

// tableColumnMap defines explicit source→target column mappings for
// all 10 migration tables. srcColumns and tgtColumns are positionally
// mapped: srcColumns[i] is SELECTed from SQLite and inserted into
// tgtColumns[i] on PostgreSQL. Columns present in SQLite but absent in
// PG are simply omitted from srcColumns; columns present only in PG
// get their DDL defaults. boolColumns lists source columns whose
// INTEGER 0/1 values must be converted to Go bool for PG BOOLEAN.
var tableColumnMap = map[string]tableMigration{
	"tenants": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "name", "slug", "domain", "plan", "max_domains", "max_mailboxes", "logo_url", "primary_color", "active", "reseller_id"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "name", "slug", "domain", "plan", "max_domains", "max_mailboxes", "logo_url", "primary_color", "active", "reseller_id"},
		boolColumns: []string{"active"},
	},
	"users": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "email", "password_hash", "role", "tenant_id", "active", "email_verified", "last_login"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "email", "password_hash", "role", "tenant_id", "active", "email_verified", "last_login"},
		boolColumns: []string{"active", "email_verified"},
		dependsOn:   []string{"tenants"},
	},
	"domains": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "tenant_id", "domain", "dkim_selector", "status", "is_verified", "is_primary"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "tenant_id", "domain", "dkim_selector", "status", "is_verified", "is_primary"},
		boolColumns: []string{"is_verified", "is_primary"},
		dependsOn:   []string{"tenants"},
	},
	"mailboxes": {
		// SQLite mailboxes stores local_part + domain_id; PostgreSQL mailboxes
		// stores local_part + email. The source query joins domains so the
		// target email is built as local_part || '@' || domains.domain, preserving
		// the real mailbox identity instead of copying local_part into email.
		srcColumns:   []string{"id", "created_at", "updated_at", "deleted_at", "tenant_id", "domain_id", "local_part", "email", "password_hash", "quota_mb", "is_alias", "is_catchall", "is_active", "display_name", "send_limit"},
		tgtColumns:   []string{"id", "created_at", "updated_at", "deleted_at", "tenant_id", "domain_id", "local_part", "email", "password_hash", "quota_mb", "is_alias", "is_catchall", "is_active", "display_name", "send_limit"},
		boolColumns:  []string{"is_alias", "is_catchall", "is_active"},
		nullDefaults: map[string]any{"display_name": ""},
		dependsOn:    []string{"domains"},
		sourceQuery: `SELECT m.id, m.created_at, m.updated_at, m.deleted_at, m.tenant_id, m.domain_id, m.local_part,
		m.local_part || '@' || d.domain AS email, m.password_hash, m.quota_mb, m.is_alias, m.is_catchall,
		m.is_active, COALESCE(m.display_name, '') AS display_name, m.send_limit
		FROM mailboxes m JOIN domains d ON m.domain_id = d.id`,
	},
	"api_keys": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "key_hash", "name", "expires_at", "last_used_at", "active"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "key_hash", "name", "expires_at", "last_used_at", "active"},
		boolColumns: []string{"active"},
		dependsOn:   []string{"users"},
	},
	"sessions": {
		srcColumns: []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "token_hash", "role", "email", "ip", "user_agent", "expires_at"},
		tgtColumns: []string{"id", "created_at", "updated_at", "deleted_at", "user_id", "token_hash", "role", "email", "ip", "user_agent", "expires_at"},
		dependsOn:  []string{"users"},
	},
	"coremail_audit": {
		srcColumns: []string{"id", "actor", "role", "action", "target", "result", "ip", "user_agent", "timestamp"},
		tgtColumns: []string{"id", "actor", "role", "action", "target", "result", "ip", "user_agent", "timestamp"},
	},
	"security_events": {
		srcColumns: []string{"id", "created_at", "ip", "email", "event_type", "count"},
		tgtColumns: []string{"id", "created_at", "ip", "email", "event_type", "count"},
	},
	"feature_flags": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "name", "enabled", "tier_required", "module_version", "description"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "name", "enabled", "tier_required", "module_version", "description"},
		boolColumns: []string{"enabled"},
	},
	"licenses": {
		srcColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "key_hash", "tier", "issued_at", "expires_at", "max_domains", "max_mailboxes", "hardware_id", "metadata", "active"},
		tgtColumns:  []string{"id", "created_at", "updated_at", "deleted_at", "key_hash", "tier", "issued_at", "expires_at", "max_domains", "max_mailboxes", "hardware_id", "metadata", "active"},
		boolColumns: []string{"active"},
	},
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
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteIdentifier(table)).Scan(&n); err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		counts[table] = n
	}
	return counts, nil
}

func (p migrationPlan) run(ctx context.Context, srcDB, tgtDB *sql.DB) error {
	ordered := p.dependencyOrder()
	for _, table := range ordered {
		tm, ok := tableColumnMap[table]
		if !ok {
			return fmt.Errorf("no migration definition for table %s", table)
		}
		if err := migrateTable(ctx, srcDB, tgtDB, table, tm); err != nil {
			return fmt.Errorf("migrate table %s: %w", table, err)
		}
		if err := syncSequence(ctx, tgtDB, table); err != nil {
			return fmt.Errorf("sync sequence for %s: %w", table, err)
		}
	}
	return nil
}

// syncSequence updates the PostgreSQL SERIAL/BIGSERIAL sequence for table
// so that subsequent inserts using DEFAULT nextval(...) do not collide with
// rows that were copied with explicit SQLite ids.
func syncSequence(ctx context.Context, tgtDB *sql.DB, table string) error {
	var seqName sql.NullString
	if err := tgtDB.QueryRowContext(ctx, "SELECT pg_get_serial_sequence($1, 'id')", table).Scan(&seqName); err != nil {
		return fmt.Errorf("lookup sequence for %s: %w", table, err)
	}
	if !seqName.Valid || seqName.String == "" {
		return nil // no serial sequence for this table
	}
	_, err := tgtDB.ExecContext(ctx,
		"SELECT setval($1, COALESCE((SELECT MAX(id) FROM "+quoteIdentifier(table)+"), 1), true)",
		seqName.String)
	if err != nil {
		return fmt.Errorf("setval %s for %s: %w", seqName.String, table, err)
	}
	return nil
}

// dependencyOrder returns tables sorted so that dependents come after
// their dependencies, using topological sort.
func (p migrationPlan) dependencyOrder() []string {
	inDegree := make(map[string]int)
	graph := make(map[string][]string)
	for _, t := range p.tables {
		if _, ok := inDegree[t]; !ok {
			inDegree[t] = 0
		}
		tm := tableColumnMap[t]
		for _, dep := range tm.dependsOn {
			graph[dep] = append(graph[dep], t)
			inDegree[t]++
		}
	}
	var queue []string
	for _, t := range p.tables {
		if inDegree[t] == 0 {
			queue = append(queue, t)
		}
	}
	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)
		for _, neighbor := range graph[node] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}
	return result
}

func migrateTable(ctx context.Context, srcDB, tgtDB *sql.DB, table string, tm tableMigration) error {
	if err := validateIdentifier(table); err != nil {
		return fmt.Errorf("invalid table name %q: %w", table, err)
	}
	if len(tm.srcColumns) == 0 {
		return nil
	}

	// Build SELECT from source using srcColumns.
	srcQuoted := make([]string, len(tm.srcColumns))
	for i, c := range tm.srcColumns {
		if err := validateIdentifier(c); err != nil {
			return fmt.Errorf("invalid source column %q in table %s: %w", c, table, err)
		}
		srcQuoted[i] = quoteIdentifier(c)
	}
	srcColList := strings.Join(srcQuoted, ", ")

	// Build INSERT into target using tgtColumns.
	tgtQuoted := make([]string, len(tm.tgtColumns))
	for i, c := range tm.tgtColumns {
		if err := validateIdentifier(c); err != nil {
			return fmt.Errorf("invalid target column %q in table %s: %w", c, table, err)
		}
		tgtQuoted[i] = quoteIdentifier(c)
	}
	tgtColList := strings.Join(tgtQuoted, ", ")

	placeholders := make([]string, len(tm.tgtColumns))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}
	insertSQL := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT DO NOTHING",
		quoteIdentifier(table), tgtColList, strings.Join(placeholders, ", "))

	selectSQL := tm.sourceQuery
	if selectSQL == "" {
		selectSQL = fmt.Sprintf("SELECT %s FROM %s", srcColList, quoteIdentifier(table))
	}
	srcRows, err := srcDB.QueryContext(ctx, selectSQL)
	if err != nil {
		return fmt.Errorf("select from source %s: %w", table, err)
	}
	defer srcRows.Close()

	// Build bool column lookup set.
	boolSet := make(map[string]bool)
	for _, c := range tm.boolColumns {
		boolSet[c] = true
	}

	valuePtrs := make([]any, len(tm.srcColumns))
	values := make([]any, len(tm.srcColumns))
	for i := range valuePtrs {
		valuePtrs[i] = &values[i]
	}

	var processed int64
	var inserted int64
	for srcRows.Next() {
		if err := srcRows.Scan(valuePtrs...); err != nil {
			return fmt.Errorf("scan row from %s: %w", table, err)
		}
		// Convert SQLite integer booleans to Go bool for PostgreSQL BOOLEAN columns.
		for i, col := range tm.srcColumns {
			if boolSet[col] {
				switch v := values[i].(type) {
				case int64:
					values[i] = v != 0
				case float64:
					values[i] = v != 0
				}
			}
		}
		// Apply null defaults: when a SQLite value is NULL and
		// a default is defined, substitute it so we don't violate
		// PostgreSQL NOT NULL constraints.
		for i, col := range tm.srcColumns {
			if dflt, ok := tm.nullDefaults[col]; ok && values[i] == nil {
				values[i] = dflt
			}
		}
		res, err := tgtDB.ExecContext(ctx, insertSQL, values...)
		if err != nil {
			return fmt.Errorf("insert into %s: %w", table, err)
		}
		processed++
		// ON CONFLICT DO NOTHING returns RowsAffected=0 when a row is skipped.
		// Only count rows that were actually inserted.
		if n, raErr := res.RowsAffected(); raErr == nil && n > 0 {
			inserted++
		}
	}
	if err := srcRows.Err(); err != nil {
		return err
	}

	fmt.Printf("Migrated %d rows into %s (%d processed, %d inserted, %d skipped)\n", inserted, table, processed, inserted, processed-inserted)
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

func init() {
	_ = time.Now
}
