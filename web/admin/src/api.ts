const BASE = "/api/v1";

// REQUEST_TIMEOUT_MS bounds every request this client makes. Without it, a
// backend that accepts the connection but never answers (e.g. a stuck DB
// transaction starving the connection pool — the 2026-08 production
// incident where admin login hung forever on "Signing in...") leaves the
// UI in a silent spinner state with no way for the user to know anything
// is wrong. AbortController turns that into a visible, retriable error
// after a bounded wait.
const REQUEST_TIMEOUT_MS = 15000;

let csrfTokenValue = "";
let csrfTokenPromise: Promise<string> | null = null;

async function fetchWithTimeout(input: string, init?: RequestInit): Promise<Response> {
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, REQUEST_TIMEOUT_MS);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } catch (err) {
    if (timedOut) {
      throw new ApiError("REQUEST_TIMEOUT", "The server took too long to respond. Please try again.", 0);
    }
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * ApiError carries the stable machine-readable `code` from the typed backend
 * error contract together with a safe human-readable `message`. Components
 * map `code` to user-facing copy and never parse fragile message strings.
 */
export class ApiError extends Error {
  code: string;
  status: number;
  /**
   * The decoded response body, when the server sent one. Some endpoints
   * deliberately return real, renderable data alongside a non-2xx status —
   * most notably POST /enterprise/domains/:id/dns/verify, which answers 429
   * during the verification cooldown with the LAST SUCCESSFUL DNS health
   * snapshot as its body. Callers must be able to keep rendering that data
   * instead of collapsing to a blocking error screen, so the body is
   * preserved here rather than discarded.
   */
  body: any;

  constructor(code: string, message: string, status: number, body?: any) {
    super(message || code || `Request failed (${status})`);
    this.name = "ApiError";
    this.code = code || "UNKNOWN_ERROR";
    this.status = status;
    this.body = body;
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
      return "This domain has reached its mailbox limit.";
    case "MAILBOX_ALREADY_EXISTS":
      return "A mailbox with this address already exists.";
    // Domain provisioning contract. INVALID_LIMIT / LIMIT_EXCEEDS_PLAN /
    // LIMIT_CONTRADICTION deliberately fall through to the server's own
    // message, which names the specific field and ceiling — strictly more
    // actionable than any generic sentence we could write here.
    case "PLAN_UNAVAILABLE":
      return "Your organization plan could not be read, so provisioning is blocked. Contact support.";
    case "DESCRIPTION_TOO_LONG":
      return "The description must be 500 characters or fewer.";
    case "INVALID_DKIM_SELECTOR":
      return "That DKIM selector is not valid. Use letters, digits, hyphens or underscores.";
    case "ALIAS_LIMIT_REACHED":
      return "This domain has reached its alias limit.";
    case "QUOTA_EXCEEDS_DOMAIN_MAXIMUM":
      return "That quota is larger than this domain's maximum quota per mailbox.";
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
    const res = await fetchWithTimeout(`${BASE}/csrf-token`, { credentials: "include" });
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
  /** When true, resolves with the raw Response body as a Blob instead of parsing JSON — for file downloads (e.g. the bulk mailbox template). */
  responseType?: "json" | "blob";
}

// Exported so feature modules under src/features/**/api.ts can perform
// HTTP transport through this same CSRF/auth-aware client instead of
// calling fetch() directly. A full relocation of this client into
// shared/api/ is tracked as follow-up scope, not done in this change
// to avoid a risky blind rewrite of every existing api.ts consumer.
export async function request<T>(path: string, options?: RequestOptions): Promise<T> {
  const method = options?.method || "GET";
  const isMutation = isMutationMethod(method);

  if (isMutation && !options?.skipCSRF && !csrfTokenValue) {
    await initCSRF();
  }

  const isFormData = typeof FormData !== "undefined" && options?.body instanceof FormData;

  const headers: Record<string, string> = {};
  if (!isFormData) {
    // multipart/form-data requires a browser-generated boundary in the
    // Content-Type header — never set it manually for a FormData body.
    headers["Content-Type"] = "application/json";
  }

  if (options?.headers) {
    const incoming = options.headers as Record<string, string>;
    for (const k of Object.keys(incoming)) {
      headers[k] = incoming[k];
    }
  }

  if (isMutation && !options?.skipCSRF && csrfTokenValue) {
    headers["X-CSRF-Token"] = csrfTokenValue;
  }

  const res = await fetchWithTimeout(`${BASE}${path}`, {
    ...options,
    credentials: "include",
    headers,
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
      throw new ApiError(code || "FORBIDDEN", message, res.status, body);
    }
    throw new ApiError(code, message, res.status, body);
  }

  if (res.status === 204) {
    return undefined as unknown as T;
  }

  if (options?.responseType === "blob") {
    return (await res.blob()) as unknown as T;
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
  // GET /enterprise/billing/state — coherent real subscription/plan/
  // usage/invoice summary with HONEST payment-provider configuration
  // state (configured:false + "provider not configured" when no
  // provider is wired; never fabricated cards/MRR/paid invoices).
  getBillingState: () =>
    request<{
      tenant_id: number;
      subscription: any | null;
      plan: any | null;
      usage: any | null;
      invoices: any[];
      payment_provider: { provider: string; enabled: boolean; configured: boolean; note: string };
    }>("/enterprise/billing/state"),

  // Customer domains
  listDomains: () => request<any>("/customer/domains"),
  getDomain: (id: number) => request<any>(`/customer/domains/${id}`),
  getDomainDNS: (id: number) => request<any>(`/customer/domains/${id}/dns`),
  verifyDomain: (id: number) =>
    request<any>(`/customer/domains/${id}/verify`, { method: "POST" }),

  // Enterprise (tenant-scoped)
  getOrganization: (id: number) => request<any>(`/enterprise/organizations/${id}`),
  listDomainsEnterprise: () => request<any>("/enterprise/domains"),
  /**
   * Organization plan + live usage for the provisioning wizard. Tenant-scoped
   * server-side: the tenant comes from the session, never from the caller.
   */
  getOrganizationCapacity: () => request<any>("/enterprise/organizations/current/capacity"),
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
  // CreateInvitation returns {"invitation": ..., "token": ...} — the
  // one-time token is shown exactly once by the caller (InvitationsPage).
  createInvitation: (data: any) =>
    request<{ invitation: any; token: string }>("/enterprise/invitations", { method: "POST", body: JSON.stringify(data) }),
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

  // Signup — email OTP verification flow (Phase D/E)
  signupStart: (data: { email: string; password: string; name?: string }) =>
    request<{ message: string; email: string }>("/auth/signup/start", {
      method: "POST",
      body: JSON.stringify(data),
    }),
  signupResend: (email: string) =>
    request<{ message: string }>("/auth/signup/resend", {
      method: "POST",
      body: JSON.stringify({ email }),
    }),
  signupVerify: (email: string, code: string) =>
    request<{ access_token: string; access_expires_in: number; refresh_expires_in: number }>(
      "/auth/signup/verify",
      { method: "POST", body: JSON.stringify({ email, code }) },
    ),

  // Dashboard
  getDashboard: () => request<any>("/enterprise/dashboard"),
  // getPlatformDashboard moved to features/platform/overview/api.ts

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

  // Platform organizations: moved to src/features/platform/organizations/
  // (contract.ts/api.ts/queries.ts/mutations.ts) as the SOLID
  // feature-directory pattern. createPlatformOrganization was removed
  // outright — it called POST /platform/organizations, which is not a
  // registered route (internal/api/router.go registers only GET on that
  // path); it was also unused by any component. Organizations are
  // created via tenant signup, not a platform-admin action — a real
  // platform-initiated "create organization" capability does not exist
  // in the backend today (MISSING_BACKEND_CAPABILITY, see the
  // capability matrix). getPlatformOrganization (GET /platform/
  // organizations/:id) was also unused — it returns an untyped
  // map[string]interface{} from a different service than the typed
  // detail endpoint the new feature module uses. updateOrganization
  // (PATCH /platform/organizations/:id) is now wired to a real, typed
  // "Edit organization" form (OrganizationEditForm.tsx) — see that
  // feature's api.ts/mutations.ts.

  // Mail Operations (queue admin) moved to
  // features/platform/mail-operations/api.ts. listPlatformQueue
  // (GET /queue, the legacy webmail-facing ListQueue handler) had no
  // admin-console caller and is a different resource than the
  // /admin/queue/* platform-admin endpoints the new feature uses.

  // Reliability (Backups/Restore/Updates/Monitoring/Storage/Cluster)
  // moved to features/platform/reliability/api.ts. getBackup (single),
  // getChangelog, applyModuleUpdate, getMonitoringHealth, and
  // getAdminRuntime were dead code (no UI caller) — getChangelog and
  // applyModuleUpdate correspond to real, registered routes
  // (/updates/changelog, /updates/apply/:module) with no frontend
  // action; tracked as a UI gap in the capability matrix rather than
  // wired to a fabricated control.

  // Security (Audit/SSL/Antivirus/Firewall/Guardian/Self-Heal/Log Rules)
  // moved to features/platform/security/api.ts. createFirewallRule and
  // uploadSslCertificate were dead code (real, registered routes —
  // POST /firewall/rules, POST /admin/ssl/certificates — with no UI
  // caller in either the old or new component); tracked as UI gaps in
  // the capability matrix rather than wired to fabricated forms.

  // Configuration (Settings/Feature Flags) moved to
  // features/platform/configuration/api.ts. getProtocolSettings /
  // patchProtocolSettings (GET/PATCH /admin/settings/protocol/:protocol)
  // are real, registered routes with no UI caller in either the old or
  // new component — tracked as a UI gap in the capability matrix.

  // Invoices
  listInvoices: () => request<any[]>("/enterprise/billing/invoices"),
  getInvoice: (id: number) => request<any>(`/enterprise/billing/invoices/${id}`),

  // Enterprise API keys (tenant-scoped, /enterprise/api-keys)
  listEnterpriseApiKeys: () => request<any[]>("/enterprise/api-keys"),
  createEnterpriseApiKey: (data: { name: string; scopes: string[] }) =>
    request("/enterprise/api-keys", { method: "POST", body: JSON.stringify(data) }),
  rotateEnterpriseApiKey: (id: number, scopes?: string[]) =>
    request(`/enterprise/api-keys/${id}/rotate`, { method: "POST", body: JSON.stringify({ scopes: scopes || [] }) }),
  deleteEnterpriseApiKey: (id: number) =>
    request(`/enterprise/api-keys/${id}`, { method: "DELETE" }),

  // Audit logs — the tenant-facing audit page reads the CANONICAL
  // {entries, total, limit, offset} envelope (same store + contract as the
  // platform audit page). The wrapper unwraps .entries so existing
  // consumers keep receiving the entry array; new UI should use
  // features/platform/audit/api.ts for the full paginated envelope.
  listAuditLogs: async (): Promise<any[]> => {
    const data = await request<any>("/enterprise/audit/logs");
    if (Array.isArray(data)) return data; // legacy bare-array fallback
    return data?.entries ?? [];
  },
  // Full paginated envelope for the tenant audit page (filters:
  // action/actor/result + page/page_size, same contract as the
  // platform audit page).
  listAuditLogsEnvelope: (params: { page?: number; page_size?: number; action?: string; actor?: string; result?: string } = {}) => {
    const p = new URLSearchParams();
    if (params.page) p.set("page", String(params.page));
    if (params.page_size) p.set("page_size", String(params.page_size));
    if (params.action) p.set("action", params.action);
    if (params.actor) p.set("actor", params.actor);
    if (params.result) p.set("result", params.result);
    const qs = p.toString();
    return request<{ entries: any[]; total: number; limit: number; offset: number }>(
      `/enterprise/audit/logs${qs ? `?${qs}` : ""}`,
    );
  },

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

  // Public invitation acceptance — the ONLY activation path for a
  // PSA-created organization owner (POST /auth/invitations/accept).
  // The email comes from the invitation row server-side, never from
  // this payload. Stable codes: 404 NOT_FOUND (unknown token),
  // 409 INVALID_STATE_TRANSITION (revoked/expired/already used),
  // 409 CONFLICT (an account already exists for the invited email).
  acceptInvitation: (data: { token: string; password: string; name?: string }) =>
    request<{
      status: string;
      user_id: number;
      organization_id: number;
      email: string;
      role: string;
      organization_active: boolean;
    }>("/auth/invitations/accept", { method: "POST", body: JSON.stringify(data) }),

  // Enterprise invitations — resend rotates the one-time token; the new
  // token is returned exactly once and replaces any prior copy.
  resendInvitation: (id: number) =>
    request<{ invitation: any; token: string; warning: string }>(`/enterprise/invitations/${id}/resend`, {
      method: "POST",
    }),
};
