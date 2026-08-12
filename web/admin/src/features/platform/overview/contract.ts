// Exact contract for GET /platform/dashboard (platformMW-gated,
// internal/api/handlers/dashboard_admin.go's PlatformDashboard), matching
// internal/admin/dashboard/types.go's PlatformDashboard struct
// field-for-field. There is no queue/storage/alerts data on this
// endpoint — the previous PlatformHome.tsx assumed a nested
// {tenants:{total,active}, mailboxes:{total}, domains:{total},
// queue:{pending,failed}} shape that has never existed on the wire,
// so every one of its stat cards silently rendered "—"/0 placeholders
// forever. Only the fields the backend actually returns are modeled
// here; queue/storage/alerts are MISSING_BACKEND_CAPABILITY for this
// endpoint (see the capability matrix) and are not fabricated.

export interface RecentAuditEntry {
  action: string;
  target: string;
  timestamp: string;
}

export interface PlatformDashboard {
  total_organizations: number;
  active_organizations: number;
  total_domains: number;
  total_mailboxes: number;
  quota_used_bytes: number;
  recent_audit_entries: RecentAuditEntry[] | null;
}
