const BASE = "/api/v1";

let csrfTokenValue = "";
let csrfTokenPromise: Promise<string> | null = null;

/**
 * ApiError carries the stable machine-readable `code` from the typed backend
 * error contract together with a safe human-readable `message`. Components
 * map `code` to user-facing copy and never parse fragile message strings.
 */
export class ApiError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status: number) {
    super(message || code || `Request failed (${status})`);
    this.name = "ApiError";
    this.code = code || "UNKNOWN_ERROR";
    this.status = status;
  }
}

/** Maps a typed error code to user-safe copy. Unknown codes fall back to the server message. */
export function domainErrorMessage(code: string | undefined, fallback: string): string {
  switch (code) {
    case "DOMAIN_NOT_FOUND":
      return "Domain not found or you do not have access to it.";
    case "DOMAIN_DISABLED":
      return "This domain is disabled. Re-enable it before creating mailboxes.";
    case "DOMAIN_SUSPENDED":
      return "This domain is suspended. Restore it before creating mailboxes.";
    case "DOMAIN_LOCKED":
      return "This domain is locked and not available for this action.";
    case "DOMAIN_UNAVAILABLE":
      return "This domain is not available for this action right now.";
    case "DOMAIN_STATUS_INVALID":
      return "That domain status is not supported.";
    case "DOMAIN_NOT_VERIFIED":
      return "This domain is not verified or not eligible yet. Complete domain setup first.";
    case "DOMAIN_ALREADY_EXISTS":
      return "This domain is already configured on your account.";
    case "INVALID_DOMAIN_NAME":
      return "That domain name is not valid. Use a bare domain like example.com.";
    case "DOMAIN_HAS_MAILBOXES":
      return "This domain still has mailboxes. Delete its mailboxes first.";
    case "DOMAIN_HAS_DEPENDENCIES":
      return "This domain has dependencies (aliases, DKIM, routing) and cannot be deleted.";
    case "DOMAIN_LIMIT_REACHED":
      return "You have reached the domain limit for your plan.";
    case "DKIM_ALREADY_CONFIGURED":
      return "DKIM is already configured for this domain.";
    case "DKIM_NOT_CONFIGURED":
      return "Generate DKIM before rotating keys.";
    case "MAILBOX_LIMIT_REACHED":
      return "You have reached the mailbox limit for your plan.";
    case "MAILBOX_ALREADY_EXISTS":
      return "A mailbox with this address already exists.";
    default:
      return fallback;
  }
}

function isMutationMethod(method?: string): boolean {
  if (!method) return false;
  return ["POST", "PUT", "PATCH", "DELETE"].includes(method.toUpperCase());
}

export async function initCSRF(): Promise<void> {
  if (csrfTokenValue) return;
  if (csrfTokenPromise) {
    await csrfTokenPromise;
    return;
  }
  csrfTokenPromise = (async () => {
    const res = await fetch(`${BASE}/csrf-token`, { credentials: "include" });
    if (!res.ok) {
      csrfTokenPromise = null;
      throw new Error(`CSRF token fetch failed: ${res.status}`);
    }
    const data = await res.json();
    csrfTokenValue = data.csrf_token || "";
    return csrfTokenValue;
  })();
  try {
    await csrfTokenPromise;
  } finally {
    csrfTokenPromise = null;
  }
}

export function setCSRFToken(token: string): void {
  csrfTokenValue = token;
}

export function resetCSRFToken(): void {
  csrfTokenValue = "";
  csrfTokenPromise = null;
}

interface RequestOptions extends RequestInit {
  skipCSRF?: boolean;
  _csrfRetried?: boolean;
}

async function request<T>(path: string, options?: RequestOptions): Promise<T> {
  const method = options?.method || "GET";
  const isMutation = isMutationMethod(method);

  if (isMutation && !options?.skipCSRF && !csrfTokenValue) {
    await initCSRF();
  }

  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (options?.headers) {
    const incoming = options.headers as Record<string, string>;
    for (const k of Object.keys(incoming)) {
      headers[k] = incoming[k];
    }
  }

  if (isMutation && !options?.skipCSRF && csrfTokenValue) {
    headers["X-CSRF-Token"] = csrfTokenValue;
  }

  const res = await fetch(`${BASE}${path}`, {
    credentials: "include",
    headers,
    ...options,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    const code = body?.code || "";
    const message = body?.message || body?.error || `${res.status} ${res.statusText}`;
    if (res.status === 403 && isMutation && !options?.skipCSRF) {
      const errMsg = String(message).toLowerCase();
      if (errMsg.includes("csrf") && csrfTokenValue && !options?._csrfRetried) {
        csrfTokenValue = "";
        await initCSRF();
        return request<T>(path, { ...options, _csrfRetried: true });
      }
      throw new ApiError(code || "FORBIDDEN", message, res.status);
    }
    throw new ApiError(code, message, res.status);
  }

  if (res.status === 204) {
    return undefined as unknown as T;
  }

  return res.json();
}

export const api = {
  // Billing
  getPlans: () => request<any[]>("/billing/plans"),
  getSubscription: () => request<any>("/enterprise/billing/subscription"),
  createSubscription: (data: any) =>
    request("/enterprise/billing/subscription", { method: "POST", body: JSON.stringify(data) }),
  getUsage: () => request<any>("/enterprise/billing/usage"),
  checkQuota: (resource: string, used: number) =>
    request<any>(`/enterprise/billing/quota?resource=${resource}&used=${used}`),

  // Customer domains
  listDomains: () => request<any>("/customer/domains"),
  getDomain: (id: number) => request<any>(`/customer/domains/${id}`),
  getDomainDNS: (id: number) => request<any>(`/customer/domains/${id}/dns`),
  verifyDomain: (id: number) =>
    request<any>(`/customer/domains/${id}/verify`, { method: "POST" }),

  // Enterprise (tenant-scoped)
  getOrganization: (id: number) => request<any>(`/enterprise/organizations/${id}`),
  listDomainsEnterprise: () => request<any>("/enterprise/domains"),
  createDomainEnterprise: (data: any) =>
    request("/enterprise/domains", { method: "POST", body: JSON.stringify(data) }),
  updateDomainEnterprise: (id: number, data: any) =>
    request(`/enterprise/domains/${id}`, { method: "PATCH", body: JSON.stringify(data) }),
  setDomainEnterpriseStatus: (id: number, status: string, reason?: string) =>
    request(`/enterprise/domains/${id}/status`, { method: "POST", body: JSON.stringify({ status, reason: reason || "" }) }),
  deleteDomainEnterprise: (id: number) =>
    request(`/enterprise/domains/${id}`, { method: "DELETE" }),
  getDomainDKIM: (id: number) =>
    request<any>(`/enterprise/domains/${id}/dkim`),
  generateDomainDKIM: (id: number, selector?: string) =>
    request(`/enterprise/domains/${id}/dkim/generate`, { method: "POST", body: JSON.stringify({ selector: selector || "mail" }) }),
  rotateDomainDKIM: (id: number, selector?: string) =>
    request(`/enterprise/domains/${id}/dkim/rotate`, { method: "POST", body: JSON.stringify({ selector: selector || "mail" }) }),
  verifyDomainEnterprise: (id: number) =>
    request(`/enterprise/domains/${id}/verify`, { method: "POST" }),
  getEnterpriseDomainDNS: (id: number) =>
    request<any>(`/enterprise/domains/${id}/dns`),
  verifyEnterpriseDomainDNS: (id: number) =>
    request<any>(`/enterprise/domains/${id}/dns/verify`, { method: "POST" }),
  listMailboxes: () => request<any>("/enterprise/mailboxes"),
  createMailbox: (data: any) =>
    request("/enterprise/mailboxes", { method: "POST", body: JSON.stringify(data) }),
  deleteMailbox: (id: number) =>
    request(`/enterprise/mailboxes/${id}`, { method: "DELETE" }),
  setMailboxStatus: (id: number, status: string, reason?: string) =>
    request(`/enterprise/mailboxes/${id}/status`, { method: "POST", body: JSON.stringify({ status, reason: reason || "" }) }),

  // Abuse
  listAbuseSignals: () => request<any[]>("/enterprise/abuse/signals"),
  acknowledgeSignal: (id: number) =>
    request(`/enterprise/abuse/signals/${id}/acknowledge`, { method: "POST" }),
  resolveSignal: (id: number) =>
    request(`/enterprise/abuse/signals/${id}/resolve`, { method: "POST" }),
  checkSendLimit: () => request<any>("/enterprise/abuse/send-limit"),

  // Auth helpers
  login: (email: string, password: string) =>
    request<any>("/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),
  refresh: () => request<any>("/auth/refresh", { method: "POST" }),
  logout: () => request<any>("/auth/logout", { method: "POST" }),
  logoutAll: () => request<any>("/auth/logout-all", { method: "POST" }),

  // Current user
  getMe: () => request<any>("/me"),

  // Organization
  getCurrentOrganization: () => request<any>("/enterprise/organizations/current"),

  // Invitations
  listInvitations: () => request<any[]>("/enterprise/invitations"),
  createInvitation: (data: any) =>
    request("/enterprise/invitations", { method: "POST", body: JSON.stringify(data) }),
  revokeInvitation: (id: number) =>
    request(`/enterprise/invitations/${id}/revoke`, { method: "POST" }),

  // Members
  listMembers: () => request<any[]>("/enterprise/members"),
  updateMemberRole: (userId: number, role: string) =>
    request(`/enterprise/members/${userId}/role`, { method: "PATCH", body: JSON.stringify({ role }) }),
  removeMember: (userId: number) =>
    request(`/enterprise/members/${userId}`, { method: "DELETE" }),

  // Ownership transfer
  requestOwnershipTransfer: (email: string) =>
    request("/enterprise/ownership/request", { method: "POST", body: JSON.stringify({ email }) }),
  acceptOwnershipTransfer: (token: string) =>
    request(`/enterprise/ownership/accept`, { method: "POST", body: JSON.stringify({ token }) }),
  cancelOwnershipTransfer: () =>
    request("/enterprise/ownership/cancel", { method: "POST" }),

  // Aliases
  listAliases: () => request<any[]>("/enterprise/aliases"),
  createAlias: (data: any) =>
    request("/enterprise/aliases", { method: "POST", body: JSON.stringify(data) }),
  deleteAlias: (id: number) => request(`/enterprise/aliases/${id}`, { method: "DELETE" }),

  // Groups
  listGroups: () => request<any[]>("/enterprise/groups"),
  createGroup: (data: any) =>
    request("/enterprise/groups", { method: "POST", body: JSON.stringify(data) }),
  deleteGroup: (id: number) => request(`/enterprise/groups/${id}`, { method: "DELETE" }),
  addGroupMember: (groupId: number, email: string) =>
    request(`/enterprise/groups/${groupId}/members`, { method: "POST", body: JSON.stringify({ email }) }),
  removeGroupMember: (groupId: number, memberId: number) =>
    request(`/enterprise/groups/${groupId}/members/${memberId}`, { method: "DELETE" }),

  // Account settings
  getProfile: () => request<any>("/account/profile"),
  updateProfile: (data: any) =>
    request("/account/profile", { method: "PATCH", body: JSON.stringify(data) }),
  submitSupportRequest: (data: { category: string; subject: string; message: string }) =>
    request<any>("/account/support-requests", { method: "POST", body: JSON.stringify(data) }),
  changePassword: (data: any) =>
    request("/auth/change-password", { method: "POST", body: JSON.stringify(data) }),

  // Signup
  signup: (data: any) =>
    request("/auth/signup", { method: "POST", body: JSON.stringify(data) }),

  // Dashboard
  getDashboard: () => request<any>("/enterprise/dashboard"),
  getPlatformDashboard: () => request<any>("/platform/dashboard"),

  // Platform admin summary/users/firewall/modules (superadmin/admin scope,
  // distinct from the tenant-scoped /enterprise/* endpoints above)
  getAdminSummary: () => request<any>("/admin/summary"),
  listPlatformUsers: () => request<any[]>("/users"),
  deleteUser: (userId: number) => request(`/users/${userId}`, { method: "DELETE" }),
  updateUserStatus: (userId: number, status: "active" | "suspended") =>
    request(`/users/${userId}/status`, { method: "PATCH", body: JSON.stringify({ status }) }),
  listFirewallRules: () => request<any[]>("/firewall/rules"),
  listFirewallLogs: () => request<any[]>("/firewall/logs"),
  listModules: () => request<any[]>("/modules"),

  // Admin mailboxes (cross-tenant for superadmins, uses /mailboxes admin endpoint)
  listAdminMailboxes: (q?: string, status?: string) => {
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    if (status) params.set("status", status);
    const qs = params.toString();
    return request<any>(`/mailboxes${qs ? "?" + qs : ""}`);
  },
  adminCreateMailbox: (data: { email: string; password: string; quota_mb?: number }) =>
    request<any>("/mailboxes", { method: "POST", body: JSON.stringify(data) }),
  adminUpdateMailboxStatus: (id: number, status: "active" | "suspended") =>
    request<any>(`/mailboxes/${id}/status`, { method: "PATCH", body: JSON.stringify({ status }) }),
  adminDeleteMailbox: (id: number) => request(`/mailboxes/${id}`, { method: "DELETE" }),

  // Admin domains (cross-tenant for superadmins, uses /domains admin endpoint)
  listAdminDomains: (q?: string) => {
    const params = new URLSearchParams();
    if (q) params.set("q", q);
    const qs = params.toString();
    return request<any[]>(`/domains${qs ? "?" + qs : ""}`);
  },

  // Platform organizations
  listPlatformOrganizations: (search?: string, limit?: number, offset?: number) => {
    const params = new URLSearchParams();
    if (search) params.set("search", search);
    if (limit !== undefined) params.set("limit", String(limit));
    if (offset !== undefined) params.set("offset", String(offset));
    const qs = params.toString();
    return request<any>(`/platform/organizations${qs ? "?" + qs : ""}`);
  },
  createPlatformOrganization: (data: { name: string; slug: string; domain: string; plan?: string }) =>
    request<any>("/platform/organizations", { method: "POST", body: JSON.stringify(data) }),
  setPlatformOrganizationActive: (id: number, active: boolean, reason?: string) =>
    request<any>(`/platform/organizations/${id}/active`, { method: "POST", body: JSON.stringify({ active, reason: reason || "" }) }),

  // Invoices
  listInvoices: () => request<any[]>("/enterprise/billing/invoices"),
  getInvoice: (id: number) => request<any>(`/enterprise/billing/invoices/${id}`),

  // Audit logs
  listAuditLogs: () => request<any[]>("/enterprise/audit/logs"),

  // Sessions
  listSessions: () => request<any>("/account/sessions"),
  revokeSession: (id: string) =>
    request(`/account/sessions/${id}/revoke`, { method: "POST" }),

  // MFA
  getMFAStatus: () => request<any>("/account/mfa/status"),
  setupMFABegin: (data: { current_password: string }) =>
    request("/account/mfa/setup", { method: "POST", body: JSON.stringify(data) }),
  setupMFAVerify: (code: string) =>
    request("/account/mfa/verify", { method: "POST", body: JSON.stringify({ code }) }),
  disableMFA: (data: { current_password: string; code?: string; recovery_code?: string }) =>
    request("/account/mfa/disable", { method: "POST", body: JSON.stringify(data) }),
  regenerateRecoveryCodes: (data: { current_password: string; code?: string }) =>
    request("/account/mfa/recovery-codes/regenerate", { method: "POST", body: JSON.stringify(data) }),

  // Forgot/reset password
  forgotPassword: (email: string) =>
    request("/auth/forgot-password", { method: "POST", body: JSON.stringify({ email }) }),
  resetPassword: (token: string, password: string) =>
    request("/auth/reset-password", { method: "POST", body: JSON.stringify({ token, password }) }),
};
