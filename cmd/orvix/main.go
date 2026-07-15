package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/api"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/autoheal"
	"github.com/orvix/orvix/internal/buildinfo"
	"github.com/orvix/orvix/internal/calendar"
	"github.com/orvix/orvix/internal/collaboration"
	"github.com/orvix/orvix/internal/compliance"
	"github.com/orvix/orvix/internal/compose"
	"github.com/orvix/orvix/internal/config"
	coremailruntime "github.com/orvix/orvix/internal/coremail/runtime"
	coremailstorage "github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/dns"
	"github.com/orvix/orvix/internal/firewall"
	"github.com/orvix/orvix/internal/guardian"
	"github.com/orvix/orvix/internal/intelligence"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/migration"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/provision"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	"github.com/orvix/orvix/internal/updater"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// handleMetadataArgs returns (handled, exitCode) for the small set of
// CLI args that must short-circuit before any runtime bootstrap. Any
// other argument causes the function to return (false, 0) and main()
// proceeds with normal startup.
//
// The set is intentionally narrow:
//
//	-h, --help    Print usage and exit 0.
//	-v, --version Print short version summary and exit 0.
//	version       Print short version summary and exit 0.
//	version --full / version -v  Print long version detail and exit 0.
//
// Adding new short-circuit args here is fine; promoting to flag.Parse
// is fine if the list grows past a handful of flags.
func handleMetadataArgs(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	first := args[0]
	switch first {
	case "-h", "--help", "help":
		printHelp()
		return true, 0
	case "-v", "--version":
		fmt.Println(buildinfo.Short())
		return true, 0
	case "version":
		// `orvix version`         → short
		// `orvix version --full`  → long
		// `orvix version -v`      → long
		if len(args) > 1 && (args[1] == "--full" || args[1] == "-v" || args[1] == "-V") {
			fmt.Print(buildinfo.Long())
		} else {
			fmt.Println(buildinfo.Short())
		}
		return true, 0
	}
	return false, 0
}

func printHelp() {
	fmt.Println(`orvix — Orvix Email Server Platform

Usage:
  orvix [flags]
  orvix <command> [args]

Commands:
  serve              Start the Orvix runtime (default if no command is given).
  migrate            Migrate data between database backends. See ` + "`orvix migrate -h`" + `.
  version [--full]   Print version metadata and exit. Does not touch config,
                     database, migrations, modules, or listeners.
  help, -h, --help   Print this help and exit. Same fast-path as version.

Flags:
  -h, --help         Print this help and exit.
  -v, --version      Print version summary and exit.

Notes:
  • All metadata commands exit before loading config or connecting to
    the database, so they are safe to run in CI, recovery shells, and
    upgrade dry-runs where the service may not be healthy.
  • The runtime is started by running ` + "`orvix`" + ` with no metadata flag.

Build metadata (visible via ` + "`orvix version --full`" + `):
  version, commit, tag, build_time, channel, go_version, os/arch.`)
}

func main() {
	// This guarantees `orvix --help` and `orvix version` exit quickly
	// without touching config, DB, migrations, modules, or listeners —
	// which is the documented behavior for the CLI metadata commands.
	//
	// See ENTERPRISE-BACKEND-COMPLETION item 12: "orvix --help
	// unexpectedly booted config, DB, migrations, modules, runtime".
	//
	// If a future flag set grows beyond a couple of help/version flags,
	// promote this to flag.Parse or a small subcommand multiplexer.
	if handled, exit := handleMetadataArgs(os.Args[1:]); handled {
		os.Exit(exit)
	}

	// Dispatch non-server subcommands before booting config/DB/runtime.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			os.Exit(migrateCommand(os.Args[2:]))
		case "restore-run":
			// External, privileged restore coordinator invoked by
			// orvix-restore.service. Never started by the API process.
			os.Exit(restoreRunCommand(os.Args[2:]))
		case "serve":
			// fall through to normal startup
			_ = 0
		}
	}

	logger, err := config.NewLogger(&config.LoggingConfig{
		Level:  "info",
		Format: "console",
		Output: "stdout",
	})
	if err != nil {
		log.Fatalf("failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("orvix starting",
		zap.Any("watermark", config.GetWatermark()),
		zap.String("canary", config.CanaryToken()),
	)

	cfg, err := config.Load(logger)
	if err != nil {
		logger.Fatal("failed to load configuration", zap.Error(err))
	}

	db, err := config.NewDatabase(&cfg.Database, logger)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}

	if err := migrateConfiguredDatabase(db, cfg.Database.Driver, logger); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}
	logger.Info("database migrations completed")

	seedFeatureFlags(db, logger)

	reg := modules.NewRegistry(logger)

	// Create the shared listener state registry for admin
	// runtime telemetry. The coremail runtime module populates
	// it during Start(), and the router reads it for the
	// /api/v1/admin/runtime endpoint.
	listenerRegistry := orvixruntime.NewListenerRegistry()

	_, _ = license.NewValidator("", db, logger)

	featureFlags := license.NewFeatureFlags(logger)
	featureFlags.SetTier(license.TierSMB)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		logger.Fatal("failed to create authenticator", zap.Error(err))
	}

	seedAdminUser(db, authenticator, logger, dbdialect.FromDriver(cfg.Database.Driver))

	registerModules(reg, cfg, db, logger, featureFlags, listenerRegistry)

	if err := reg.InitAll(cfg, db); err != nil {
		logger.Fatal("failed to initialize modules", zap.Error(err))
	}

	if err := reg.StartAll(); err != nil {
		logger.Fatal("failed to start modules", zap.Error(err))
	}

	var redisClient *redis.Client
	if cfg.Redis.Host != "" {
		redisClient = config.NewRedisClient(&cfg.Redis, logger)
	}

	router := api.NewRouter(cfg, authenticator, logger, db, reg, featureFlags, redisClient)
	router.Start()
	logger.Info("billing scheduler and background services started")

	app := router.App()

	adminPort := cfg.Server.AdminPort
	if adminPort == 0 {
		adminPort = 8080
	}

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, adminPort)

	if cfg.Server.TLSAuto && cfg.Server.TLSHostname != "" {
		certManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Server.TLSHostname),
			Cache:      autocert.DirCache(cfg.Server.TLSCacheDir),
			Email:      cfg.Server.TLSEmail,
		}
		go func() {
			logger.Info("starting HTTPS with auto TLS via autocert",
				zap.Int("port", adminPort),
				zap.String("hostname", cfg.Server.TLSHostname),
			)
			if err := app.Listen(addr, fiber.ListenConfig{
				AutoCertManager: certManager,
			}); err != nil {
				logger.Fatal("auto TLS server error", zap.Error(err))
			}
		}()
	} else if cfg.Server.TLSCertFile != "" && cfg.Server.TLSKeyFile != "" {
		go func() {
			logger.Info("starting HTTPS with configured certificates",
				zap.Int("port", adminPort),
			)
			if err := app.Listen(addr, fiber.ListenConfig{
				CertFile:    cfg.Server.TLSCertFile,
				CertKeyFile: cfg.Server.TLSKeyFile,
			}); err != nil {
				logger.Fatal("HTTPS server error", zap.Error(err))
			}
		}()
	} else {
		go func() {
			logger.Info("admin server starting (HTTP)", zap.Int("port", adminPort))
			if err := app.Listen(addr); err != nil {
				logger.Fatal("admin server error", zap.Error(err))
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down orvix...")

	if err := router.Shutdown(); err != nil {
		logger.Error("admin server shutdown error", zap.Error(err))
	}

	if err := reg.StopAll(); err != nil {
		logger.Error("module shutdown error", zap.Error(err))
	}

	logger.Info("orvix shutdown complete")
}

func registerModules(r *modules.Registry, cfg *config.Config, db *gorm.DB, logger *zap.Logger, ff *license.FeatureFlags, listenerReg *orvixruntime.ListenerRegistry) {
	cmModule := coremailruntime.New(logger)
	cmModule.SetListenerRegistry(listenerReg)
	r.Register(cmModule)
	r.Register(&firewall.Module{})
	r.Register(&autoheal.Module{})
	r.Register(&dns.Module{})
	r.Register(&migration.Module{})
	r.Register(&guardian.Module{})
	r.Register(&compose.Module{})
	r.Register(&updater.Module{})
	r.Register(&provision.Module{})
	r.Register(&calendar.Module{})
	r.Register(&collaboration.Module{})
	r.Register(&compliance.Module{})
	r.Register(&intelligence.Module{})
}

func ensureCoreMailBootstrapSchema(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	for _, stmt := range append(coremailstorage.Tables(), coremailstorage.Indexes()...) {
		if _, err := sqlDB.Exec(stmt); err != nil {
			return fmt.Errorf("coremail bootstrap storage migration: %w", err)
		}
	}
	return nil
}

// migrateConfiguredDatabase runs the correct migration (and any
// dialect-specific schema bootstrap) based on the database driver.
// For SQLite it calls MigrateAllRaw + ensureCoreMailBootstrapSchema;
// for PostgreSQL it calls only MigrateAllPostgres (the CoreMail
// bootstrap schema is SQLite-only DDL).
func migrateConfiguredDatabase(db *gorm.DB, driver string, logger *zap.Logger) error {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "sqlite", "sqlite3":
		if err := models.MigrateAllRaw(db); err != nil {
			return err
		}
		return ensureCoreMailBootstrapSchema(db)
	case "postgres", "postgresql":
		return models.MigrateAllPostgres(db)
	default:
		return fmt.Errorf("unsupported database driver: %s", driver)
	}
}

func seedFeatureFlags(db *gorm.DB, logger *zap.Logger) {
	flags := []struct {
		Name          string
		TierRequired  string
		ModuleVersion string
		Description   string
	}{
		{"webmail", "smb", "1.0.0", "Webmail UI"},
		{"firewall_basic", "smb", "1.0.0", "Mail firewall"},
		{"two_factor_auth", "smb", "1.0.0", "Two-factor authentication"},
		{"rest_api", "isp", "1.0.0", "REST API access"},
		{"audit_logs", "smb", "1.0.0", "Audit log access"},
		{"autoheal", "smb", "1.0.0", "Auto-heal system"},
		{"dns_automation", "smb", "1.0.0", "DNS automation"},
		{"smart_migration", "isp", "1.0.0", "Smart migration tool"},
		{"guardian", "isp", "1.0.0", "Guardian AI threat analysis"},
		{"smart_compose", "smb", "1.0.0", "Smart Compose AI"},
		{"auto_update", "smb", "1.0.0", "Auto-update system"},
		{"provision_api", "isp", "1.0.0", "Instant deployment API"},
		{"calendar", "smb", "1.0.0", "Calendar"},
		{"contacts", "smb", "1.0.0", "Contacts"},
		{"collaboration", "enterprise", "1.0.0", "Collaboration layer"},
		{"compliance", "enterprise", "1.0.0", "Compliance center"},
		{"intelligence", "isp", "1.0.0", "Email intelligence"},
	}

	for _, f := range flags {
		var count int64
		db.Model(&models.FeatureFlag{}).Where("name = ?", f.Name).Count(&count)
		if count == 0 {
			db.Create(&models.FeatureFlag{
				Name:          f.Name,
				Enabled:       true,
				TierRequired:  f.TierRequired,
				ModuleVersion: f.ModuleVersion,
				Description:   f.Description,
			})
		}
	}

	logger.Info("feature flags seeded")
}

func seedAdminUser(db *gorm.DB, authenticator *auth.Authenticator, logger *zap.Logger, dial *dbdialect.Info) {
	adminEmail := os.Getenv("ORVIX_ADMIN_EMAIL")
	adminPassword, passwordErr := bootstrapAdminPassword()

	if adminEmail == "" || adminPassword == "" {
		logger.Info("admin credentials not provided via environment variables")
		logger.Info("set ORVIX_ADMIN_EMAIL and ORVIX_ADMIN_PASSWORD_B64 to create admin user")
		if passwordErr != nil {
			logger.Warn("admin password bootstrap value was invalid", zap.Error(passwordErr))
		}
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		logger.Warn("failed to access database for admin bootstrap", zap.Error(err))
		return
	}
	if !dial.IsPostgres() {
		if err := ensureCoreMailBootstrapSchema(db); err != nil {
			logger.Warn("failed to prepare coremail storage schema for admin bootstrap", zap.Error(err))
			return
		}
	}

	var count int64
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = "+dial.Placeholder(1), adminEmail).Scan(&count); err != nil {
		logger.Warn("failed to check existing admin user", zap.Error(err))
		return
	}
	if count > 0 {
		if !verifyStoredAdminHash(sqlDB, dial, authenticator, adminEmail, adminPassword) {
			logger.Error("admin user row exists but password verification failed; refusing to keep inconsistent state",
				zap.String("email", adminEmail),
				zap.String("hint", "stop the service, delete /etc/orvix/bootstrap.env, then run the installer again"))
			return
		}
		logger.Info("admin user already exists and password verifies", zap.String("email", adminEmail))
		return
	}

	hashedPassword, err := authenticator.HashPassword(adminPassword)
	if err != nil {
		logger.Warn("failed to hash admin password", zap.Error(err))
		return
	}

	parts := strings.Split(adminEmail, "@")
	var tenantDomain string
	if len(parts) == 2 {
		tenantDomain = parts[1]
	} else {
		tenantDomain = "local"
	}

	if err := insertBootstrapAdmin(sqlDB, dial, adminEmail, hashedPassword, tenantDomain, adminPassword, logger); err != nil {
		logger.Warn("failed to create admin user", zap.Error(err))
		return
	}

	if !verifyStoredAdminHash(sqlDB, dial, authenticator, adminEmail, adminPassword) {
		logger.Error("admin user was created but password verification failed against the stored hash",
			zap.String("email", adminEmail),
			zap.String("hint", "this is a runtime bug; please report with the install log"))
		return
	}

	logger.Info("admin user created and password verification succeeded", zap.String("email", adminEmail))
}

// verifyStoredAdminHash returns true if the row in users for
// the given email has a password_hash that verifies the
// supplied plain password. It is the post-condition guard for
// the bootstrap path: a non-nil return proves the runtime
// can authenticate the same credentials the installer's
// verify_install used, in this process, with this database
// connection.
func verifyStoredAdminHash(sqlDB *sql.DB, dial *dbdialect.Info, authenticator *auth.Authenticator, email, password string) bool {
	var storedHash string
	if err := sqlDB.QueryRow("SELECT password_hash FROM users WHERE email = "+dial.Placeholder(1), email).Scan(&storedHash); err != nil {
		return false
	}
	return authenticator.VerifyPassword(password, storedHash)
}

func bootstrapAdminPassword() (string, error) {
	if encoded := os.Getenv("ORVIX_ADMIN_PASSWORD_B64"); encoded != "" {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("decode ORVIX_ADMIN_PASSWORD_B64: %w", err)
		}
		return string(raw), nil
	}
	return os.Getenv("ORVIX_ADMIN_PASSWORD"), nil
}

func insertBootstrapAdmin(db *sql.DB, dial *dbdialect.Info, adminEmail, hashedPassword, tenantDomain, plainPassword string, logger *zap.Logger) error {
	now := time.Now().UTC()
	slug := strings.ReplaceAll(tenantDomain, ".", "-")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var tenantID int64
	err = tx.QueryRow("SELECT id FROM tenants WHERE domain = "+dial.Placeholder(1)+" AND deleted_at IS NULL", tenantDomain).Scan(&tenantID)
	if err == sql.ErrNoRows {
		if dial.IsPostgres() {
			err = tx.QueryRow(
				"INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active) VALUES ("+dial.Placeholder(1)+", "+dial.Placeholder(2)+", "+dial.Placeholder(3)+", "+dial.Placeholder(4)+", "+dial.Placeholder(5)+", "+dial.Placeholder(6)+", "+dial.Placeholder(7)+") RETURNING id",
				now, now, tenantDomain, slug, tenantDomain, "enterprise", true,
			).Scan(&tenantID)
		} else {
			res, execErr := tx.Exec(
				`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active)
				 VALUES (`+dial.Placeholder(1)+`, `+dial.Placeholder(2)+`, `+dial.Placeholder(3)+`, `+dial.Placeholder(4)+`, `+dial.Placeholder(5)+`, `+dial.Placeholder(6)+`, `+dial.Placeholder(7)+`)`,
				now, now, tenantDomain, slug, tenantDomain, "enterprise", 1,
			)
			if execErr != nil {
				return fmt.Errorf("insert tenant: %w", execErr)
			}
			tenantID, err = res.LastInsertId()
		}
		if err != nil {
			return fmt.Errorf("tenant id: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("select tenant: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (`+dial.Placeholder(1)+`, `+dial.Placeholder(2)+`, `+dial.Placeholder(3)+`, `+dial.Placeholder(4)+`, `+dial.Placeholder(5)+`, `+dial.Placeholder(6)+`, `+dial.Placeholder(7)+`, `+dial.Placeholder(8)+`)`,
		now, now, adminEmail, hashedPassword, "admin", tenantID, true, true,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}

	// Create CoreMail domain.
	var domainID int64
	err = tx.QueryRow("SELECT id FROM coremail_domains WHERE name = "+dial.Placeholder(1), tenantDomain).Scan(&domainID)
	if err == sql.ErrNoRows {
		if dial.IsPostgres() {
			err = tx.QueryRow(
				"INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at) VALUES ("+dial.Placeholder(1)+", "+dial.Placeholder(2)+", 'active', 'enterprise', 0, 0, 0, "+dial.Placeholder(3)+", "+dial.Placeholder(4)+") RETURNING id",
				tenantDomain, tenantID, now, now,
			).Scan(&domainID)
		} else {
			res, execErr := tx.Exec(
				`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at)
				 VALUES (`+dial.Placeholder(1)+`, `+dial.Placeholder(2)+`, 'active', 'enterprise', 0, 0, 0, `+dial.Placeholder(3)+`, `+dial.Placeholder(4)+`)`,
				tenantDomain, tenantID, now, now,
			)
			if execErr != nil {
				return fmt.Errorf("insert domain: %w", execErr)
			}
			domainID, err = res.LastInsertId()
		}
		if err != nil {
			return fmt.Errorf("domain id: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("select domain: %w", err)
	}

	// Create CoreMail mailbox with Argon2id hash.
	localPart := adminEmail
	if at := strings.Index(adminEmail, "@"); at > 0 {
		localPart = adminEmail[:at]
	}

	argon2Hash, err := hashPasswordArgon2id(plainPassword)
	if err != nil {
		logger.Warn("failed to hash admin password with argon2id, skipping mailbox creation", zap.Error(err))
	} else {
		var mailboxID int64
		if dial.IsPostgres() {
			err = tx.QueryRow(
				"INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at) VALUES ("+dial.Placeholder(1)+", "+dial.Placeholder(2)+", "+dial.Placeholder(3)+", "+dial.Placeholder(4)+", 'Admin', "+dial.Placeholder(5)+", 'argon2id', 'active', 1024, "+dial.Placeholder(6)+", "+dial.Placeholder(7)+", "+dial.Placeholder(8)+") RETURNING id",
				domainID, tenantID, localPart, adminEmail, argon2Hash, true, now, now,
			).Scan(&mailboxID)
			if err != nil {
				return fmt.Errorf("mailbox id pg: %w", err)
			}
		} else {
			mailboxRes, execErr := tx.Exec(
				`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
				 VALUES (`+dial.Placeholder(1)+`, `+dial.Placeholder(2)+`, `+dial.Placeholder(3)+`, `+dial.Placeholder(4)+`, 'Admin', `+dial.Placeholder(5)+`, 'argon2id', 'active', 1024, 1, `+dial.Placeholder(6)+`, `+dial.Placeholder(7)+`)`,
				domainID, tenantID, localPart, adminEmail, argon2Hash, now, now,
			)
			if execErr != nil {
				return fmt.Errorf("insert mailbox: %w", execErr)
			}
			mailboxID, err = mailboxRes.LastInsertId()
			if err != nil {
				return fmt.Errorf("mailbox id sqlite: %w", err)
			}
		}

		if err := provisionSystemFoldersTx(context.Background(), tx, dial, uint(mailboxID), now); err != nil {
			logger.Warn("failed to provision system folders for admin mailbox",
				zap.String("email", adminEmail),
				zap.Int64("mailbox_id", mailboxID),
				zap.Error(err))
		}
	}

	return tx.Commit()
}

func hashPasswordArgon2id(password string) (string, error) {
	const (
		argon2Time    uint32 = 3
		argon2Mem     uint32 = 65536
		argon2Threads uint8  = 4
		argon2KeyLen  uint32 = 32
	)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Mem, argon2Threads, argon2KeyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2Mem, argon2Time, argon2Threads, b64Salt, b64Hash), nil
}

// provisionSystemFoldersTx inserts the canonical system
// folders (INBOX, Sent, Drafts, Trash, Junk, Archive)
// for the given mailbox, using the supplied *sql.Tx.
//
// The function is a thin wrapper around
// coremail.EnsureMailboxSystemFolders that knows how to
// run inside the bootstrap transaction. The installer's
// admin bootstrap is the only place we have a live
// *sql.Tx at the right moment; everywhere else (the
// admin CreateMailbox handler, the webmail login
// handler) uses the standalone coremail helper against
// the live *sql.DB.
//
// Like the standalone helper, this is idempotent: if a
// folder at the canonical path already exists, it is
// left as-is.
func provisionSystemFoldersTx(ctx context.Context, tx *sql.Tx, dial *dbdialect.Info, mailboxID uint, now time.Time) error {
	folders := []struct {
		path string
		typ  string
	}{
		{"INBOX", "inbox"},
		{"Sent", "sent"},
		{"Drafts", "drafts"},
		{"Trash", "trash"},
		{"Junk", "junk"},
		{"Archive", "archive"},
	}
	for _, f := range folders {
		var existingID uint
		err := tx.QueryRowContext(ctx,
			"SELECT id FROM coremail_folders WHERE mailbox_id = "+dial.Placeholder(1)+" AND path = "+dial.Placeholder(2),
			mailboxID, f.path,
		).Scan(&existingID)
		switch err {
		case nil:
			continue
		case sql.ErrNoRows:
			// fall through to INSERT
		default:
			return fmt.Errorf("check system folder %s: %w", f.path, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO coremail_folders
				(mailbox_id, parent_id, name, path, folder_type,
				 message_count, unread_count, total_size,
				 created_at, updated_at)
			VALUES (`+dial.Placeholder(1)+`, NULL, `+dial.Placeholder(2)+`, `+dial.Placeholder(3)+`, `+dial.Placeholder(4)+`, 0, 0, 0, `+dial.Placeholder(5)+`, `+dial.Placeholder(6)+`)`,
			mailboxID, f.path, f.path, f.typ, now, now,
		); err != nil {
			return fmt.Errorf("create system folder %s: %w", f.path, err)
		}
	}
	return nil
}
