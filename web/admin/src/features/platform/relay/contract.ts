// Exact contracts for the Platform relay administration routes
// (internal/api/router.go: /platform/relays*).
//
// Wire shapes match the Go structs field-for-field:
//   - RedactedProvider: internal/platform/relay/secret.go (Provider +
//     has_credential; SecretRef is `json:"-"` and never serialized)
//   - HealthCheckResult: internal/platform/relay/dial.go
//
// Mutations require an Idempotency-Key header (platformIdempotent in
// platform_relay_admin.go); destructive/credential actions require the
// X-Confirm typed-confirmation header. Credentials are never returned
// by any read; rotate returns a generated credential exactly once.

export type RelayScope = "global" | "tenant" | "domain";
export type ConnSecurity = "none" | "starttls" | "implicit_tls";
export type TLSValidation = "strict" | "opportunistic";
export type CircuitState = "closed" | "open" | "half_open";

export interface PlatformRelay {
  id: number;
  scope: RelayScope;
  tenant_id?: number;
  domain_id?: number;
  pool_id: number;
  name: string;
  host: string;
  port: number;
  username?: string;
  conn_security: ConnSecurity;
  tls_validation: TLSValidation;
  priority: number;
  weight: number;
  active: boolean;
  rate_limit_per_min?: number;
  circuit_state: CircuitState;
  circuit_failures: number;
  circuit_opened_at?: string;
  last_test_at?: string;
  last_test_result?: string;
  version: number;
  created_at: string;
  updated_at: string;
  /** True when a credential is configured — the only secret signal exposed. */
  has_credential: boolean;
}

export interface ListPlatformRelaysResponse {
  relays: PlatformRelay[];
  total: number;
  limit: number;
  offset: number;
}

export interface PlatformRelayFilter {
  scope?: RelayScope;
  q?: string;
  tenant_id?: number;
  domain_id?: number;
  active?: boolean;
  limit?: number;
  offset?: number;
}

export interface CreatePlatformRelayRequest {
  scope: RelayScope;
  tenant_id?: number;
  domain_id?: number;
  pool_id?: number;
  name: string;
  host: string;
  port: number;
  username?: string;
  password?: string;
  conn_security?: ConnSecurity;
  tls_validation?: TLSValidation;
  priority?: number;
  weight?: number;
  active?: boolean;
  rate_limit_per_min?: number;
}

/** Guarded update: `version` must match the current row version. */
export interface UpdatePlatformRelayRequest {
  version: number;
  scope?: RelayScope;
  tenant_id?: number;
  domain_id?: number;
  pool_id?: number;
  name?: string;
  host?: string;
  port?: number;
  username?: string;
  /** non-nil => re-encrypt and store; empty string clears the credential */
  password?: string;
  conn_security?: ConnSecurity;
  tls_validation?: TLSValidation;
  priority?: number;
  weight?: number;
  active?: boolean;
  rate_limit_per_min?: number;
}

export interface RelayActiveRequest {
  version: number;
}

export interface RotateRelayCredentialsRequest {
  version: number;
  new_password?: string;
}

export interface RotateRelayCredentialsResponse {
  relay: PlatformRelay;
  generated_password?: string;
  show_once?: boolean;
}

/** Safe connectivity-test result — never contains credentials or raw network errors. */
export interface RelayHealthCheckResult {
  connected: boolean;
  tls_negotiated: boolean;
  auth_ok: boolean;
  error?: string;
  duration_ms: number;
}

export interface DeleteRelayResponse {
  status: "ok";
  id: number;
}

/** Typed confirmation phrases (handler-enforced via X-Confirm). */
export function relayDisableConfirmation(id: number): string {
  return `DISABLE-RELAY-${id}`;
}

export function relayRotateConfirmation(id: number): string {
  return `ROTATE-RELAY-${id}`;
}

export function relayDeleteConfirmation(id: number): string {
  return `DELETE-RELAY-${id}`;
}

/** Safe last-test-result vocabulary persisted by the backend. */
export const LAST_TEST_RESULTS: ReadonlyArray<string> = [
  "ok",
  "connect_failed",
  "tls_failed",
  "auth_failed",
  "failed",
];

export function lastTestResultLabel(result: string | undefined): string {
  switch (result) {
    case "ok":
      return "OK";
    case "connect_failed":
      return "Connect failed";
    case "tls_failed":
      return "TLS failed";
    case "auth_failed":
      return "Auth failed";
    case "failed":
      return "Failed";
    default:
      return result || "Never tested";
  }
}
