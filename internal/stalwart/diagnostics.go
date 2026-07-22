package stalwart

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"github.com/orvixemail/orvix/internal/config"
	"go.uber.org/zap"
)

type DiagnosticBundle struct {
	SystemInfo    map[string]string `json:"system_info"`
	ConfigSummary map[string]string `json:"config_summary"`
	VersionInfo   map[string]string `json:"version_info"`
	HealthStatus  map[string]string `json:"health_status"`
}

type DiagnosticsService struct {
	cfg    config.StalwartConfig
	logger *zap.SugaredLogger
}

func NewDiagnosticsService(cfg config.StalwartConfig, logger *zap.SugaredLogger) *DiagnosticsService {
	return &DiagnosticsService{cfg: cfg, logger: logger}
}

func (s *DiagnosticsService) Collect() (*DiagnosticBundle, error) {
	bundle := &DiagnosticBundle{
		SystemInfo:    make(map[string]string),
		ConfigSummary: make(map[string]string),
		VersionInfo:   make(map[string]string),
		HealthStatus:  make(map[string]string),
	}

	hostname, _ := os.Hostname()
	bundle.SystemInfo["hostname"] = hostname
	bundle.ConfigSummary["binary_path"] = s.cfg.BinaryPath
	bundle.ConfigSummary["config_path"] = s.cfg.ConfigPath

	if _, err := os.Stat(s.cfg.BinaryPath); err == nil {
		bundle.VersionInfo["stalwart_detected"] = "true"
		cmd := exec.Command(s.cfg.BinaryPath, "--version")
		if output, err := cmd.Output(); err == nil {
			bundle.VersionInfo["stalwart_version"] = string(output)
		}
	} else {
		bundle.VersionInfo["stalwart_detected"] = "false"
	}

	for _, port := range s.cfg.SMTPPorts {
		addr := fmt.Sprintf("localhost:%d", port)
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			bundle.HealthStatus[fmt.Sprintf("smtp_%d", port)] = "up"
		} else {
			bundle.HealthStatus[fmt.Sprintf("smtp_%d", port)] = "down"
		}
	}

	for _, port := range s.cfg.IMAPPorts {
		addr := fmt.Sprintf("localhost:%d", port)
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			conn.Close()
			bundle.HealthStatus[fmt.Sprintf("imap_%d", port)] = "up"
		} else {
			bundle.HealthStatus[fmt.Sprintf("imap_%d", port)] = "down"
		}
	}

	return bundle, nil
}
