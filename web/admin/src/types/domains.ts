export interface Domain {
  id: number;
  domain: string;
  name?: string;
  plan?: string;
  tenant_id: number;
  tenant_name?: string;
  status: "active" | "disabled" | "pending" | "suspended";
  verified?: boolean;
  mailbox_count?: number;
  mx_status: "ok" | "error" | "pending" | "unknown";
  spf_status: "ok" | "error" | "pending" | "unknown";
  dkim_status: "ok" | "error" | "pending" | "unknown";
  dmarc_status: "ok" | "error" | "pending" | "unknown";
  created_at: string;
  updated_at: string;
}

export interface DnsStatus {
  mx: { status: "ok" | "error" | "pending"; value?: string; error?: string };
  spf: { status: "ok" | "error" | "pending"; value?: string; error?: string };
  dkim: { status: "ok" | "error" | "pending"; value?: string; error?: string };
  dmarc: { status: "ok" | "error" | "pending"; value?: string; error?: string };
  checked_at: string;
}

export interface ListDomainsResponse {
  domains: Domain[];
  total: number;
  page: number;
  page_size: number;
}
