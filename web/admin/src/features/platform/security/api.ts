import { request } from "../../../api";
import type {
  AuditEntry, ListCertificatesResponse, AcmeStatus, ExpiryWarning, AntivirusStatus,
  FirewallRule, FirewallLog, GuardianLog, HealHistoryEntry, RunHealCheckResponse,
  ListLogRulesResponse, CreateLogRuleRequest, LogRule,
  UploadCertificateRequest, UploadCertificateResponse,
} from "./contract";

// GET /audit/logs returns the canonical {entries, total, limit, offset}
// envelope (same store/contract as the platform audit page). The panel
// only needs the entries array — unwrap here so AuditPanel renders real
// rows instead of an empty list.
export const listAuditLogs = async (): Promise<AuditEntry[]> => {
  const data = await request<{ entries: AuditEntry[]; total: number; limit: number; offset: number }>("/audit/logs");
  return data?.entries ?? [];
};

export const listSslCertificates = () => request<ListCertificatesResponse>("/admin/ssl/certificates");
export const reloadSslCertificates = () => request<{ status?: string }>("/admin/ssl/certificates/reload", { method: "POST" });
export const getAcmeStatus = () => request<AcmeStatus>("/admin/ssl/acme/status");
export const getSslExpiryWarnings = () => request<{ warnings: ExpiryWarning[] }>("/admin/ssl/expiry-warnings");
export const deleteSslCertificate = (id: string) => request<void>(`/admin/ssl/certificates/${id}`, { method: "DELETE" });
// The request body carries the private key (key_pem) — never logged,
// never echoed by this function, and the caller must clear its own
// form state immediately after the request settles either way.
export const uploadSslCertificate = (body: UploadCertificateRequest) =>
  request<UploadCertificateResponse>("/admin/ssl/certificates", { method: "POST", body: JSON.stringify(body) });

export const getAntivirusStatus = () => request<AntivirusStatus>("/admin/security/antivirus");

// POST /firewall/rules is intentionally not called by this client: the
// legacy internal/firewall engine it once fed is not wired into any
// production mail path (CoreMail enforces policy via internal/ruler
// instead), and the handler now fails closed with 410
// FIREWALL_RULE_ENGINE_NOT_OPERATIONAL. See FirewallPanel.tsx.
export const listFirewallRules = () => request<FirewallRule[]>("/firewall/rules");
export const listFirewallLogs = () => request<FirewallLog[]>("/firewall/logs");

export const listGuardianLogs = () => request<GuardianLog[]>("/guardian/logs");

export const listHealHistory = () => request<HealHistoryEntry[]>("/heal/history");
export const runHealCheck = (name: string) => request<RunHealCheckResponse>(`/heal/check/${name}`, { method: "POST" });

export const listLogRules = () => request<ListLogRulesResponse>("/admin/log-rules");
// CreateLogRule (internal/api/handlers/enterprise_admin.go) requires
// "name" and writes to match_pattern/destination/source/severity/
// enabled — the previous LogRulesTab sent only {"pattern": ...}, a
// field that has never existed in this handler's request struct or
// the coremail_log_rules schema, so every create attempt was rejected
// with 400 "name is required".
export const createLogRule = (body: CreateLogRuleRequest) =>
  request<{ id: number }>("/admin/log-rules", { method: "POST", body: JSON.stringify(body) });
export const deleteLogRule = (id: number) => request<void>(`/admin/log-rules/${id}`, { method: "DELETE" });
export type { LogRule };
