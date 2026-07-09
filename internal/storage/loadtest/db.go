package loadtest

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	_ "modernc.org/sqlite"
)

const benchmarkTable = "loadtest_coremail_messages"

func envRowCount() int {
	if v := strings.TrimSpace(os.Getenv("ORVIX_LOADTEST_ROWS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 10000
}

func envMailboxCount() int {
	if v := strings.TrimSpace(os.Getenv("ORVIX_LOADTEST_MAILBOXES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 10
}

func envBatchSize() int {
	if v := strings.TrimSpace(os.Getenv("ORVIX_LOADTEST_BATCH_SIZE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 500
}

type benchEnv struct {
	DB        *sql.DB
	Driver    string
	Rows      int
	Mailboxes int
	BatchSize int
}

// openBenchDB opens a database connection for benchmark tests.
// Returns nil when ORVIX_RUN_DB_LOADTEST is not "1", or when
// driver is "postgres" but DSN is empty (deliberate skip).
// The DSN is never logged.
func openBenchDB() (*benchEnv, error) {
	if strings.TrimSpace(os.Getenv("ORVIX_RUN_DB_LOADTEST")) != "1" {
		return nil, nil
	}
	driver := strings.ToLower(strings.TrimSpace(os.Getenv("ORVIX_DB_DRIVER")))
	if driver == "" {
		driver = "sqlite"
	}
	dsn := strings.TrimSpace(os.Getenv("ORVIX_DB_DSN"))

	if driver == "postgres" && dsn == "" {
		return nil, nil
	}

	var (
		sqldb *sql.DB
		err   error
	)

	switch driver {
	case "sqlite":
		if dsn == "" {
			dsn = fmt.Sprintf("file:bench_%d.db?mode=memory&cache=shared&_loc=auto&_busy_timeout=5000",
				time.Now().UnixNano())
		}
		sqldb, err = sql.Open("sqlite", dsn)
		if err != nil {
			return nil, fmt.Errorf("sqlite open: %w", err)
		}
		sqldb.SetMaxOpenConns(1)
		sqldb.SetMaxIdleConns(1)
		if err := sqldb.Ping(); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("sqlite ping: %w", err)
		}

	case "postgres":
		gdb, gerr := gorm.Open(postgres.Open(dsn), &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormlogger.Silent),
		})
		if gerr != nil {
			return nil, fmt.Errorf("postgres open: %w", gerr)
		}
		sqldb, err = gdb.DB()
		if err != nil {
			return nil, fmt.Errorf("postgres sql.DB: %w", err)
		}
		sqldb.SetMaxOpenConns(25)
		sqldb.SetMaxIdleConns(5)
		sqldb.SetConnMaxLifetime(300 * time.Second)
		if err := sqldb.Ping(); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("postgres ping: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported driver %q (want sqlite or postgres)", driver)
	}

	return &benchEnv{
		DB:        sqldb,
		Driver:    driver,
		Rows:      envRowCount(),
		Mailboxes: envMailboxCount(),
		BatchSize: envBatchSize(),
	}, nil
}

func (b *benchEnv) closeBenchDB() {
	if b == nil || b.DB == nil {
		return
	}
	b.DB.Close()
}

func (b *benchEnv) createBenchTable() error {
	var ddl string
	switch b.Driver {
	case "sqlite":
		ddl = sqliteBenchDDL
	case "postgres":
		ddl = postgresBenchDDL
	default:
		return fmt.Errorf("unknown driver: %s", b.Driver)
	}

	if _, err := b.DB.Exec(ddl); err != nil {
		return fmt.Errorf("create benchmark table: %w", err)
	}
	for _, idx := range benchIndexes(b.Driver) {
		if _, err := b.DB.Exec(idx); err != nil {
			return fmt.Errorf("create benchmark index: %w", err)
		}
	}
	return nil
}

func (b *benchEnv) dropBenchTable() {
	if b == nil || b.DB == nil {
		return
	}
	b.DB.Exec(`DROP TABLE IF EXISTS ` + benchmarkTable)
}

// --- DDL variants ---

const sqliteBenchDDL = `
CREATE TABLE IF NOT EXISTS loadtest_coremail_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id TEXT UNIQUE NOT NULL,
	mailbox_id INTEGER NOT NULL,
	folder_id INTEGER NOT NULL DEFAULT 0,
	subject TEXT NOT NULL DEFAULT '',
	from_address TEXT NOT NULL DEFAULT '',
	to_addresses TEXT NOT NULL DEFAULT '',
	seen INTEGER NOT NULL DEFAULT 0,
	deleted INTEGER NOT NULL DEFAULT 0,
	flagged INTEGER NOT NULL DEFAULT 0,
	received_date INTEGER NOT NULL DEFAULT 0,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL DEFAULT 0
)`

const postgresBenchDDL = `
CREATE TABLE IF NOT EXISTS loadtest_coremail_messages (
	id BIGSERIAL PRIMARY KEY,
	message_id TEXT UNIQUE NOT NULL,
	mailbox_id INTEGER NOT NULL,
	folder_id INTEGER NOT NULL DEFAULT 0,
	subject TEXT NOT NULL DEFAULT '',
	from_address TEXT NOT NULL DEFAULT '',
	to_addresses TEXT NOT NULL DEFAULT '',
	seen INTEGER NOT NULL DEFAULT 0,
	deleted INTEGER NOT NULL DEFAULT 0,
	flagged INTEGER NOT NULL DEFAULT 0,
	received_date BIGINT NOT NULL DEFAULT 0,
	size_bytes INTEGER NOT NULL DEFAULT 0,
	created_at BIGINT NOT NULL DEFAULT 0
)`

func benchIndexes(driver string) []string {
	_ = driver
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_lt_messages_mailbox_date ON loadtest_coremail_messages (mailbox_id, received_date DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_lt_messages_mailbox_folder_id ON loadtest_coremail_messages (mailbox_id, folder_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_lt_messages_folder_id ON loadtest_coremail_messages (folder_id, id)`,
	}
}
