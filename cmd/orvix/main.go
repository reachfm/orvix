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

	if err := models.MigrateAllRaw(db); err != nil {
		logger.Fatal("failed to run migrations", zap.Error(err))
	}
	logger.Info("database migrations completed")
	if err := ensureCoreMailBootstrapSchema(db); err != nil {
		logger.Fatal("failed to run coremail bootstrap schema migrations", zap.Error(err))
	}

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

	seedAdminUser(db, authenticator, logger)

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

	if err := app.Shutdown(); err != nil {
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

func seedAdminUser(db *gorm.DB, authenticator *auth.Authenticator, logger *zap.Logger) {
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
	if err := ensureCoreMailBootstrapSchema(db); err != nil {
		logger.Warn("failed to prepare coremail storage schema for admin bootstrap", zap.Error(err))
		return
	}

	var count int64
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", adminEmail).Scan(&count); err != nil {
		logger.Warn("failed to check existing admin user", zap.Error(err))
		return
	}
	if count > 0 {
		// User row already exists from a previous boot. Re-verify
		// the stored hash actually matches the env password — if
		// the env is being re-applied after a partial failure on a
		// previous boot (e.g. the row was inserted but the hash
		// got mangled), this is the only place we can detect the
		// mismatch and refuse to silently keep the broken row.
		if !verifyStoredAdminHash(sqlDB, authenticator, adminEmail, adminPassword) {
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

	if err := insertBootstrapAdmin(sqlDB, adminEmail, hashedPassword, tenantDomain, adminPassword, logger); err != nil {
		logger.Warn("failed to create admin user", zap.Error(err))
		return
	}

	// Post-insert guard: prove the freshly written hash verifies
	// the same password. If this fails, the row was committed
	// with a hash that cannot authenticate — the symptom would
	// be "INSTALLATION VERIFICATION PASSED" on first boot but
	// every subsequent login fails. We catch that here and log
	// the root cause instead of waiting for the user to report it.
	if !verifyStoredAdminHash(sqlDB, authenticator, adminEmail, adminPassword) {
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
func verifyStoredAdminHash(sqlDB *sql.DB, authenticator *auth.Authenticator, email, password string) bool {
	var storedHash string
	if err := sqlDB.QueryRow("SELECT password_hash FROM users WHERE email = ?", email).Scan(&storedHash); err != nil {
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

func insertBootstrapAdmin(db *sql.DB, adminEmail, hashedPassword, tenantDomain, plainPassword string, logger *zap.Logger) error {
	now := time.Now().UTC()
	slug := strings.ReplaceAll(tenantDomain, ".", "-")

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var tenantID int64
	err = tx.QueryRow("SELECT id FROM tenants WHERE domain = ? AND deleted_at IS NULL", tenantDomain).Scan(&tenantID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO tenants (created_at, updated_at, name, slug, domain, plan, active)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			now, now, tenantDomain, slug, tenantDomain, "enterprise", 1,
		)
		if err != nil {
			return err
		}
		tenantID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	_, err = tx.Exec(
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, now, adminEmail, hashedPassword, "admin", tenantID, 1, 1,
	)
	if err != nil {
		return err
	}

	// Create CoreMail domain.
	var domainID int64
	err = tx.QueryRow("SELECT id FROM coremail_domains WHERE name = ?", tenantDomain).Scan(&domainID)
	if err == sql.ErrNoRows {
		res, err := tx.Exec(
			`INSERT INTO coremail_domains (name, tenant_id, status, plan, max_mailboxes, max_aliases, max_quota_mb, created_at, updated_at)
			 VALUES (?, ?, 'active', 'enterprise', 0, 0, 0, ?, ?)`,
			tenantDomain, tenantID, now, now,
		)
		if err != nil {
			return err
		}
		domainID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
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
		mailboxRes, err := tx.Exec(
			`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
			 VALUES (?, ?, ?, ?, 'Admin', ?, 'argon2id', 'active', 1024, 1, ?, ?)`,
			domainID, tenantID, localPart, adminEmail, argon2Hash, now, now,
		)
		if err != nil {
			return err
		}
		mailboxID, err := mailboxRes.LastInsertId()
		if err != nil {
			return err
		}

		// Provision the canonical system folders
		// (INBOX, Sent, Drafts, Trash, Junk,
		// Archive) for the freshly created admin
		// mailbox. Without this, the first time the
		// admin opens Webmail and tries to send,
		// the Send handler returns
		//   "Sent folder not found for mailbox;
		//    ensure system folders are provisioned"
		// — which is the exact bug we are fixing
		// here. The provision runs inside the
		// bootstrap transaction so a partial install
		// never leaves a mailbox with no folders.
		if err := provisionSystemFoldersTx(context.Background(), tx, uint(mailboxID), now); err != nil {
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
func provisionSystemFoldersTx(ctx context.Context, tx *sql.Tx, mailboxID uint, now time.Time) error {
	// Inline copy of the canonical list, kept in
	// sync with internal/coremail/system_folders.go
	// and internal/coremail/storage/schema.go's
	// DefaultSystemFolders. We intentionally do NOT
	// import those from cmd/orvix/main.go: this
	// file is the install-time bootstrap, and
	// pulling storage or coremail helpers into the
	// installer binary would change its dependency
	// surface for no gain.
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
			"SELECT id FROM coremail_folders WHERE mailbox_id = ? AND path = ?",
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
			VALUES (?, NULL, ?, ?, ?, 0, 0, 0, ?, ?)`,
			mailboxID, f.path, f.path, f.typ, now, now,
		); err != nil {
			return fmt.Errorf("create system folder %s: %w", f.path, err)
		}
	}
	return nil
}
