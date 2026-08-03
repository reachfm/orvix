/**
 * Wire types for the canonical DNS health contract.
 *
 * These mirror Go's customerdomain.EnterpriseDNSHealth exactly — the shape
 * returned by BOTH `GET /enterprise/domains/:id/dns` and
 * `POST /enterprise/domains/:id/dns/verify` (including the 429 cooldown
 * response, whose body is the last successful snapshot in this same shape).
 * Keep these in sync with internal/customerdomain/types.go.
 */

/** Mirrors MXCheck / SPFCheck / DMARCCheck / MTASTSCheck / TLSRPTCheck / DKIMHealthCheck. */
export interface DNSHealthCheck {
  status: string;
  /** MXCheck.observed is a string[]; the single-value checks use a string. */
  observed?: string | string[];
  expected?: string;
  reason?: string;
  /** Operator-facing repair text, computed by the backend. */
  guidance?: string;
  checked_at?: string;
  // DKIMHealthCheck-only fields.
  selector?: string;
  record_name?: string;
  configured?: boolean;
  public_txt?: string;
  matches_dns?: boolean;
}

/**
 * Mirrors customerdomain.DNSRecordCheck — the generic first-class result for
 * the expanded record inventory (mail-host A/AAAA, per-MX-host resolution,
 * PTR, autodiscover/autoconfig, TLSA).
 */
export interface DNSRecordCheck {
  name: string;
  type: string;
  status: string;
  expected?: string;
  observed?: string[];
  reason?: string;
  guidance?: string;
  optional: boolean;
  checked_at?: string;
}

/**
 * Statuses that mean "this record is not required of this deployment". They
 * never count for or against the health score, and the UI must not render
 * them as failures.
 */
export const NON_REQUIRED_STATUSES = new Set(["optional", "not_applicable"]);

/** Mirrors customerdomain.MTASTSPolicy (snake_case json tags). */
export interface MTASTSPolicy {
  raw?: string;
  valid?: boolean;
  mode?: string;
  max_age?: number;
  mx?: string[];
  error?: string;
}

export interface DomainDNSHealth {
  domain_id: number;
  domain_name: string;
  operational_status: string;
  dns_health: string;
  health_score: number;
  last_checked_at?: string;
  cooldown_until?: string;
  retry_after_seconds?: number;
  /**
   * True only when every record was reconstructed from a real check. The UI
   * must show an explicit incomplete state when this is false, no matter what
   * health_score says.
   */
  complete: boolean;
  mx: DNSHealthCheck | null;
  spf: DNSHealthCheck | null;
  dkim: DNSHealthCheck | null;
  dmarc: DNSHealthCheck | null;
  mtasts: DNSHealthCheck | null;
  tlsrpt: DNSHealthCheck | null;
  mtasts_policy?: MTASTSPolicy | null;

  // ── Expanded record inventory (additive; the six fields above are
  // unchanged for backward compatibility). ──
  mail_host_a?: DNSRecordCheck | null;
  mail_host_aaaa?: DNSRecordCheck | null;
  mx_hosts?: DNSRecordCheck[] | null;
  ptr?: DNSRecordCheck | null;
  autodiscover?: DNSRecordCheck | null;
  autoconfig?: DNSRecordCheck | null;
  tlsa?: DNSRecordCheck | null;
}

/**
 * Mirrors admin/domain.DKIMResult — the ONLY DKIM payload the API returns.
 * It carries the public TXT record and selector; the private key never leaves
 * the server (see internal/admin/domain/service.go: PrivateKeyPEM is written to
 * the repository and read back only to derive the public key, and is not a
 * field of this struct).
 */
export interface DKIMResult {
  selector: string;
  public_dns_txt: string;
  dns_record_name: string;
}

/** One comparison row in the DNS modal table. */
export interface DNSRecordRow {
  key: string;
  name: string;
  type: string;
  required: string;
  observed: string;
  status: string;
  reason: string;
  /** Concrete repair instructions for this exact record. Always present. */
  guidance: string;
  /** True for optional / not_applicable rows, which are not required. */
  optional: boolean;
  priority?: number;
}
