export interface Organization {
  id: number;
  slug: string;
  name: string;
  domain: string;
  plan: "free" | "starter" | "pro" | "enterprise";
  status: "active" | "suspended" | "pending";
  max_mailboxes: number;
  max_storage_mb: number;
  active_mailboxes?: number;
  created_at: string;
  updated_at: string;
}

export interface OrgStats {
  mailbox_count: number;
  active_mailbox_count: number;
  domain_count: number;
  storage_used_mb: number;
}

export interface ListOrgsResponse {
  organizations: Organization[];
  total: number;
  page: number;
  page_size: number;
}
