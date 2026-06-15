package adminapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// ── Roles ────────────────────────────────────────────────────

type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
	RoleSupport    Role = "support"
	RoleReadOnly   Role = "read_only"
)

func (r Role) IsValid() bool {
	switch r {
	case RoleSuperAdmin, RoleAdmin, RoleSupport, RoleReadOnly:
		return true
	default:
		return false
	}
}

// ── Permissions ──────────────────────────────────────────────

type Permission string

const (
	PermSystemRead      Permission = "system.read"
	PermHealthRead      Permission = "health.read"
	PermMetricsRead     Permission = "metrics.read"
	PermAuditRead       Permission = "audit.read"
	PermDomainsRead     Permission = "domains.read"
	PermDomainsWrite    Permission = "domains.write"
	PermMailboxesRead   Permission = "mailboxes.read"
	PermMailboxesWrite  Permission = "mailboxes.write"
	PermPoliciesRead    Permission = "policies.read"
	PermPoliciesWrite   Permission = "policies.write"
	PermTrustRead       Permission = "trust.read"
	PermTrustWrite      Permission = "trust.write"
	PermQueueRead       Permission = "queue.read"
	PermQueueWrite      Permission = "queue.write"
	PermSettingsRead    Permission = "settings.read"
	PermSettingsWrite   Permission = "settings.write"
	PermRuntimeRead     Permission = "runtime.read"
	PermRuntimeControl  Permission = "runtime.control"
	PermBackupRead      Permission = "backup.read"
	PermBackupWrite     Permission = "backup.write"
	PermComplianceRead  Permission = "compliance.read"
	PermComplianceWrite Permission = "compliance.write"
	PermMonitoringRead  Permission = "monitoring.read"
	PermMonitoringWrite Permission = "monitoring.write"
	PermLifecycleRead   Permission = "lifecycle.read"
	PermLifecycleWrite  Permission = "lifecycle.write"
	PermMigrationRead   Permission = "migration.read"
	PermMigrationWrite  Permission = "migration.write"
)

var allPermissions = []Permission{
	PermSystemRead, PermHealthRead, PermMetricsRead, PermAuditRead,
	PermDomainsRead, PermDomainsWrite, PermMailboxesRead, PermMailboxesWrite,
	PermPoliciesRead, PermPoliciesWrite, PermTrustRead, PermTrustWrite,
	PermQueueRead, PermQueueWrite, PermSettingsRead, PermSettingsWrite,
	PermRuntimeRead, PermRuntimeControl,
	PermBackupRead, PermBackupWrite,
	PermComplianceRead, PermComplianceWrite,
	PermMonitoringRead, PermMonitoringWrite,
	PermLifecycleRead, PermLifecycleWrite,
	PermMigrationRead, PermMigrationWrite,
}

var readPermissions = []Permission{
	PermSystemRead, PermHealthRead, PermMetricsRead, PermAuditRead,
	PermDomainsRead, PermMailboxesRead,
	PermPoliciesRead, PermTrustRead, PermQueueRead, PermSettingsRead,
	PermRuntimeRead,
	PermBackupRead,
	PermComplianceRead,
	PermMonitoringRead,
	PermLifecycleRead,
	PermMigrationRead,
}

var rolePermissions = map[Role][]Permission{
	RoleSuperAdmin: allPermissions,
	RoleAdmin:      allPermissions,
	RoleSupport:    append(readPermissions, PermMailboxesWrite),
	RoleReadOnly:   readPermissions,
}

func GetPermissions(role Role) []Permission {
	if perms, ok := rolePermissions[role]; ok {
		return perms
	}
	return readPermissions
}

func HasPermission(role Role, perm Permission) bool {
	for _, p := range GetPermissions(role) {
		if p == perm {
			return true
		}
	}
	return false
}

// ── Session ──────────────────────────────────────────────────

type Session struct {
	Token     string    `json:"token"`
	UserID    uint      `json:"userId"`
	Username  string    `json:"username"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (s *Session) Permissions() []Permission {
	return GetPermissions(s.Role)
}

func (s *Session) HasPermission(perm Permission) bool {
	return HasPermission(s.Role, perm)
}

func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

func (ss *SessionStore) Get(token string) *Session {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.sessions[token]
}

func (ss *SessionStore) Set(session *Session) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[session.Token] = session
}

func (ss *SessionStore) Delete(token string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, token)
}

// ── Request / Response Types ─────────────────────────────────

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	UserID      uint         `json:"userId"`
	Username    string       `json:"username"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
}

type SessionResponse struct {
	UserID      uint         `json:"userId"`
	Username    string       `json:"username"`
	Role        Role         `json:"role"`
	Permissions []Permission `json:"permissions"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// ── Audit ────────────────────────────────────────────────────

type AuditAction string

const (
	AuditLoginSuccess            AuditAction = "login_success"
	AuditLoginFailure            AuditAction = "login_failure"
	AuditLogout                  AuditAction = "logout"
	AuditSessionExpired          AuditAction = "session_expired"
	AuditPermissionDenied        AuditAction = "permission_denied"
	AuditHealthViewed            AuditAction = "health_viewed"
	AuditRuntimeViewed           AuditAction = "runtime_viewed"
	AuditRuntimeReload           AuditAction = "runtime_reload"
	AuditSettingsViewed          AuditAction = "settings_viewed"
	AuditSettingsUpdated         AuditAction = "settings_updated"
	AuditDomainViewed            AuditAction = "domain_viewed"
	AuditDomainCreated           AuditAction = "domain_created"
	AuditDomainUpdated           AuditAction = "domain_updated"
	AuditDomainDeleted           AuditAction = "domain_deleted"
	AuditMailboxViewed           AuditAction = "mailbox_viewed"
	AuditMailboxCreated          AuditAction = "mailbox_created"
	AuditMailboxUpdated          AuditAction = "mailbox_updated"
	AuditMailboxPasswordReset    AuditAction = "mailbox_password_reset"
	AuditMailboxSuspended        AuditAction = "mailbox_suspended"
	AuditMailboxActivated        AuditAction = "mailbox_activated"
	AuditMailboxDeleted          AuditAction = "mailbox_deleted"
	AuditQueueViewed             AuditAction = "queue_viewed"
	AuditQueueEntryViewed        AuditAction = "queue_entry_viewed"
	AuditQueueRetry              AuditAction = "queue_retry"
	AuditQueueCancel             AuditAction = "queue_cancel"
	AuditDNSReportViewed         AuditAction = "dns_report_viewed"
	AuditDNSValidationRun        AuditAction = "dns_validation_run"
	AuditMessageTraceViewed      AuditAction = "message_trace_viewed"
	AuditMessageTraceSearch      AuditAction = "message_trace_searched"
	AuditTrustLockoutCleared     AuditAction = "trust_lockout_cleared"
	AuditPolicyCreated           AuditAction = "policy_created"
	AuditPolicyUpdated           AuditAction = "policy_updated"
	AuditPolicyDeleted           AuditAction = "policy_deleted"
	AuditPolicyViewed            AuditAction = "policy_viewed"
	AuditBackupCreated           AuditAction = "backup_created"
	AuditBackupDeleted           AuditAction = "backup_deleted"
	AuditBackupVerified          AuditAction = "backup_verified"
	AuditBackupViewed            AuditAction = "backup_viewed"
	AuditBackupRestored          AuditAction = "backup_restored"
	AuditBackupRestoreFailed     AuditAction = "backup_restore_failed"
	AuditBackupRestoreRejected   AuditAction = "restore_rejected"
	AuditCertificateViewed       AuditAction = "certificate_viewed"
	AuditCertificateValidated    AuditAction = "certificate_validated"
	AuditCertificateReload       AuditAction = "certificate_reload"
	AuditCertificateReloadFailed AuditAction = "certificate_reload_failed"
	AuditMessageQuarantined      AuditAction = "message_quarantined"
	AuditMessageReleased         AuditAction = "message_released"
	AuditMessageDeleted          AuditAction = "message_deleted"
	AuditAbuseViewed             AuditAction = "abuse_viewed"
	AuditAlertViewed             AuditAction = "alert_viewed"
	AuditAlertResolved           AuditAction = "alert_resolved"
	AuditCapacityViewed          AuditAction = "capacity_viewed"
	AuditUpgradeStarted          AuditAction = "upgrade_started"
	AuditUpgradeCompleted        AuditAction = "upgrade_completed"
	AuditUpgradeFailed           AuditAction = "upgrade_failed"
	AuditRollbackStarted         AuditAction = "rollback_started"
	AuditRollbackCompleted       AuditAction = "rollback_completed"
	AuditPreflightExecuted       AuditAction = "preflight_executed"
	AuditMigrationStarted        AuditAction = "migration_started"
	AuditMigrationCompleted      AuditAction = "migration_completed"
	AuditMigrationFailed         AuditAction = "migration_failed"
	AuditMigrationCancelled      AuditAction = "migration_cancelled"
	AuditLicenseLimitWarning     AuditAction = "license_limit_warning"
	AuditLicenseLimitExceeded    AuditAction = "license_limit_exceeded"
	AuditDomainCreateBlocked     AuditAction = "license_domain_create_blocked"
	AuditMailboxCreateBlocked    AuditAction = "license_mailbox_create_blocked"
	AuditLicenseInstalled        AuditAction = "license_installed"
	AuditLicenseInstallFailed    AuditAction = "license_install_failed"
	AuditLicenseValidated        AuditAction = "license_validated"
	AuditLicenseRefreshed        AuditAction = "license_refreshed"
)

type AuditEntry struct {
	Actor     string      `json:"actor"`
	Role      Role        `json:"role"`
	Action    AuditAction `json:"action"`
	IP        string      `json:"ip"`
	UserAgent string      `json:"userAgent"`
	Result    string      `json:"result"`
	Timestamp time.Time   `json:"timestamp"`
}

// ── Helpers ──────────────────────────────────────────────────

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: msg})
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
