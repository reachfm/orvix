package main

import (
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
	"github.com/orvix/orvix/internal/calendar"
	"github.com/orvix/orvix/internal/collaboration"
	"github.com/orvix/orvix/internal/compliance"
	"github.com/orvix/orvix/internal/compose"
	"github.com/orvix/orvix/internal/config"
	coremailruntime "github.com/orvix/orvix/internal/coremail/runtime"
	"github.com/orvix/orvix/internal/dns"
	"github.com/orvix/orvix/internal/firewall"
	"github.com/orvix/orvix/internal/guardian"
	"github.com/orvix/orvix/internal/intelligence"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/migration"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/provision"
	"github.com/orvix/orvix/internal/updater"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
	"gorm.io/gorm"
)

func main() {
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

	seedFeatureFlags(db, logger)

	reg := modules.NewRegistry(logger)

	_, _ = license.NewValidator("", db, logger)

	featureFlags := license.NewFeatureFlags(logger)
	featureFlags.SetTier(license.TierSMB)

	authenticator, err := auth.NewAuthenticator(&cfg.Auth, db, logger)
	if err != nil {
		logger.Fatal("failed to create authenticator", zap.Error(err))
	}

	seedAdminUser(db, authenticator, logger)

	registerModules(reg, cfg, db, logger, featureFlags)

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

func registerModules(r *modules.Registry, cfg *config.Config, db *gorm.DB, logger *zap.Logger, ff *license.FeatureFlags) {
	r.Register(coremailruntime.New(logger))
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

	var count int64
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", adminEmail).Scan(&count); err != nil {
		logger.Warn("failed to check existing admin user", zap.Error(err))
		return
	}
	if count > 0 {
		logger.Info("admin user already exists", zap.String("email", adminEmail))
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

	if err := insertBootstrapAdmin(sqlDB, adminEmail, hashedPassword, tenantDomain); err != nil {
		logger.Warn("failed to create admin user", zap.Error(err))
		return
	}

	logger.Info("admin user created", zap.String("email", adminEmail))
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

func insertBootstrapAdmin(db *sql.DB, adminEmail, hashedPassword, tenantDomain string) error {
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

	return tx.Commit()
}
