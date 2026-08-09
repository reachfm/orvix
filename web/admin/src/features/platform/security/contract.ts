// Exact contracts for the platform-owned Security endpoints, read from
// the real Go structs/handlers before writing this file:
//   Audit:      internal/api/handlers/handlers.go's ListAuditLogs safeEntry
//   SSL/ACME:   internal/api/handlers/enterprise_admin_ssl.go
//   Antivirus:  internal/api/handlers/enterprise_admin_v3.go's
//               AdminAntivirusStatus (see the standalone Antivirus fix)
//   Firewall:   internal/models.FirewallRule / FirewallLog
//   Guardian:   internal/models.GuardianLog
//   Self-Heal:  internal/models.HealHistory
//   Log Rules:  internal/api/handlers/enterprise_admin.go

export interface AuditEntry {
  id: number;
  action: string;
  actor: string;
  target: string;
  result: string;
  timestamp: string;
}

// --- SSL / ACME ---

export interface CertInfo {
  id: string;
  name: string;
  source: "runtime" | "uploaded";
  path: string;
  key_path?: string;
  common_name: string;
  sans?: string[];
  issuer: string;
  serial_number: string;
  not_before?: string;
  not_after?: string;
  days_remaining: number;
  fingerprint_sha256: string;
  status: string;
}

export interface ListCertificatesResponse {
  runtime: CertInfo[];
  uploaded: CertInfo[];
  expiry_warnings: CertInfo[];
  expiry_cutoff_days: number;
  config_path: string;
  config_key_path: string;
}

// POST /admin/ssl/certificates (AdminSslUploadCertificate). The
// request carries the private key in cert_pem/key_pem — it is never
// echoed back. The response is metadata only (no PEM/key material).
export interface UploadCertificateRequest {
  name: string;
  cert_pem: string;
  key_pem: string;
}

export interface UploadCertificateResponse {
  name: string;
  common_name: string;
  issuer: string;
  not_after: string;
  days_remaining: number;
  status: string;
  fingerprint_sha256: string;
  path: string;
  key_path?: string;
}

export interface AcmeStatus {
  acme_enabled: boolean;
  issuing_certificates: boolean;
  acme_provider: string;
  manual_paths: string[];
  script_helper: string;
  on_disk_candidates: string[];
  honest_notes: string[];
}

export interface ExpiryWarning {
  name: string;
  not_after: string;
  days_remaining: number;
  status: string;
}

// --- Antivirus (see PlatformSecurity.tsx's AntivirusTab for the
// original fix — this is the same contract, now shared). ---

export interface AntivirusStatus {
  engine: string;
  engine_configured: boolean;
  engine_reachable: boolean;
  engine_active: boolean;
  runtime_enforced: boolean;
  clamav_host: string;
  clamav_port: number;
  clamav_response: string;
  policy_on_infected: string;
  policy_on_scanner_unavailable: string;
  last_error: string;
  counts: {
    scanned: number;
    infected: number;
    rejected: number;
    quarantined: number;
    tagged: number;
    fail_open: number;
    fail_closed: number;
  };
  honest_notes: string[];
}

// --- Firewall ---

export interface FirewallRule {
  id: number;
  name: string;
  condition: string;
  action: string;
  priority: number;
  enabled: boolean;
}

export interface FirewallLog {
  id: number;
  ip: string;
  domain: string;
  sender: string;
  recipient: string;
  created_at: string;
}

// --- Guardian ---

export interface GuardianLog {
  id: number;
  message_id: string;
  threat_score: number;
  verdict: string;
  confidence: number;
  reasons: string;
  action: string;
  created_at: string;
}

// --- Self-Heal ---

export interface HealHistoryEntry {
  id: number;
  check_name: string;
  severity: string;
  issue: string;
  fix_applied: string;
  success: boolean;
  created_at: string;
}

export interface RunHealCheckResponse {
  status: string;
  check: string;
}

// --- Log Rules ---

export interface LogRule {
  id: number;
  name: string;
  source: string;
  severity: string;
  match_pattern: string;
  destination: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ListLogRulesResponse {
  rules: LogRule[];
}

export interface CreateLogRuleRequest {
  name: string;
  source?: string;
  severity?: string;
  match_pattern?: string;
  destination?: string;
  enabled?: boolean;
}
