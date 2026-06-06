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

func (cg *ConfigGenerator) writeMainConfig() error {
	config := fmt.Sprintf(`---
server:
  host: "0.0.0.0"
  port: 25
  tls:
    enabled: true
    certificate: "%s/certs/fullchain.pem"
    private-key: "%s/certs/privkey.pem"

imap:
  host: "0.0.0.0"
  port: 143
  tls-port: 993

pop3:
  host: "0.0.0.0"
  port: 110
  tls-port: 995

jmap:
  host: "0.0.0.0"
  port: 8800

api:
  host: "127.0.0.1"
  port: 18080
  api-key: "%s"

data:
  directory: "%s"

logging:
  directory: "%s"
  level: "info"

auth:
  mechanism: "internal"

queue:
  type: "rocksdb"
  directory: "%s/queue"
`, cg.configDir, cg.configDir, cg.apiKey, cg.dataDir, cg.logDir, cg.dataDir)

	return os.WriteFile(cg.configDir+"/stalwart.yaml", []byte(config), 0640)
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
