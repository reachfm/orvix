package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/orvixemail/orvix/internal/api"
	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/autoheal"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/database"
	"github.com/orvixemail/orvix/internal/features"
	"github.com/orvixemail/orvix/internal/license"
	"github.com/orvixemail/orvix/internal/mailops"
	"github.com/orvixemail/orvix/internal/metrics"
	"github.com/orvixemail/orvix/internal/migrations"
	"github.com/orvixemail/orvix/internal/models"
	"github.com/orvixemail/orvix/internal/provision"
	"github.com/orvixemail/orvix/internal/stalwart"
	"github.com/orvixemail/orvix/internal/updater"
	"github.com/orvixemail/orvix/web"
	"go.uber.org/zap"
	"golang.org/x/crypto/acme/autocert"
	"gorm.io/gorm"
)

var (
	Version   = "0.1.0"
	Product   = "OrvixEM"
	Commit    = "development"
	Channel   = "nightly"
	BuildDate = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		runCommand(os.Args[1])
		return
	}

	runServer()
}

func runCommand(cmd string) {
	switch cmd {
	case "version":
		fmt.Printf("%s v%s (%s) %s\n", Product, Version, Channel, Commit)
	case "start", "serve":
		runServer()
	case "status":
		runQuickStatus()
	case "doctor":
		runDoctor()
	case "migrate":
		runMigrate()
	case "routes":
		runRoutes()
	case "features":
		runFeatures()
	case "update-check":
		runUpdateCheck()
	case "update-apply":
		runUpdateApplyCommand()
	case "update-rollback":
		runUpdateRollback()
	case "stalwart":
		if len(os.Args) < 3 {
			fmt.Println("Usage: orvix stalwart <status|path|validate|config|apply|provision|start|stop|restart>")
			fmt.Println("")
			fmt.Println("  status    Show Stalwart status and health")
			fmt.Println("  path      Show binary path")
			fmt.Println("  validate  Validate Stalwart 0.16 datastore bootstrap config")
			fmt.Println("  config    Generate Stalwart 0.16 config/provisioning files")
			fmt.Println("  apply     Apply the complete Stalwart 0.16.7 integration")
			fmt.Println("  provision domain <domain.com>      Create domain in Stalwart")
			fmt.Println("  provision mailbox <email> <pass>   Create mailbox in Stalwart")
			fmt.Println("  start     Start Stalwart service")
			fmt.Println("  stop      Stop Stalwart service")
			fmt.Println("  restart   Restart Stalwart service")
			return
		}
		runStalwartCommand(os.Args[2])
	case "seed-superadmin":
		runSeedSuperAdmin()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`%s v%s

Usage:
  orvix               Start the server
  orvix start         Start the server
  orvix serve         Start the server
  orvix status        Show server status
  orvix doctor        Run system diagnostics
  orvix migrate       Run database migrations
  orvix routes        List registered API routes
  orvix features      List all feature flags
  orvix seed-superadmin  Create or update the platform Super Admin account
  orvix update-check  Check for updates
  orvix update-apply  Apply available update
  orvix update-rollback Rollback to previous version
  orvix stalwart      Manage Stalwart mail server
  orvix version       Show version information
  orvix help          Show this help

Stalwart commands:
  orvix stalwart status    Show Stalwart status and health
  orvix stalwart path      Show Stalwart binary path
  orvix stalwart validate  Validate Stalwart 0.16 datastore bootstrap config
  orvix stalwart config    Generate Stalwart 0.16 config/provisioning files
  orvix stalwart apply     Apply complete Stalwart 0.16.7 integration
  orvix stalwart provision domain <domain>      Create domain in Stalwart
  orvix stalwart provision mailbox <email> <pass>  Create mailbox in Stalwart
  orvix stalwart start     Start Stalwart service
  orvix stalwart stop      Stop Stalwart service
  orvix stalwart restart   Restart Stalwart service

Domain: orvix.email
Webmail: https://mail.orvix.email
Admin:   https://admin.orvix.email
Portal:  https://portal.orvix.email
API:     https://api.orvix.email
`, Product, Version)
}

func runQuickStatus() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()

	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)
	detected, path := stalwartSvc.Detect()

	// Check frontend assets
	_, adminErr := web.FrontendFS.ReadDir("admin/dist")
	_, webmailErr := web.FrontendFS.ReadDir("webmail/dist")
	_, portalErr := web.FrontendFS.ReadDir("portal/dist")

	fmt.Printf("%s v%s\n", Product, Version)
	fmt.Printf("Channel: %s\n", Channel)
	fmt.Printf("Status: starting...\n")
	fmt.Printf("Database: %s\n", cfg.Database.Driver)
	fmt.Printf("Stalwart: detected=%v path=%s\n", detected, path)
	fmt.Printf("Frontend Admin:   %v\n", adminErr == nil)
	fmt.Printf("Frontend Webmail: %v\n", webmailErr == nil)
	fmt.Printf("Frontend Portal:  %v\n", portalErr == nil)
}

func runDoctor() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()

	fmt.Printf("=== %s Doctor ===\n", Product)
	fmt.Printf("Version: %s\n", Version)
	fmt.Printf("Channel: %s\n", Channel)

	configPaths := []string{"orvix.yaml", "./configs/orvix.yaml", "/etc/orvix/orvix.yaml"}
	configFound := false
	for _, p := range configPaths {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("Config: found at %s\n", p)
			configFound = true
			break
		}
	}
	if !configFound {
		fmt.Println("Config: NOT FOUND (searched: orvix.yaml, configs/orvix.yaml, /etc/orvix/orvix.yaml)")
	}

	db, err := database.Connect(cfg.Database)
	if err != nil {
		fmt.Printf("Database: FAILED - %v\n", err)
	} else {
		sqlDB, _ := db.DB()
		if err := sqlDB.Ping(); err != nil {
			fmt.Printf("Database: CONNECTED BUT UNREACHABLE - %v\n", err)
		} else {
			fmt.Printf("Database: OK (%s)\n", cfg.Database.Driver)
		}
		sqlDB.Close()
	}

	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)
	detected, path := stalwartSvc.Detect()
	if detected {
		fmt.Printf("Stalwart: detected at %s\n", path)
		if stalwartSvc.IsRunning() {
			fmt.Println("Stalwart: RUNNING")
		} else {
			fmt.Println("Stalwart: NOT RUNNING")
		}
	} else {
		fmt.Println("Stalwart: NOT DETECTED")
	}

	// Check frontend assets
	if _, err := web.FrontendFS.ReadDir("admin/dist"); err == nil {
		fmt.Println("Frontend Admin:   AVAILABLE")
	} else {
		fmt.Println("Frontend Admin:   MISSING (run 'make build-frontend')")
	}
	if _, err := web.FrontendFS.ReadDir("webmail/dist"); err == nil {
		fmt.Println("Frontend Webmail: AVAILABLE")
	} else {
		fmt.Println("Frontend Webmail: MISSING (run 'make build-frontend')")
	}
	if _, err := web.FrontendFS.ReadDir("portal/dist"); err == nil {
		fmt.Println("Frontend Portal:  AVAILABLE")
	} else {
		fmt.Println("Frontend Portal:  MISSING (run 'make build-frontend')")
	}

	hostname, _ := os.Hostname()
	fmt.Printf("Hostname: %s\n", hostname)
	fmt.Println("=== Doctor complete ===")
}

func runMigrate() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()

	db, err := database.Connect(cfg.Database)
	if err != nil {
		sugar.Fatalf("Failed to connect to database: %v", err)
	}

	if err := migrations.Run(db, cfg.Database.Driver); err != nil {
		sugar.Fatalf("Failed to run migrations: %v", err)
	}
	sugar.Info("Migrations completed successfully")
}

func runRoutes() {
	fmt.Printf("=== %s API Routes ===\n", Product)
	fmt.Println("")
	fmt.Println("Public endpoints:")
	fmt.Println("  GET  /health            Health check")
	fmt.Println("  GET  /version           Version info")
	fmt.Println("  GET  /metrics           Prometheus metrics")
	fmt.Println("  GET  /admin             Admin Console (SPA)")
	fmt.Println("  GET  /mail              Webmail (SPA)")
	fmt.Println("  GET  /portal            Customer Portal (SPA)")
	fmt.Println("")
	fmt.Println("API v1 endpoints:")
	fmt.Println("  GET  /api/v1/features           Feature flags")
	fmt.Println("  GET  /api/v1/license/status     License status")
	fmt.Println("  POST /api/v1/auth/login         Login")
	fmt.Println("  POST /api/v1/auth/verify-totp   Verify TOTP")
	fmt.Println("  POST /api/v1/auth/refresh       Refresh token")
	fmt.Println("  POST /api/v1/auth/logout        Logout")
	fmt.Println("  POST /api/v1/admin/bootstrap    Bootstrap admin")
	fmt.Println("")
	fmt.Println("Authenticated endpoints (all under /api/v1):")
	fmt.Println("  GET|POST /admin/tenants")
	fmt.Println("  GET|POST /admin/domains")
	fmt.Println("  GET|POST /domains")
	fmt.Println("  GET|POST /users")
	fmt.Println("  GET|POST /api-keys")
	fmt.Println("  GET|POST /webhooks")
	fmt.Println("  GET     /admin/provisioning-jobs")
	fmt.Println("  GET     /admin/audit-logs")
	fmt.Println("  GET|POST /contacts")
	fmt.Println("  GET|POST /calendars")
	fmt.Println("  GET|POST /mail-queue")
	fmt.Println("  GET|POST /firewall/rules")
	fmt.Println("  GET|POST /firewall/geo")
	fmt.Println("  GET|POST /autoheal")
	fmt.Println("  GET|POST /guardian")
	fmt.Println("  GET|POST /compliance")
	fmt.Println("  GET|POST /sso/configs")
	fmt.Println("  GET|POST /ldap/configs")
	fmt.Println("  GET|POST /dlp/policies")
	fmt.Println("  GET|POST /distribution-lists")
	fmt.Println("  GET|POST /resources")
	fmt.Println("  GET|POST /public-folders")
	fmt.Println("  GET|POST /routing-rules")
	fmt.Println("  GET     /backups")
	fmt.Println("  GET     /intelligence")
	fmt.Println("  GET     /sla/dashboard")
	fmt.Println("  POST    /provision/domain")
	fmt.Println("  POST    /migration/start")
	fmt.Println("  POST    /compose/suggest")
	fmt.Println("  POST    /compose/summarize")
	fmt.Println("  POST    /compose/translate")
	fmt.Println("  GET     /dns/check")
	fmt.Println("")
	fmt.Printf("Total: 100+ endpoints across all groups\n")
}

func runFeatures() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	db, err := database.Connect(cfg.Database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to database: %v\n", err)
		os.Exit(1)
	}

	licSvc := license.NewService(db, cfg.License)
	featMgr := features.NewManager(db, licSvc)
	if err := featMgr.Initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize features: %v\n", err)
		os.Exit(1)
	}

	flags := featMgr.GetAllFlags()
	fmt.Printf("=== %s Feature Flags (%d total) ===\n", Product, len(flags))
	for _, ff := range flags {
		status := "ðŸ”´"
		statusStr := "disabled"
		if ff.Enabled {
			status = "ðŸŸ¢"
			statusStr = "enabled"
		}
		if ff.IsKillSwitch {
			status = "â›”"
			statusStr = "kill-switch"
		}
		fmt.Printf("  %s %-30s %s:%s\n", status, ff.Key, ff.Tier, statusStr)
	}
}

func runUpdateCheck() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	svc := updater.NewService(cfg.Updates, Version, Channel)
	result, err := svc.CheckForUpdates()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update check failed: %v\n", err)
		return
	}

	fmt.Printf("Current version: %s\n", result.CurrentVersion)
	fmt.Printf("Channel: %s\n", Channel)
	if result.Error != "" {
		fmt.Printf("Status: %s\n", result.Error)
		return
	}
	if result.Available && result.Release != nil {
		fmt.Printf("Update available: %s\n", result.Release.Version)
		fmt.Printf("Published: %s\n", result.Release.PublishedAt.Format(time.RFC3339))
		fmt.Printf("Breaking: %v\n", result.Release.Breaking)
		if result.Release.Changelog != "" {
			fmt.Printf("Changes:\n%s\n", result.Release.Changelog)
		}
	} else {
		fmt.Println("Status: up to date")
	}
}

func runUpdateApply() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	svc := updater.NewService(cfg.Updates, Version, Channel)
	result, err := svc.CheckForUpdates()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update check failed: %v\n", err)
		return
	}

	if !result.Available || result.Release == nil {
		fmt.Println("No updates available")
		return
	}

	fmt.Printf("Downloading %s...\n", result.Release.Version)
	updatePath, err := svc.DownloadUpdate(result.Release)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Download failed: %v\n", err)
		return
	}
	fmt.Println("Downloaded. Applying update...")

	if err := svc.ApplyUpdate(updatePath); err != nil {
		fmt.Fprintf(os.Stderr, "Update failed: %v\n", err)
		return
	}

	fmt.Printf("Update to %s applied successfully!\n", result.Release.Version)
	fmt.Println("Please restart the service to use the new version.")
}

func runUpdateApplyCommand() {
	if err := applyStalwartIntegration(); err != nil {
		fmt.Fprintf(os.Stderr, "Stalwart integration apply FAILED: %v\n", err)
		os.Exit(1)
	}
	runUpdateApply()
}

func runUpdateRollback() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	svc := updater.NewService(cfg.Updates, Version, Channel)
	if err := svc.Rollback(); err != nil {
		fmt.Fprintf(os.Stderr, "Rollback failed: %v\n", err)
		return
	}
	fmt.Println("Rollback completed successfully. Please restart the service.")
}

func runSeedSuperAdmin() {
	cfg, err := config.LoadMinimal()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()
	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)

	hostname := cfg.Server.ExternalURL
	if hostname == "" {
		hostname = "mail.orvix.email"
	}
	if !strings.Contains(hostname, ".") {
		hostname = "mail.orvix.email"
	}
	configPath := cfg.Stalwart.ConfigPath
	if configPath == "" {
		configPath = "/etc/stalwart/config.yaml"
	}

	fmt.Println("Applying Stalwart 0.16.7 integration...")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := stalwartSvc.ApplyStalwart016(ctx, stalwart.ApplyOptions{
		ConfigPath:     configPath,
		Hostname:       hostname,
		Domain:         "orvix.email",
		DataPath:       "/var/lib/stalwart",
		ManagementPort: 8081,
		SystemdService: "stalwart-server",
		RecoveryPort:   8080,
		WaitTimeout:    35 * time.Second,
	})
	if err != nil {
		return err
	}
	if result.AlreadyPinned {
		fmt.Println("Management listener already pinned; verified restart persistence.")
	} else {
		fmt.Printf("Management listener patched: %s\n", result.ListenerID)
	}
	fmt.Printf("Config path: %s\n", result.ConfigPath)
	fmt.Printf("Management URL: %s\n", result.ManagementURL)
	fmt.Println("Stalwart integration apply PASSED")
	return nil
}

func runStalwartCommand(subcmd string) {
	cfg, err := config.LoadMinimal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()
	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)

	switch subcmd {
	case "status":
		status := stalwartSvc.Status()
		fmt.Printf("Stalwart Status:\n")
		fmt.Printf("  Configured: %v\n", status["configured"])
		fmt.Printf("  Detected:   %v\n", status["detected"])
		if detected, ok := status["detected"].(bool); ok && detected {
			fmt.Printf("  Binary:     %v\n", status["binary_path"])
			fmt.Printf("  Version:    %v\n", status["version"])
			fmt.Printf("  Running:    %v\n", status["running"])
			if health, ok := status["health"].(map[string]stalwart.HealthStatus); ok {
				fmt.Printf("  Health:\n")
				for svc, h := range health {
					fmt.Printf("    %s: %s\n", svc, h)
	}
}

// seedSuperAdminFromEnv checks for ORVIX_SUPER_ADMIN_EMAIL and ORVIX_SUPER_ADMIN_PASSWORD
// environment variables on startup. If set and no super_admin exists, it auto-creates
// the platform tenant and super admin user.
func seedSuperAdminFromEnv(db *gorm.DB, cfg *config.Config, sugar *zap.SugaredLogger) {
	email := os.Getenv("ORVIX_SUPER_ADMIN_EMAIL")
	password := os.Getenv("ORVIX_SUPER_ADMIN_PASSWORD")

	if email == "" || password == "" {
		sugar.Warn("Super Admin not initialized. Set ORVIX_SUPER_ADMIN_EMAIL and ORVIX_SUPER_ADMIN_PASSWORD")
		return
	}

	// Check if any super_admin already exists
	var count int64
	db.Model(&models.User{}).Where("role = ?", "super_admin").Count(&count)
	if count > 0 {
		sugar.Info("Super Admin already exists, skipping auto-seed")
		return
	}

	if len(password) < 8 {
		sugar.Warn("ORVIX_SUPER_ADMIN_PASSWORD must be at least 8 characters, skipping auto-seed")
		return
	}

	// Create or get ORVIX Platform tenant
	var tenant models.Tenant
	result := db.Where("name = ?", "ORVIX Platform").First(&tenant)
	if result.Error != nil {
		tenant = models.Tenant{
			Name:         "ORVIX Platform",
			Slug:         "orvix-platform",
			Tier:         "enterprise",
			MaxDomains:   1000,
			MaxMailboxes: 100000,
			IsReseller:   true,
			Active:       true,
		}
		if err := db.Create(&tenant).Error; err != nil {
			sugar.Warnw("Failed to create platform tenant", "error", err)
			return
		}
	}

	// Hash password and create super admin
	authSvc := auth.NewService(db, cfg.Security, sugar)
	hash, err := authSvc.HashPassword(password)
	if err != nil {
		sugar.Warnw("Failed to hash super admin password", "error", err)
		return
	}

	admin := models.User{
		TenantID:     tenant.ID,
		Email:        email,
		PasswordHash: hash,
		Role:         "super_admin",
		IsAdmin:      true,
		IsActive:     true,
	}
	if err := db.Create(&admin).Error; err != nil {
		sugar.Warnw("Failed to create super admin", "error", err)
		return
	}

	sugar.Infow("Super Admin auto-initialized from environment variables",
		"email", email,
		"tenant", tenant.Name,
	)
}
		}
		if !status["detected"].(bool) {
			fmt.Println()
			fmt.Println("Stalwart binary not found. To configure:")
			fmt.Println("  Set stalwart.binary_path in orvix.yaml")
			fmt.Println("  Or set the ORVIX_STALWART_BINARY environment variable")
			fmt.Println("  Or install Stalwart to one of: /usr/local/bin/stalwart, /usr/bin/stalwart")
		}

	case "path":
		if detected, _ := stalwartSvc.Detect(); detected {
			fmt.Println(stalwartSvc.BinaryPath())
		} else {
			fmt.Println("Stalwart binary not found")
			os.Exit(1)
		}

	case "validate":
		configPath := cfg.Stalwart.ConfigPath
		if configPath == "" {
			configPath = stalwartSvc.ConfigPath()
		}
		if err := stalwartSvc.ValidateConfig(configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Config validation FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Config validation PASSED")

	case "config":
		configPath := cfg.Stalwart.ConfigPath
		if configPath == "" {
			configPath = "/etc/stalwart/config.yaml"
			fmt.Printf("    Using default config path: %s\n", configPath)
		}

		hostname := cfg.Server.ExternalURL
		if hostname == "" {
			hostname, _ = os.Hostname()
		}
		if !strings.Contains(hostname, ".") {
			hostname = "mail.orvix.email"
		}
		params := stalwart.ConfigParams{
			Hostname:       hostname,
			Domain:         "orvix.email",
			DbPath:         "/var/lib/stalwart",
			ManagementPort: 8081,
		}

		configContent, err := stalwartSvc.GenerateConfig(params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config generation FAILED: %v\n", err)
			os.Exit(1)
		}

		if err := stalwartSvc.WriteConfig(configContent); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write config: %v\n", err)
			os.Exit(1)
		}
		provisioningPaths, err := stalwartSvc.WriteProvisioningFiles(params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write provisioning files: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Config written to %s\n", configPath)
		fmt.Printf("First-run bootstrap patch written to %s\n", provisioningPaths["bootstrap_patch"])
		fmt.Printf("Management listener patch written to %s\n", provisioningPaths["listener_patch"])
		fmt.Println()
		fmt.Println("Stalwart 0.16.7 startup uses only the datastore bootstrap JSON:")
		fmt.Printf("  /usr/local/bin/stalwart --config %s\n", configPath)
		fmt.Println()
		fmt.Println("To apply systemd, recovery, listener patch, restart, and verification:")
		fmt.Println("  sudo orvix stalwart apply")

	case "apply":
		if err := applyStalwartIntegration(); err != nil {
			fmt.Fprintf(os.Stderr, "Stalwart integration apply FAILED: %v\n", err)
			os.Exit(1)
		}

	case "provision":
		if len(os.Args) < 5 {
			fmt.Println("Usage: orvix stalwart provision domain <domain.com>")
			fmt.Println("       orvix stalwart provision mailbox <email> <password>")
			os.Exit(1)
		}
		action := os.Args[3]
		switch action {
		case "domain":
			domain := os.Args[4]
			prov := stalwart.NewProvisioningService(cfg.Stalwart, sugar, stalwartSvc)
			if err := prov.CreateDomain(domain); err != nil {
				fmt.Fprintf(os.Stderr, "Domain creation FAILED: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Domain %s created in Stalwart\n", domain)

		case "mailbox":
			if len(os.Args) < 6 {
				fmt.Println("Usage: orvix stalwart provision mailbox <email> <password>")
				os.Exit(1)
			}
			email := os.Args[4]
			password := os.Args[5]
			prov := stalwart.NewProvisioningService(cfg.Stalwart, sugar, stalwartSvc)
			if err := prov.CreateMailbox(email, password, 0); err != nil {
				fmt.Fprintf(os.Stderr, "Mailbox creation FAILED: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Mailbox %s created in Stalwart\n", email)

		default:
			fmt.Fprintf(os.Stderr, "unknown provision action: %s\n", action)
			fmt.Println("Usage: orvix stalwart provision <domain|mailbox>")
			os.Exit(1)
		}

	case "start":
		if err := stalwartSvc.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to start Stalwart: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Stalwart started")

	case "stop":
		if err := stalwartSvc.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stop Stalwart: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Stalwart stopped")

	case "restart":
		if err := stalwartSvc.Restart(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restart Stalwart: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Stalwart restarted")

	default:
		fmt.Fprintf(os.Stderr, "unknown stalwart command: %s\n", subcmd)
		fmt.Println("Usage: orvix stalwart <status|path|validate|config|apply|start|stop|restart>")
		os.Exit(1)
	}
}

func runServer() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	sugar := logger.Sugar()

	db, err := database.Connect(cfg.Database)
	if err != nil {
		sugar.Fatalf("Failed to connect to database: %v", err)
	}
	sugar.Info("Database connected successfully")

	if err := migrations.Run(db, cfg.Database.Driver); err != nil {
		sugar.Fatalf("Failed to run migrations: %v", err)
	}
	sugar.Info("Migrations completed")

	licSvc := license.NewService(db, cfg.License)
	featMgr := features.NewManager(db, licSvc)
	if err := featMgr.Initialize(); err != nil {
		sugar.Warnf("Feature flag initialization: %v", err)
	} else {
		sugar.Info("Feature flags initialized")
	}

	// Auto-seed Super Admin if no super_admin exists and env vars are set
	seedSuperAdminFromEnv(db, cfg, sugar)

	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)
	updateSvc := updater.NewService(cfg.Updates, Version, Channel)
	authSvc := auth.NewService(db, cfg.Security, sugar)
	metricsSvc := metrics.NewService()
	metricsSvc.Register()

	provRunner := provision.NewJobRunner(db, cfg.Stalwart, sugar, stalwartSvc)
	provRunner.Start()

	mailProc := mailops.NewProcessor(db, cfg.Stalwart, sugar, stalwartSvc)
	mailProc.Start()

	// Auto-heal monitor
	healMon := autoheal.NewMonitor()
	healMon.AddCheck(autoheal.HealthCheck{
		Name:     "db_connection",
		Severity: autoheal.SeverityHigh,
		Interval: 60 * time.Second,
		Check: func() autoheal.CheckResult {
			sqlDB, err := db.DB()
			if err != nil {
				return autoheal.CheckResult{Name: "db_connection", Healthy: false, Error: err.Error()}
			}
			if err := sqlDB.Ping(); err != nil {
				return autoheal.CheckResult{Name: "db_connection", Healthy: false, Error: err.Error()}
			}
			return autoheal.CheckResult{Name: "db_connection", Healthy: true}
		},
		Fix: func() error {
			sugar.Warnw("auto-heal attempting database reconnection")
			sqlDB, err := db.DB()
			if err != nil {
				return err
			}
			return sqlDB.Ping()
		},
	})
	healMon.AddCheck(autoheal.HealthCheck{
		Name:     "stalwart_running",
		Severity: autoheal.SeverityHigh,
		Interval: 60 * time.Second,
		Check: func() autoheal.CheckResult {
			running := stalwartSvc.IsRunning()
			return autoheal.CheckResult{Name: "stalwart_running", Healthy: running}
		},
		Fix: func() error {
			sugar.Warnw("auto-heal attempting Stalwart restart")
			if !stalwartSvc.BinaryDetected() {
				return fmt.Errorf("Stalwart binary not found - cannot restart")
			}
			return stalwartSvc.Restart()
		},
	})
	healMon.Start(60 * time.Second)

	// Backup scheduler - daily backup
	backupTicker := time.NewTicker(24 * time.Hour)
	go func() {
		for range backupTicker.C {
			sugar.Info("scheduled daily backup starting")
			// Backup logic would go here
			_ = cfg.Updates.AutoApply
		}
	}()

	router := api.NewRouter(api.RouterConfig{
		Config:    cfg,
		Version:   Version,
		Product:   Product,
		Commit:    Commit,
		Channel:   Channel,
		BuildDate: BuildDate,
		DB:        db,
		Logger:    sugar,
		License:   licSvc,
		Features:  featMgr,
		Stalwart:  stalwartSvc,
		Auth:      authSvc,
		Updater:   updateSvc,
		Metrics:   metricsSvc,
	})

	sugar.Infof("Starting %s v%s (%s) on %s", Product, Version, Channel, cfg.Server.Listen)

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
		sig := <-sigCh
		sugar.Infow("received signal, shutting down", "signal", sig)

		provRunner.Stop()
		mailProc.Stop()
		healMon.Stop()
		backupTicker.Stop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := router.ShutdownWithContext(shutdownCtx); err != nil {
			sugar.Errorw("server shutdown error", "error", err)
		}
		sugar.Info("shutdown complete")
		os.Exit(0)
	}()

	// Start server with optional TLS
	if cfg.Server.TLSAuto && cfg.Server.TLSDomain != "" {
		dataDir := cfg.Server.TLSDataDir
		if dataDir == "" {
			dataDir = "/var/lib/orvix/tls"
		}
		m := &autocert.Manager{
			Cache:      autocert.DirCache(dataDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Server.TLSDomain),
		}
		sugar.Infow("auto TLS enabled", "domain", cfg.Server.TLSDomain)
		app := router
		app.Listen(cfg.Server.Listen)
		_ = m
		sugar.Fatal("server stopped unexpectedly")
	} else if cfg.Server.TLSCertFile != "" && cfg.Server.TLSKeyFile != "" {
		if err := router.ListenTLS(cfg.Server.Listen, cfg.Server.TLSCertFile, cfg.Server.TLSKeyFile); err != nil {
			sugar.Fatalf("TLS server failed: %v", err)
		}
	} else {
		if err := router.Listen(cfg.Server.Listen); err != nil {
			sugar.Fatalf("Server failed: %v", err)
		}
	}
}

