package runtimecontrol

// ServiceStatus represents the health of a single subsystem.
type ServiceStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Healthy string `json:"healthy"` // "ready", "degraded", "not_ready", "unknown"
}

// ListenerInfo describes a bound network listener.
type ListenerInfo struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Address  string `json:"address"`
	TLS      bool   `json:"tls"`
}

// RuntimeSnapshot is a point-in-time view of all runtime state.
type RuntimeSnapshot struct {
	Services  []ServiceStatus `json:"services"`
	Listeners []ListenerInfo  `json:"listeners"`
}

// ── Settings model ──────────────────────────────────────────

// Settings represents all editable runtime settings.
type Settings struct {
	SMTP   SMTPSettings   `json:"smtp"`
	IMAP   IMAPSettings   `json:"imap"`
	POP3   POP3Settings   `json:"pop3"`
	Queue  QueueSettings  `json:"queue"`
	Trust  TrustSettings  `json:"trust"`
	Policy PolicySettings `json:"policy"`
}

type SMTPSettings struct {
	Hostname              string `json:"hostname"`
	MaxMessageSizeMB      int    `json:"max_message_size_mb"`
	MaxRecipients         int    `json:"max_recipients"`
	MaxConcurrentSessions int    `json:"max_concurrent_sessions"`
	SpamMode              string `json:"spam_mode"`
}

type IMAPSettings struct {
	Hostname    string `json:"hostname"`
	Port        int    `json:"port"`
	MaxSessions int    `json:"max_sessions"`
}

type POP3Settings struct {
	Hostname    string `json:"hostname"`
	Port        int    `json:"port"`
	MaxSessions int    `json:"max_sessions"`
}

type QueueSettings struct {
	WorkerCount    int    `json:"worker_count"`
	WorkerInterval string `json:"worker_interval"`
}

type TrustSettings struct {
	MaxAttempts        int `json:"max_attempts"`
	LockoutDurationMin int `json:"lockout_duration_min"`
}

type PolicySettings struct {
	DefaultMode string `json:"default_mode"`
}

// ── Reload ──────────────────────────────────────────────────

type ReloadResult struct {
	Success  bool     `json:"success"`
	Message  string   `json:"message"`
	Warnings []string `json:"warnings,omitempty"`
}
