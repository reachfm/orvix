package runtimecontrol

import (
	"fmt"
	"log"
	"time"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/observability"
)

// ConfigProvider allows reading and reloading the application config.
type ConfigProvider interface {
	GetConfig() *config.Config
	ReloadConfig() error
}

// RuntimeControl provides a centralized view of runtime state and settings.
type RuntimeControl struct {
	obs    *observability.Observability
	cfg    ConfigProvider
	cfgRef *config.Config
}

func NewRuntimeControl(obs *observability.Observability, cfg ConfigProvider) *RuntimeControl {
	rc := &RuntimeControl{
		obs:    obs,
		cfg:    cfg,
		cfgRef: cfg.GetConfig(),
	}
	return rc
}

func (rc *RuntimeControl) refreshConfig() {
	rc.cfgRef = rc.cfg.GetConfig()
}

// ── Runtime Snapshot ────────────────────────────────────────

func (rc *RuntimeControl) Snapshot() *RuntimeSnapshot {
	snap := &RuntimeSnapshot{}

	healthNames := []string{"smtp_receive", "imap", "pop3", "jmap", "queue", "mailstore", "database", "trust", "policy"}
	serviceNames := []string{"SMTP", "IMAP", "POP3", "JMAP", "Queue", "MailStore", "Database", "Trust", "Policy"}

	for i, name := range healthNames {
		status := rc.serviceHealth(name)
		snap.Services = append(snap.Services, ServiceStatus{
			Name:    serviceNames[i],
			Enabled: true,
			Running: status == "ready" || status == "degraded",
			Healthy: status,
		})
	}

	cfg := rc.cfgRef
	if cfg != nil {
		cm := cfg.CoreMail
		snap.Listeners = []ListenerInfo{
			{Protocol: "SMTP", Host: cm.SMTPHost, Port: cm.SMTPPort, Address: joinHostPort(cm.SMTPHost, cm.SMTPPort), TLS: cm.TLSCertFile != ""},
			{Protocol: "IMAP", Host: cm.IMAPHost, Port: cm.IMAPPort, Address: joinHostPort(cm.IMAPHost, cm.IMAPPort), TLS: cm.TLSCertFile != ""},
			{Protocol: "POP3", Host: cm.POP3Host, Port: cm.POP3Port, Address: joinHostPort(cm.POP3Host, cm.POP3Port), TLS: cm.TLSCertFile != ""},
			{Protocol: "JMAP", Host: cm.JMAPHost, Port: cm.JMAPPort, Address: joinHostPort(cm.JMAPHost, cm.JMAPPort), TLS: cm.TLSCertFile != ""},
		}
	}

	return snap
}

func (rc *RuntimeControl) serviceHealth(name string) string {
	if rc.obs == nil || rc.obs.Health == nil {
		return "unknown"
	}
	report := rc.obs.Health.Report()
	if report == nil || report.Checks == nil {
		return "unknown"
	}
	if check, ok := report.Checks[name]; ok {
		return check.Status.String()
	}
	return "unknown"
}

func joinHostPort(host string, port int) string {
	if host == "" { host = "0.0.0.0" }
	if port <= 0 { return host + ":0" }
	return fmt.Sprintf("%s:%d", host, port)
}

// ── Settings ────────────────────────────────────────────────

func (rc *RuntimeControl) GetSettings() *Settings {
	cfg := rc.cfgRef
	s := &Settings{}
	if cfg == nil { return s }

	cm := cfg.CoreMail
	s.SMTP = SMTPSettings{
		Hostname:              cm.Hostname,
		MaxMessageSizeMB:      25,
		MaxRecipients:         100,
		MaxConcurrentSessions: 250,
	}
	s.IMAP = IMAPSettings{Hostname: cm.Hostname, Port: cm.IMAPPort, MaxSessions: 250}
	s.POP3 = POP3Settings{Hostname: cm.Hostname, Port: cm.POP3Port, MaxSessions: 250}
	s.Queue = QueueSettings{WorkerCount: cm.QueueWorkers, WorkerInterval: cm.WorkerInterval.String()}
	s.Trust = TrustSettings{MaxAttempts: 5, LockoutDurationMin: 30}
	s.Policy = PolicySettings{DefaultMode: "allow"}
	return s
}

func (rc *RuntimeControl) UpdateSettings(s *Settings) error {
	if s == nil { return fmt.Errorf("settings cannot be nil") }

	if s.SMTP.MaxMessageSizeMB > 100 { return fmt.Errorf("max_message_size_mb cannot exceed 100") }
	if s.SMTP.MaxRecipients > 1000 { return fmt.Errorf("max_recipients cannot exceed 1000") }
	if s.SMTP.MaxConcurrentSessions > 5000 { return fmt.Errorf("max_concurrent_sessions cannot exceed 5000") }
	if s.SMTP.SpamMode != "" && s.SMTP.SpamMode != "observation" && s.SMTP.SpamMode != "enforcement" && s.SMTP.SpamMode != "suspicious" {
		return fmt.Errorf("invalid spam_mode: %s", s.SMTP.SpamMode)
	}
	if s.IMAP.MaxSessions > 5000 { return fmt.Errorf("imap max_sessions cannot exceed 5000") }
	if s.POP3.MaxSessions > 5000 { return fmt.Errorf("pop3 max_sessions cannot exceed 5000") }
	if s.Queue.WorkerCount > 50 { return fmt.Errorf("worker_count cannot exceed 50") }
	if s.Trust.MaxAttempts > 100 { return fmt.Errorf("max_attempts cannot exceed 100") }
	if s.Trust.LockoutDurationMin > 1440 { return fmt.Errorf("lockout_duration_min cannot exceed 1440") }
	if s.Policy.DefaultMode != "" && s.Policy.DefaultMode != "allow" && s.Policy.DefaultMode != "block" {
		return fmt.Errorf("invalid policy default_mode")
	}

	cfg := rc.cfgRef
	if cfg != nil {
		cm := &cfg.CoreMail
		if s.SMTP.Hostname != "" { cm.Hostname = s.SMTP.Hostname }
		if s.Queue.WorkerCount > 0 { cm.QueueWorkers = s.Queue.WorkerCount }
		if s.Queue.WorkerInterval != "" {
			if d, err := time.ParseDuration(s.Queue.WorkerInterval); err == nil { cm.WorkerInterval = d }
		}
	}
	return nil
}

// ── Reload ──────────────────────────────────────────────────

func (rc *RuntimeControl) Reload() *ReloadResult {
	if rc.cfg == nil {
		return &ReloadResult{Success: false, Message: "no config provider available"}
	}
	if err := rc.cfg.ReloadConfig(); err != nil {
		return &ReloadResult{Success: false, Message: fmt.Sprintf("reload failed: %v", err)}
	}
	rc.refreshConfig()
	log.Printf("[runtime] configuration reloaded successfully")
	return &ReloadResult{Success: true, Message: "configuration reloaded"}
}
