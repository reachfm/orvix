package stalwart

import (
	"fmt"
	"os"

	"go.uber.org/zap"
)

// ConfigGenerator generates Stalwart configuration files.
type ConfigGenerator struct {
	dataDir     string
	configDir   string
	logDir      string
	apiKey      string
	logger      *zap.Logger
}

// NewConfigGenerator creates a new Stalwart config generator.
func NewConfigGenerator(dataDir, configDir, logDir, apiKey string, logger *zap.Logger) *ConfigGenerator {
	return &ConfigGenerator{
		dataDir:   dataDir,
		configDir: configDir,
		logDir:    logDir,
		apiKey:    apiKey,
		logger:    logger,
	}
}

// Generate creates Stalwart configuration files.
func (cg *ConfigGenerator) Generate() error {
	if err := os.MkdirAll(cg.configDir, 0750); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := cg.writeMainConfig(); err != nil {
		return err
	}

	if err := cg.writeWebhooksConfig(); err != nil {
		return err
	}

	cg.logger.Info("stalwart configuration generated", zap.String("config_dir", cg.configDir))
	return nil
}

// RC5 FIX: Stalwart v0.16+ uses JSON config.json, not YAML
// The config.json only specifies the datastore - all other settings are in the database
func (cg *ConfigGenerator) writeMainConfig() error {
	// RC5 FIX: v0.16 JSON format - only datastore path is in config.json
	// Everything else (domains, accounts, SMTP, etc.) is managed via JMAP API
	config := fmt.Sprintf(`{
  "storage": {
    "@type": "RocksDb",
    "path": "%s"
  },
  "server": {
    "hostname": "localhost"
  },
  "tracing": {
    "level": "info"
  }
}`, cg.dataDir)

	// RC5 FIX: Write to config.json (v0.16 format), not stalwart.yaml
	return os.WriteFile(cg.configDir+"/config.json", []byte(config), 0640)
}

func (cg *ConfigGenerator) writeWebhooksConfig() error {
	config := `---
webhooks:
  events:
    - type: "EMAIL_RECEIVED"
      url: "http://localhost:8080/webhooks/email"
    - type: "EMAIL_SENT"
      url: "http://localhost:8080/webhooks/email"
    - type: "BOUNCE_RECEIVED"
      url: "http://localhost:8080/webhooks/bounce"
    - type: "AUTH_FAILURE"
      url: "http://localhost:8080/webhooks/auth"
`

	return os.WriteFile(cg.configDir+"/webhooks.yaml", []byte(config), 0640)
}
