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
	Outbound OutboundConfig `mapstructure:"outbound"`
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
	SubmissionEnabled         bool          `mapstructure:"submission_enabled"`
	SubmissionHost            string        `mapstructure:"submission_host"`
	SubmissionPort            int           `mapstructure:"submission_port"`
	SMTPsEnabled              bool          `mapstructure:"smtps_enabled"`
	SMTPsHost                 string        `mapstructure:"smtps_host"`
	SMTPsPort                 int           `mapstructure:"smtps_port"`
	IMAPHost                  string        `mapstructure:"imap_host"`
	IMAPPort                  int           `mapstructure:"imap_port"`
	POP3Host                  string        `mapstructure:"pop3_host"`
	POP3Port                  int           `mapstructure:"pop3_port"`
	JMAPHost                  string        `mapstructure:"jmap_host"`
	JMAPPort                  int           `mapstructure:"jmap_port"`
	TLSCertFile               string        `mapstructure:"tls_cert_file"`
	TLSKeyFile                string        `mapstructure:"tls_key_file"`
	RequireTLSForAuth         bool          `mapstructure:"require_tls_for_auth"`
	RequireAuthForSubmission  bool          `mapstructure:"require_auth_for_submission"`
	QueueWorkers              int           `mapstructure:"queue_workers"`
	WorkerInterval            time.Duration `mapstructure:"worker_interval"`
}

// ClamAVConfig holds ClamAV antivirus scanner settings.
type ClamAVConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

// OutboundConfig controls outbound SMTP delivery behavior.
type OutboundConfig struct {
	PreferIPv4 bool `mapstructure:"prefer_ipv4"`
}

// BackupConfig holds backup settings.
type BackupConfig struct {
	Dir            string `mapstructure:"dir"`
	RetentionCount int    `mapstructure:"retention_count"`
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
	// Hostname the operator points their DNS A record at for
	// the admin UI and admin API. Filled in by the installer
	// as "admin.<primary_domain>". Used by the router to scope
	// CORS allowlists and trusted redirect targets. Empty
	// means "derive from the request Host header at runtime"
	// which keeps a localhost / docker dev setup working
	// without a real hostname.
	AdminHost string `mapstructure:"admin_host"`
	// Hostname the operator points their DNS A record at for
	// the user-facing webmail. Filled in by the installer as
	// "webmail.<primary_domain>". When empty, the router
	// falls back to the request Host header. The webmail SPA
	// must always be served under a stable hostname so the
	// browser can scope the access_token cookie to a single
	// origin.
	WebmailHost string `mapstructure:"webmail_host"`
	// Hostname used by the CoreMail runtime for SMTP/IMAP/
	// POP3/JMAP listeners. Filled in by the installer as
	// "mail.<primary_domain>". Also referenced in the TLS
	// certificate SAN list.
	MailHost string `mapstructure:"mail_host"`
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

// DefaultPasswordMinLen is the default minimum password length enforced by all auth and mailbox handlers.
const DefaultPasswordMinLen = 8

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
	// Domain attribute set on every auth cookie. The installer
	// writes ".parent.com" so the same access_token cookie is
	// sent to admin.<parent> AND webmail.<parent> (single
	// sign-on across subdomains). Empty means "do not set a
	// Domain attribute" which is the right default for a
	// localhost / docker dev setup where admin and webmail
	// share a single hostname.
	CookieDomain string `mapstructure:"cookie_domain"`
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
	FeedURL      string `mapstructure:"feed_url"`
	AutoApply    bool   `mapstructure:"auto_apply"`
	BackupBefore bool   `mapstructure:"backup_before"`
	// WorkspaceRoot is the absolute path to the directory that
	// contains the runtime update script. The default is the
	// process working directory. The Update Management v1 handler
	// resolves the runtime script path against this root and
	// refuses to exec anything outside it.
	WorkspaceRoot string `mapstructure:"workspace_root"`
}

// AIConfig holds AI integration settings.
type AIConfig struct {
	DeepSeekAPIKey string `mapstructure:"deepseek_api_key"`
	DeepSeekModel  string `mapstructure:"deepseek_model"`
	OllamaURL      string `mapstructure:"ollama_url"`
	OllamaModel    string `mapstructure:"ollama_model"`
	UseOllama      bool   `mapstructure:"use_ollama"`
}

// DNSConfig holds DNS automation settings. All tokens are
// server-side only — the handlers in internal/api/handlers/dns_ops.go
// never echo any field value to a client; the admin dashboard only
// learns whether the field is set (boolean). Operators supply
// these via env (ORVIX_DNS_CLOUDFLARE_API_KEY etc.) or config
// file; installer scripts that write these fields run with root
// privileges so the file is not world-readable.
type DNSConfig struct {
	// PublicIPv4 / PublicIPv6 are the public mail server IPs the
	// DNS Ops plan generator emits in the A / AAAA / SPF records.
	// They are intentionally SEPARATE from coremail.smtp_host
	// (which is the listener bind address and defaults to
	// 0.0.0.0). Using the listener bind address for the public
	// DNS plan would either fabricate 0.0.0.0 records on a fresh
	// install or coerce the operator to mutate listener bind
	// behaviour — both unsafe. Operators configure PublicIPv4
	// (and optionally PublicIPv6) once at install time via
	// env (ORVIX_DNS_PUBLIC_IPV4) or the config file; the
	// handler validates the value and refuses anything that is
	// loopback, private, link-local, multicast, or unspecified.
	PublicIPv4 string `mapstructure:"public_ipv4"`
	PublicIPv6 string `mapstructure:"public_ipv6"`

	// Cloudflare
	CloudflareAPIKey string `mapstructure:"cloudflare_api_key"`
	CloudflareZoneID string `mapstructure:"cloudflare_zone_id"`

	// Namecheap
	NamecheapAPIUser      string `mapstructure:"namecheap_api_user"`
	NamecheapAPIKey       string `mapstructure:"namecheap_api_key"`
	NamecheapUsername     string `mapstructure:"namecheap_username"`
	NamecheapClientIP     string `mapstructure:"namecheap_client_ip"`
	NamecheapSandbox      bool   `mapstructure:"namecheap_sandbox"`
	// NamecheapEnableApply is the kill switch for live Namecheap
	// writes. The provider stays in dry-run mode until an operator
	// explicitly flips this on. The value is read from
	// dns.namecheap_enable_apply (YAML) or ORVIX_DNS_NAMECHEAP_ENABLE_APPLY
	// (env). Default false. Even with credentials present, the
	// provider's Apply() refuses when this is false. The UI surfaces
	// the resulting state as "dry_run_only" so the operator can
	// see why the Apply button is disabled.
	NamecheapEnableApply  bool   `mapstructure:"namecheap_enable_apply"`

	// AWS Route 53 (legacy stub; not used by the new DNS Ops build)
	Route53AccessKey string `mapstructure:"route53_access_key"`
	Route53SecretKey string `mapstructure:"route53_secret_key"`

	// DefaultProvider is the provider name the dashboard should
	// preselect in the provider dropdown (manual / cloudflare /
	// namecheap). Defaults to "manual" when unset.
	DefaultProvider string `mapstructure:"default_provider"`
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
			PasswordMinLen: DefaultPasswordMinLen,
			Argon2Time:     3,
			Argon2Memory:   64 * 1024,
			Argon2Threads:  4,
			LoginRateLimit: 5,
			RateWindow:     15 * time.Minute,
			// CookieDomain is intentionally empty by default.
			// The installer writes the parent domain (with
			// leading dot) for production deployments so the
			// access_token cookie is shared between
			// admin.<parent> and webmail.<parent>.
			CookieDomain: "",
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
			Dir:            "/var/backups/orvix/",
			RetentionCount: 10,
		},
		Outbound: OutboundConfig{
			PreferIPv4: false,
		},
		CoreMail: CoreMailConfig{
			Enabled:           false,
			Hostname:          "mail.local",
			MailStorePath:     "/var/lib/orvix/mailstore",
			SMTPHost:          "0.0.0.0",
			SMTPPort:          25,
			SubmissionEnabled: false,
			SubmissionHost:    "0.0.0.0",
			SubmissionPort:    587,
			SMTPsEnabled:      false,
			SMTPsHost:         "0.0.0.0",
			SMTPsPort:         465,
			IMAPHost:          "0.0.0.0",
			IMAPPort:          143,
			POP3Host:          "0.0.0.0",
			POP3Port:          110,
			RequireTLSForAuth: true,
			RequireAuthForSubmission: false,
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
	if v.GetString("DNS_PUBLIC_IPV4") != "" {
		cfg.DNS.PublicIPv4 = v.GetString("DNS_PUBLIC_IPV4")
	}
	if v.GetString("DNS_PUBLIC_IPV6") != "" {
		cfg.DNS.PublicIPv6 = v.GetString("DNS_PUBLIC_IPV6")
	}
	// Namecheap env vars: support BOTH the documented nested form
	// (ORVIX_DNS_NAMECHEAP_* → viper key "DNS_NAMECHEAP_*") and
	// the flat alias form (ORVIX_NAMECHEAP_* → viper key
	// "NAMECHEAP_*"). The nested form is the canonical documented
	// env name; the flat alias is a convenience. The default is
	// false for bools and empty for strings.
	if s := v.GetString("DNS_NAMECHEAP_API_USER"); s != "" {
		cfg.DNS.NamecheapAPIUser = s
	} else if s := v.GetString("NAMECHEAP_API_USER"); s != "" {
		cfg.DNS.NamecheapAPIUser = s
	}
	if s := v.GetString("DNS_NAMECHEAP_API_KEY"); s != "" {
		cfg.DNS.NamecheapAPIKey = s
	} else if s := v.GetString("NAMECHEAP_API_KEY"); s != "" {
		cfg.DNS.NamecheapAPIKey = s
	}
	if s := v.GetString("DNS_NAMECHEAP_USERNAME"); s != "" {
		cfg.DNS.NamecheapUsername = s
	} else if s := v.GetString("NAMECHEAP_USERNAME"); s != "" {
		cfg.DNS.NamecheapUsername = s
	}
	if s := v.GetString("DNS_NAMECHEAP_CLIENT_IP"); s != "" {
		cfg.DNS.NamecheapClientIP = s
	} else if s := v.GetString("NAMECHEAP_CLIENT_IP"); s != "" {
		cfg.DNS.NamecheapClientIP = s
	}
	if v.GetString("DNS_NAMECHEAP_SANDBOX") != "" {
		cfg.DNS.NamecheapSandbox = v.GetBool("DNS_NAMECHEAP_SANDBOX")
	} else if v.GetString("NAMECHEAP_SANDBOX") != "" {
		cfg.DNS.NamecheapSandbox = v.GetBool("NAMECHEAP_SANDBOX")
	}
	if v.GetString("DNS_NAMECHEAP_ENABLE_APPLY") != "" {
		cfg.DNS.NamecheapEnableApply = v.GetBool("DNS_NAMECHEAP_ENABLE_APPLY")
	} else if v.GetString("NAMECHEAP_ENABLE_APPLY") != "" {
		cfg.DNS.NamecheapEnableApply = v.GetBool("NAMECHEAP_ENABLE_APPLY")
	}
	if v.GetString("COREMAIL_ENABLED") != "" {
		cfg.CoreMail.Enabled = v.GetBool("COREMAIL_ENABLED")
	}
	// SUBMISSION-3D: env overrides for port 587 submission + SMTP TLS
	// binding. These let the installer / setup-smtp-tls.sh turn on
	// submission by exporting the cert/key paths and the enable
	// flag, without rewriting /etc/orvix/orvix.yaml by hand. Empty
	// env vars leave the YAML/default values untouched.
	if v.GetString("COREMAIL_SUBMISSION_ENABLED") != "" {
		cfg.CoreMail.SubmissionEnabled = v.GetBool("COREMAIL_SUBMISSION_ENABLED")
	}
	if s := v.GetString("COREMAIL_TLS_CERT_FILE"); s != "" {
		cfg.CoreMail.TLSCertFile = s
	}
	if s := v.GetString("COREMAIL_TLS_KEY_FILE"); s != "" {
		cfg.CoreMail.TLSKeyFile = s
	}
	// SMTPS is still disabled-by-default and not implemented; this
	// override exists so the operator can keep it pinned-off via env
	// if a misconfigured YAML ever flips it on.
	if v.GetString("COREMAIL_SMTPS_ENABLED") != "" {
		cfg.CoreMail.SMTPsEnabled = v.GetBool("COREMAIL_SMTPS_ENABLED")
	}
	if v.GetString("BACKUP_RETENTION_COUNT") != "" {
		cfg.Backup.RetentionCount = v.GetInt("BACKUP_RETENTION_COUNT")
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
