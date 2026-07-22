package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type ServerConfig struct {
	Listen      string `mapstructure:"listen"`
	TrustedCIDR string `mapstructure:"trusted_cidr"`
	Debug       bool   `mapstructure:"debug"`
	ExternalURL string `mapstructure:"external_url"`
	TLSAuto     bool   `mapstructure:"tls_auto"`
	TLSDomain   string `mapstructure:"tls_domain"`
	TLSCertFile string `mapstructure:"tls_cert_file"`
	TLSKeyFile  string `mapstructure:"tls_key_file"`
	TLSDataDir  string `mapstructure:"tls_data_dir"`
}

type DatabaseConfig struct {
	Driver string `mapstructure:"driver"`
	DSN    string `mapstructure:"dsn"`
}

type RedisConfig struct {
	Address  string `mapstructure:"address"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type StalwartConfig struct {
	ConfigPath     string `mapstructure:"config_path"`
	BinaryPath     string `mapstructure:"binary_path"`
	AdminPort      int    `mapstructure:"admin_port"`
	AdminUsername  string `mapstructure:"admin_username"`
	AdminPassword  string `mapstructure:"admin_password"`
	SMTPPorts      []int  `mapstructure:"smtp_ports"`
	IMAPPorts      []int  `mapstructure:"imap_ports"`
	POP3Ports      []int  `mapstructure:"pop3_ports"`
	JMAPPorts      []int  `mapstructure:"jmap_ports"`
	HealthInterval int    `mapstructure:"health_interval"`
}

type LicenseConfig struct {
	PublicKeyPath     string `mapstructure:"public_key_path"`
	EmbeddedPublicKey string `mapstructure:"embedded_public_key"`
	ServerURL         string `mapstructure:"server_url"`
	OfflineGraceDays  int    `mapstructure:"offline_grace_days"`
	HardwareBinding   bool   `mapstructure:"hardware_binding"`
}

type UpdatesConfig struct {
	Channel          string `mapstructure:"channel"`
	UpdateServer     string `mapstructure:"update_server"`
	AutoCheck        bool   `mapstructure:"auto_check"`
	AutoApply        bool   `mapstructure:"auto_apply"`
	RollbackDir      string `mapstructure:"rollback_dir"`
	SnapshotDir      string `mapstructure:"snapshot_dir"`
	GPGPublicKeyPath string `mapstructure:"gpg_public_key_path"`
}

type SecurityConfig struct {
	JWTSecret        string   `mapstructure:"jwt_secret"`
	AccessTokenTTL   int      `mapstructure:"access_token_ttl"`
	RefreshTokenTTL  int      `mapstructure:"refresh_token_ttl"`
	Argon2Time       uint32   `mapstructure:"argon2_time"`
	Argon2Memory     uint32   `mapstructure:"argon2_memory"`
	Argon2Threads    uint8    `mapstructure:"argon2_threads"`
	RateLimitPerIP   int      `mapstructure:"rate_limit_per_ip"`
	RateLimitWindow  int      `mapstructure:"rate_limit_window"`
	CSP              string   `mapstructure:"csp"`
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AdminIPAllowlist []string `mapstructure:"admin_ip_allowlist"`
}

type FeatureFlagsConfig struct {
	EmergencyDisableURL string `mapstructure:"emergency_disable_url"`
	CacheTTL            int    `mapstructure:"cache_ttl"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type GuardianConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Mode          string `mapstructure:"mode"`
	APIKey        string `mapstructure:"api_key"`
	APIEndpoint   string `mapstructure:"api_endpoint"`
	OllamaAddress string `mapstructure:"ollama_address"`
	Model         string `mapstructure:"model"`
}

type ComposeConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	Mode          string `mapstructure:"mode"`
	APIKey        string `mapstructure:"api_key"`
	APIEndpoint   string `mapstructure:"api_endpoint"`
	OllamaAddress string `mapstructure:"ollama_address"`
	Model         string `mapstructure:"model"`
}

type Config struct {
	Server       ServerConfig       `mapstructure:"server"`
	Database     DatabaseConfig     `mapstructure:"database"`
	Redis        RedisConfig        `mapstructure:"redis"`
	Stalwart     StalwartConfig     `mapstructure:"stalwart"`
	License      LicenseConfig      `mapstructure:"license"`
	Updates      UpdatesConfig      `mapstructure:"updates"`
	Security     SecurityConfig     `mapstructure:"security"`
	FeatureFlags FeatureFlagsConfig `mapstructure:"feature_flags"`
	Logging      LoggingConfig      `mapstructure:"logging"`
	Guardian     GuardianConfig     `mapstructure:"guardian"`
	Compose      ComposeConfig      `mapstructure:"compose"`
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	v := viper.New()
	v.SetConfigName("orvix")
	v.SetConfigType("yaml")

	configPaths := []string{
		".",
		"./configs",
		"/etc/orvix",
		filepath.Join(os.Getenv("HOME"), ".orvix"),
		os.Getenv("ORVIX_CONFIG_DIR"),
	}

	for _, p := range configPaths {
		if p != "" {
			v.AddConfigPath(p)
		}
	}

	// Environment variable overrides with ORVIX_ prefix
	v.SetEnvPrefix("ORVIX")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	applyEnvOverrides(&cfg, v)

	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// LoadMinimal loads config without validation (for CLI commands that don't need full server config)
func LoadMinimal() (*Config, error) {
	v := viper.New()
	v.SetConfigName("orvix")
	v.SetConfigType("yaml")

	configPaths := []string{
		".",
		"./configs",
		"/etc/orvix",
		filepath.Join(os.Getenv("HOME"), ".orvix"),
		os.Getenv("ORVIX_CONFIG_DIR"),
	}

	for _, p := range configPaths {
		if p != "" {
			v.AddConfigPath(p)
		}
	}

	v.SetEnvPrefix("ORVIX")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	applyEnvOverrides(&cfg, v)

	// Skip validation for minimal load
	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.listen", ":8080")
	v.SetDefault("server.trusted_cidr", "0.0.0.0/0")
	v.SetDefault("server.debug", false)

	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.dsn", "orvix.db?_journal_mode=WAL&_busy_timeout=5000")

	v.SetDefault("redis.address", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)

	v.SetDefault("stalwart.config_path", "/etc/stalwart/config.yaml")
	v.SetDefault("stalwart.binary_path", "/usr/local/bin/stalwart")
	v.SetDefault("stalwart.admin_port", 8081)
	v.SetDefault("stalwart.smtp_ports", []int{25, 587, 465})
	v.SetDefault("stalwart.imap_ports", []int{143, 993})
	v.SetDefault("stalwart.pop3_ports", []int{110, 995})
	v.SetDefault("stalwart.jmap_ports", []int{80, 443})
	v.SetDefault("stalwart.health_interval", 60)

	v.SetDefault("license.offline_grace_days", 7)
	v.SetDefault("license.hardware_binding", false)

	v.SetDefault("updates.channel", "stable")
	v.SetDefault("updates.update_server", "https://updates.orvix.email")
	v.SetDefault("updates.auto_check", true)
	v.SetDefault("updates.auto_apply", false)
	v.SetDefault("updates.rollback_dir", "/var/lib/orvix/rollback")
	v.SetDefault("updates.snapshot_dir", "/var/lib/orvix/snapshots")

	v.SetDefault("security.access_token_ttl", 15)
	v.SetDefault("security.refresh_token_ttl", 43200)
	v.SetDefault("security.argon2_time", 3)
	v.SetDefault("security.argon2_memory", 65536)
	v.SetDefault("security.argon2_threads", 4)
	v.SetDefault("security.rate_limit_per_ip", 100)
	v.SetDefault("security.rate_limit_window", 60)
	v.SetDefault("security.allowed_origins", []string{"*"})

	v.SetDefault("logging.level", "info")
	v.SetDefault("logging.format", "json")
	v.SetDefault("logging.output", "stdout")

	v.SetDefault("feature_flags.cache_ttl", 300)
}

func applyEnvOverrides(cfg *Config, v *viper.Viper) {
	if v.IsSet("server_listen") {
		cfg.Server.Listen = v.GetString("server_listen")
	}
	if v.IsSet("security_jwt_secret") {
		cfg.Security.JWTSecret = v.GetString("security_jwt_secret")
	}
}

func validateConfig(cfg *Config) error {
	if cfg.Server.Listen == "" {
		return fmt.Errorf("server.listen is required")
	}
	if cfg.Database.Driver != "sqlite" && cfg.Database.Driver != "postgres" {
		return fmt.Errorf("database.driver must be 'sqlite' or 'postgres', got %q", cfg.Database.Driver)
	}
	if cfg.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	validChannels := map[string]bool{"stable": true, "beta": true, "early-access": true, "nightly": true}
	if !validChannels[cfg.Updates.Channel] {
		return fmt.Errorf("updates.channel must be one of: stable, beta, early-access, nightly")
	}
	if cfg.Security.JWTSecret == "" {
		return fmt.Errorf("security.jwt_secret is required: set ORVIX_SECURITY_JWT_SECRET environment variable or add jwt_secret to orvix.yaml")
	}
	if cfg.Security.JWTSecret == "CHANGE_ME_TO_A_SECURE_RANDOM_STRING" {
		return fmt.Errorf("security.jwt_secret must be changed from the default value")
	}
	checkConfigFilePermissions()
	return nil
}

func checkConfigFilePermissions() {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return
	}
	paths := []string{"orvix.yaml", "./configs/orvix.yaml", "/etc/orvix/orvix.yaml"}
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil {
			if info.Mode().Perm()&0077 != 0 {
				fmt.Fprintf(os.Stderr, "WARNING: config file %s has permissions %o (recommended: 0600)\n", p, info.Mode().Perm())
			}
			break
		}
	}
}
