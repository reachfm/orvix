package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
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
		runUpdateApply()
	case "update-rollback":
		runUpdateRollback()
	case "seed-superadmin":
		runSeedSuperAdmin()
	case "stalwart":
		if len(os.Args) < 3 {
			fmt.Println("Usage: orvix stalwart <status|path|validate|config|provision|start|stop|restart>")
			return
		}
		runStalwartCommand(os.Args[2])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Printf(`OrvixEM v%s

Usage:
  orvix                       Start the server
  orvix start                 Start the server
  orvix serve                 Start the server
  orvix status                Show server status
  orvix doctor                Run system diagnostics
  orvix migrate               Run database migrations
  orvix routes                List registered API routes
  orvix features              List all feature flags
  orvix seed-superadmin       Create or update the platform Super Admin account
  orvix update-check          Check for updates
  orvix update-apply          Apply available update
  orvix update-rollback       Rollback to previous version
  orvix stalwart              Manage Stalwart mail server
  orvix version               Show version information
  orvix help                  Show this help
`, Version)
}

func runQuickStatus() {
	cfg, err := config.LoadMinimal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	logger, _ := zap.NewProduction()
	sugar := logger.Sugar()
	stalwartSvc := stalwart.NewService(cfg.Stalwart, sugar)
	detected, path := stalwartSvc.Detect()
	_, adminErr := web.FrontendFS.ReadDir("admin/dist")
	_, webmailErr := web.FrontendFS.ReadDir("webmail/dist")
	_, portalErr := web.FrontendFS.ReadDir("portal/dist")
	fmt.Printf("%s v%s\n", Product, Version)
	fmt.Printf("Channel: %s\n", Channel)
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
	configPaths := []string{"orvix.yaml", "./configs/orvix.yaml", "/etc/orvix/orvix.yaml"}
	for _, p := range configPaths {
		if _, err := os.Stat(p); err == nil {
			fmt.Printf("Config: found at %s\n", p)
			break
		}
	}
	db, err := database.Connect(cfg.Database)
	if err != nil {
		fmt.Printf("Database: FAILED - %v\n", err)
	} else {
		sqlDB, _ := db.DB()
		if err := sqlDB.Ping(); err != nil {
			fmt.Printf("Database: UNREACHABLE - %v\n", err)
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
	if _, err := web.FrontendFS.ReadDir("admin/dist"); err == nil {
		fmt.Println("Frontend Admin: AVAILABLE")
	}
	hostname, _ := os.Hostname()
	fmt.Printf("Hostname: %s\n", hostname)
	fmt.Println("=== Doctor complete ===")
}

func runMigrate() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
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
	fmt.Println("=== API Routes ===")
	fmt.Println("  127+ endpoints registered under /api/v1")
	fmt.Println("  Run the server and visit /api/v1/* for details")
}

func runFeatures() {
	cfg, err := config.LoadMinimal()
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
	featMgr.Initialize()
	flags := featMgr.GetAllFlags()
	fmt.Printf("=== Feature Flags (%d total) ===\n", len(flags))
	for _, ff := range flags {
		status := "disabled"
		if ff.Enabled {
			status = "enabled"
		}
		fmt.Printf("  %-30s %s:%s\n", ff.Key, ff.Tier, status)
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
	if result.Available && result.Release != nil {
		fmt.Printf("Update available: %s\n", result.Release.Version)
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
	fmt.Printf("Update to %s applied!\n", result.Release.Version)
	fmt.Println("Please restart the service.")
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
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
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

	email := ""
	password := ""
	for i, arg := range os.Args {
		if (arg == "--email" || arg == "-e") && i+1 < len(os.Args) {
			email = os.Args[i+1]
		}
		if (arg == "--password" || arg == "-p") && i+1 < len(os.Args) {
			password = os.Args[i+1]
		}
	}
	if email == "" || password == "" {
		fmt.Fprintf(os.Stderr, "Usage: orvix seed-superadmin --email=admin@example.com --password=StrongPass123\n")
		os.Exit(1)
	}

	authSvc := auth.NewService(db, cfg.Security, sugar)

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
			sugar.Fatalf("Failed to create platform tenant: %v", err)
		}
		fmt.Printf("Created platform tenant: %s (id=%d)\n", tenant.Name, tenant.ID)
	}

	hash, err := authSvc.HashPassword(password)
	if err != nil {
		sugar.Fatalf("Failed to hash password: %v", err)
	}

	var admin models.User
	if err := db.Where("email = ?", email).First(&admin).Error; err != nil {
		admin = models.User{
			TenantID:     tenant.ID,
			Email:        email,
			PasswordHash: hash,
			Role:         "super_admin",
			IsAdmin:      true,
			IsActive:     true,
		}
		if err := db.Create(&admin).Error; err != nil {
			sugar.Fatalf("Failed to create super admin: %v", err)
		}
		fmt.Printf("Created super admin: %s (id=%d)\n", admin.Email, admin.ID)
	} else {
		admin.PasswordHash = hash
		admin.Role = "super_admin"
		admin.IsAdmin = true
		admin.IsActive = true
		admin.TenantID = tenant.ID
		db.Save(&admin)
		fmt.Printf("Updated super admin: %s (id=%d, role=%s)\n", admin.Email, admin.ID, admin.Role)
	}

	fmt.Println()
	fmt.Println("Seed complete. Login credentials:")
	fmt.Printf("  POST /api/v1/auth/login {\"email\":\"%s\",\"password\":\"...\"}\n", email)
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
		}
		if !status["detected"].(bool) {
			fmt.Println("\nStalwart binary not found.")
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
		params := stalwart.ConfigParams{
			Hostname:       cfg.Server.ExternalURL,
			SMTPAddress:    "0.0.0.0",
			IMAPAddress:    "0.0.0.0",
			ManagementPort: 8081,
		}
		configContent, err := stalwartSvc.GenerateConfig(params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Config generation FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(configContent)
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

	healMon := autoheal.NewMonitor()
	healMon.AddCheck(autoheal.HealthCheck{
		Name: "db_connection", Severity: autoheal.SeverityHigh, Interval: 60 * time.Second,
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
	})
	healMon.AddCheck(autoheal.HealthCheck{
		Name: "stalwart_running", Severity: autoheal.SeverityHigh, Interval: 60 * time.Second,
		Check: func() autoheal.CheckResult {
			return autoheal.CheckResult{Name: "stalwart_running", Healthy: stalwartSvc.IsRunning()}
		},
	})
	healMon.Start(60 * time.Second)

	backupTicker := time.NewTicker(24 * time.Hour)
	go func() {
		for range backupTicker.C {
			sugar.Info("scheduled daily backup starting")
		}
	}()

	router := api.NewRouter(api.RouterConfig{
		Config: cfg, Version: Version, Product: Product, Commit: Commit,
		Channel: Channel, BuildDate: BuildDate, DB: db, Logger: sugar,
		License: licSvc, Features: featMgr, Stalwart: stalwartSvc,
		Auth: authSvc, Updater: updateSvc, Metrics: metricsSvc,
	})

	sugar.Infof("Starting %s v%s (%s) on %s", Product, Version, Channel, cfg.Server.Listen)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		sugar.Infow("shutting down", "signal", sig)
		provRunner.Stop()
		mailProc.Stop()
		healMon.Stop()
		backupTicker.Stop()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		router.ShutdownWithContext(ctx)
		sugar.Info("shutdown complete")
		os.Exit(0)
	}()

	if err := router.Listen(cfg.Server.Listen); err != nil {
		sugar.Fatalf("Server failed: %v", err)
	}
}

func seedSuperAdminFromEnv(db *gorm.DB, cfg *config.Config, sugar *zap.SugaredLogger) {
	email := os.Getenv("ORVIX_SUPER_ADMIN_EMAIL")
	password := os.Getenv("ORVIX_SUPER_ADMIN_PASSWORD")
	if email == "" || password == "" {
		sugar.Warn("Super Admin not initialized. Set ORVIX_SUPER_ADMIN_EMAIL and ORVIX_SUPER_ADMIN_PASSWORD")
		return
	}
	var count int64
	db.Model(&models.User{}).Where("role = ?", "super_admin").Count(&count)
	if count > 0 {
		sugar.Info("Super Admin already exists")
		return
	}
	var tenant models.Tenant
	if err := db.Where("name = ?", "ORVIX Platform").First(&tenant).Error; err != nil {
		tenant = models.Tenant{
			Name: "ORVIX Platform", Slug: "orvix-platform", Tier: "enterprise",
			MaxDomains: 1000, MaxMailboxes: 100000, IsReseller: true, Active: true,
		}
		if err := db.Create(&tenant).Error; err != nil {
			sugar.Warnw("Failed to create platform tenant", "error", err)
			return
		}
	}
	authSvc := auth.NewService(db, cfg.Security, sugar)
	hash, err := authSvc.HashPassword(password)
	if err != nil {
		sugar.Warnw("Failed to hash password", "error", err)
		return
	}
	admin := models.User{
		TenantID: tenant.ID, Email: email, PasswordHash: hash,
		Role: "super_admin", IsAdmin: true, IsActive: true,
	}
	if err := db.Create(&admin).Error; err != nil {
		sugar.Warnw("Failed to create super admin", "error", err)
		return
	}
	sugar.Infow("Super Admin auto-initialized", "email", email)
}
