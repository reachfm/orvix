// Package security implements the normalized security-event core of
// Feature 15 (Milestone 12). It does NOT reimplement antivirus
// scanning (internal/antivirus.Engine, already wired into the real
// SMTP receive path), spam scoring (internal/coremail/antispam.Engine,
// already wired), quarantine (coremail_quarantine_index +
// internal/api/handlers/enterprise_admin.go's ListQuarantine/
// ResolveQuarantine, already atomic/tenant-scoped/audited), or
// allow/block lists (coremail_acl_rules, already scoped/prioritized).
// This package is the normalization layer those systems (and future
// ones) record into, so security reporting/retention/export has one
// event taxonomy instead of N system-specific tables.
package security

import "time"

// Category is the normalized security-event taxonomy required by
// Feature 15.
type Category string

const (
	CategoryMalware         Category = "malware"
	CategorySpam            Category = "spam"
	CategoryPhishing        Category = "phishing"
	CategorySpoofing        Category = "spoofing"
	CategoryAuthAbuse       Category = "auth_abuse"
	CategoryRelayAbuse      Category = "relay_abuse"
	CategoryBruteForce      Category = "brute_force"
	CategorySuspiciousAPI   Category = "suspicious_api"
	CategoryPolicyViolation Category = "policy_violation"
)

func (c Category) IsValid() bool {
	switch c {
	case CategoryMalware, CategorySpam, CategoryPhishing, CategorySpoofing, CategoryAuthAbuse,
		CategoryRelayAbuse, CategoryBruteForce, CategorySuspiciousAPI, CategoryPolicyViolation:
		return true
	default:
		return false
	}
}

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Event is one normalized security occurrence. Deliberately narrow:
// no message body, no raw headers, no credential material — Detail is
// a short, pre-redacted operator-facing summary, never a dump of
// whatever triggered the event. SourceSystem records which existing
// subsystem raised it (e.g. "antivirus", "antispam", "acl",
// "auth") for traceability without this package needing to know that
// subsystem's internals.
type Event struct {
	ID           uint      `json:"id"`
	TenantID     uint      `json:"tenant_id"`
	Category     Category  `json:"category"`
	Severity     Severity  `json:"severity"`
	SourceSystem string    `json:"source_system"`
	Actor        string    `json:"actor,omitempty"` // IP, account, or API key ID — never a password/token
	Detail       string    `json:"detail"`
	CreatedAt    time.Time `json:"created_at"`
}
