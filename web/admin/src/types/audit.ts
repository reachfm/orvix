export interface AuditLog {
  id: number;
  action: string;
  actor: string;
  actor_role?: string;
  target: string;
  result: "success" | "failure" | "denied";
  tenant_id?: number;
  tenant_name?: string;
  ip_address?: string;
  role?: string;
  details?: string;
  timestamp: string;
}

export interface ListAuditLogsResponse {
  logs: AuditLog[];
  total: number;
  page: number;
  page_size: number;
}
