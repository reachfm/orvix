export interface Mailbox {
  id: number;
  email: string;
  name: string;
  domain?: string;
  tenant_id: number;
  tenant_name?: string;
  status: "active" | "suspended" | "disabled";
  quota_mb: number;
  used_mb?: number;
  is_admin?: boolean;
  created_at: string;
  updated_at: string;
}

export interface MailboxQuota {
  quota_mb: number;
  used_mb: number;
  used_percent: number;
}

export interface ListMailboxesResponse {
  mailboxes: Mailbox[];
  total: number;
  page: number;
  page_size: number;
}
