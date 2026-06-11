package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config holds all Orvix configuration values.
type Config struct {
	logger   *zap.Logger
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Redis    RedisConfig    `mapstructure:"redis"`
	Auth     AuthConfig     `mapstructure:"auth"`
	License  LicenseConfig  `mapstructure:"license"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Metrics  MetricsConfig  `mapstructure:"metrics"`
	Update   UpdateConfig   `mapstructure:"update"`
	AI       AIConfig       `mapstructure:"ai"`
	DNS      DNSConfig      `mapstructure:"dns"`
	ClamAV   ClamAVConfig   `mapstructure:"clamav"`
	Backup   BackupConfig   `mapstructure:"backup"`
	CoreMail CoreMailConfig `mapstructure:"coremail"`
}

// CoreMailConfig controls the native CoreMail protocol runtime.
type CoreMailConfig struct {
	Enabled                   bool          `mapstructure:"enabled"`
	Hostname                  string        `mapstructure:"hostname"`
	LicenseFilePath           string        `mapstructure:"license_file_path"`
	LicenseAuthorityCachePath string        `mapstructure:"license_authority_cache_path"`
	LicenseAuthorityURL       string        `mapstructure:"license_authority_url"`
	LicenseAuthorityTimeout   time.Duration `mapstructure:"license_authority_timeout"`
	LicenseAuthorityTestMode  bool          `mapstructure:"license_authority_test_mode"`
	DataPath                  string        `mapstructure:"data_path"`
	MailStorePath             string        `mapstructure:"mailstore_path"`
	SMTPHost                  string        `mapstructure:"smtp_host"`
	SMTPPort                  int           `mapstructure:"smtp_port"`
	IMAPHost                  string        `mapstructure:"imap_host"`
	IMAPPort                  int           `mapstructure:"imap_port"`
	POP3Host                  string        `mapstructure:"pop3_host"`
	POP3Port                  int           `mapstructure:"pop3_port"`
	JMAPHost                  string        `mapstructure:"jmap_host"`
	JMAPPort                  int           `mapstructure:"jmap_port"`
	TLSCertFile               string        `mapstructure:"tls_cert_file"`
	TLSKeyFile                string        `mapstructure:"tls_key_file"`
	RequireTLSForAuth         bool          `mapstructure:"require_tls_for_auth"`
	QueueWorkers              int           `mapstructure:"queue_workers"`
	WorkerInterval            time.Duration `mapstructure:"worker_interval"`
}

// ClamAVConfig holds ClamAV antivirus scanner settings.
type ClamAVConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// BackupConfig holds backup settings.
type BackupConfig struct {
	Dir string `mapstructure:"dir"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host           string        `mapstructure:"host"`
	Port           int           `mapstructure:"port"`
	AdminPort      int           `mapstructure:"admin_port"`
	ReadTimeout    time.Duration `mapstructure:"read_timeout"`
	WriteTimeout   time.Duration `mapstructure:"write_timeout"`
	IdleTimeout    time.Duration `mapstructure:"idle_timeout"`
	BodyLimit      int           `mapstructure:"body_limit"`
	TLSCertFile    string        `mapstructure:"tls_cert_file"`
	TLSKeyFile     string        `mapstructure:"tls_key_file"`
	TLSAuto        bool          `mapstructure:"tls_auto"`
	TLSHostname    string        `mapstructure:"tls_hostname"`
	TLSCacheDir    string        `mapstructure:"tls_cache_dir"`
	TLSEmail       string        `mapstructure:"tls_email"`
	AdminUIDir     string        `mapstructure:"admin_ui_dir"`
	WebmailUIDir   string        `mapstructure:"webmail_ui_dir"`
	AllowedOrigins []string      `mapstructure:"allowed_origins"`
	TrustedProxies []string      `mapstructure:"trusted_proxies"`
}

// DatabaseConfig holds database connection settings.
type DatabaseConfig struct {
	Driver      string `mapstructure:"driver"`
	DSN         string `mapstructure:"dsn"`
	Host        string `mapstructure:"host"`
	Port        int    `mapstructure:"port"`
	User        string `mapstructure:"user"`
	Password    string `mapstructure:"password"`
	DBName      string `mapstructure:"dbname"`
	SSLMode     string `mapstructure:"sslmode"`
	MaxOpen     int    `mapstructure:"max_open"`
	MaxIdle     int    `mapstructure:"max_idle"`
	MaxLifetime int    `mapstructure:"max_lifetime"`
	SQLitePath  string `mapstructure:"sqlite_path"`
}

// RedisConfig holds Redis connection settings.
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	JWTSecret      string        `mapstructure:"jwt_secret"`
	JWTKeyPath     string        `mapstructure:"jwt_key_path"`
	JWTAccessTTL   time.Duration `mapstructure:"jwt_access_ttl"`
	JWTRefreshTTL  time.Duration `mapstructure:"jwt_refresh_ttl"`
	PasswordMinLen int           `mapstructure:"password_min_len"`
	Argon2Time     uint32        `mapstructure:"argon2_time"`
	Argon2Memory   uint32        `mapstructure:"argon2_memory"`
	Argon2Threads  uint8         `mapstructure:"argon2_threads"`
	LoginRateLimit int           `mapstructure:"login_rate_limit"`
	RateWindow     time.Duration `mapstructure:"rate_window"`
}

// LicenseConfig holds license validation settings.
type LicenseConfig struct {
	PublicKeyPath string `mapstructure:"public_key_path"`
	OfflineMode   bool   `mapstructure:"offline_mode"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
	LogDir string `mapstructure:"log_dir"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Path    string `mapstructure:"path"`
}

// UpdateConfig holds auto-update settings.
type UpdateConfig struct {
	Channel      string `mapstructure:"channel"`
	CheckURL     string `mapstructure:"check_url"`
	AutoApply    bool   `mapstructure:"auto_apply"`
	BackupBefore bool   `mapstructure:"backup_before"`
}

// AIConfig holds AI integration settings.
type AIConfig struct {
	DeepSeekAPIKey string `mapstructure:"deepseek_api_key"`
	DeepSeekModel  string `mapstructure:"deepseek_model"`
	OllamaURL      string `mapstructure:"ollama_url"`
	OllamaModel    string `mapstructure:"ollama_model"`
	UseOllama      bool   `mapstructure:"use_ollama"`
}

// DNSConfig holds DNS automation settings.
type DNSConfig struct {
	CloudflareAPIKey string `mapstructure:"cloudflare_api_key"`
	Route53AccessKey string `mapstructure:"route53_access_key"`
	Route53SecretKey string `mapstructure:"route53_secret_key"`
	DefaultProvider  string `mapstructure:"default_provider"`
}

// Defaults returns a Config populated with secure defaults.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host:           "0.0.0.0",
			Port:           80,
			AdminPort:      8080,
			ReadTimeout:    60 * time.Second,
			WriteTimeout:   60 * time.Second,
			IdleTimeout:    120 * time.Second,
			BodyLimit:      50 * 1024 * 1024,
			TLSAuto:        false,
			TLSCacheDir:    "/var/lib/orvix/cert-cache",
			AdminUIDir:     "/usr/share/orvix/admin",
			WebmailUIDir:   "/usr/share/orvix/webmail",
			AllowedOrigins: []string{},
			TrustedProxies: []string{},
		},
		Database: DatabaseConfig{
			Driver:      "sqlite",
			Host:        "localhost",
			Port:        5432,
			User:        "orvix",
			DBName:      "orvix",
			SSLMode:     "disable",
			MaxOpen:     25,
			MaxIdle:     5,
			MaxLifetime: 300,
			SQLitePath:  "/var/lib/orvix/orvix.db",
		},
		Redis: RedisConfig{
			Host: "localhost",
			Port: 6379,
			DB:   0,
		},
		Auth: AuthConfig{
			JWTKeyPath:     "/var/lib/orvix/jwt_key.pem",
			JWTAccessTTL:   15 * time.Minute,
			JWTRefreshTTL:  30 * 24 * time.Hour,
			PasswordMinLen: 8,
			Argon2Time:     3,
			Argon2Memory:   64 * 1024,
			Argon2Threads:  4,
			LoginRateLimit: 5,
			RateWindow:     15 * time.Minute,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
			LogDir: "/var/log/orvix",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
		Update: UpdateConfig{
			Channel:      "stable",
			AutoApply:    false,
			BackupBefore: true,
		},
		AI: AIConfig{
			DeepSeekModel: "deepseek-chat",
			OllamaURL:     "http://localhost:11434",
			OllamaModel:   "llama3",
			UseOllama:     false,
		},
		DNS: DNSConfig{
			DefaultProvider: "cloudflare",
		},
		ClamAV: ClamAVConfig{
			Host: "localhost",
			Port: 3310,
		},
		Backup: BackupConfig{
			Dir: "/var/lib/orvix/backups",
		},
		CoreMail: CoreMailConfig{
			Enabled:           false,
			Hostname:          "mail.local",
			MailStorePath:     "/var/lib/orvix/mailstore",
			SMTPHost:          "0.0.0.0",
			SMTPPort:          25,
			IMAPHost:          "0.0.0.0",
			IMAPPort:          143,
			POP3Host:          "0.0.0.0",
			POP3Port:          110,
			RequireTLSForAuth: true,
			QueueWorkers:      1,
			WorkerInterval:    5 * time.Second,
		},
		License: LicenseConfig{
			OfflineMode: true,
		},
	}
}

// Load reads configuration from file, ENV, and defaults.
func Load(logger *zap.Logger) (*Config, error) {
	v := viper.New()
	v.SetConfigName("orvix")
	v.SetConfigType("yaml")

	v.AddConfigPath("/etc/orvix")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")

	_ = v.ReadInConfig()

	v.SetEnvPrefix("ORVIX")
	v.AutomaticEnv()

	cfg := Defaults()
	cfg.SetLogger(logger)

	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyEnvOverrides(v, cfg)

	cfg.validate()

	logger.Info("configuration loaded",
		zap.String("driver", cfg.Database.Driver),
		zap.String("server", fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)),
		zap.String("log_level", cfg.Logging.Level),
	)

	return cfg, nil
}

func applyEnvOverrides(v *viper.Viper, cfg *Config) {
	if v.GetString("DATABASE_PASSWORD") != "" {
		cfg.Database.Password = v.GetString("DATABASE_PASSWORD")
	}
	if v.GetString("REDIS_PASSWORD") != "" {
		cfg.Redis.Password = v.GetString("REDIS_PASSWORD")
	}
	if v.GetString("JWT_SECRET") != "" {
		cfg.Auth.JWTSecret = v.GetString("JWT_SECRET")
	}
	if v.GetString("DEEPSEEK_API_KEY") != "" {
		cfg.AI.DeepSeekAPIKey = v.GetString("DEEPSEEK_API_KEY")
	}
	if v.GetString("CLOUDFLARE_API_KEY") != "" {
		cfg.DNS.CloudflareAPIKey = v.GetString("CLOUDFLARE_API_KEY")
	}
	if v.GetString("COREMAIL_ENABLED") != "" {
		cfg.CoreMail.Enabled = v.GetBool("COREMAIL_ENABLED")
	}
}

// SetLogger stores a logger instance on the config.
func (c *Config) SetLogger(logger *zap.Logger) {
	c.logger = logger
}

// GetLogger returns the config's logger instance.
func (c *Config) GetLogger() *zap.Logger {
	return c.logger
}

func (c *Config) validate() {
	if c.CoreMail.DataPath != "" {
		c.CoreMail.MailStorePath = c.CoreMail.DataPath
	}
	if c.Database.DSN == "" {
		switch c.Database.Driver {
		case "postgres":
			c.Database.DSN = fmt.Sprintf("host=%s port=%d user=%s dbname=%s password=%s sslmode=%s",
				c.Database.Host, c.Database.Port, c.Database.User,
				c.Database.DBName, c.Database.Password, c.Database.SSLMode)
		case "sqlite":
			c.Database.DSN = c.Database.SQLitePath
		}
	}
}
