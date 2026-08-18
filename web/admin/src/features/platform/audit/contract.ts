// Exact contracts for the platform audit-log endpoints
// (internal/audit/extended.go ExtendedEntry + internal/api/handlers/audit_export.go).

export interface AuditEntry {
  id: number;
  actor: string;
  actor_id: number;
  actor_role: string;
  tenant_id: number;
  action: string;
  target?: string;
  target_id?: number;
  result: string;
  reason?: string;
  before?: string;
  after?: string;
  request_id?: string;
  ip: string;
  user_agent: string;
  timestamp: string;
}

export interface ListAuditResponse {
  entries: AuditEntry[];
  total: number;
  limit: number;
  offset: number;
}

// GET /audit/logs/export streams a real file (JSON or CSV) built from
// the SAME canonical store as the list — the response is a Blob, not
// an audit-record row, so the client downloads it (blob responseType).
export type AuditExportFormat = "json" | "csv";
