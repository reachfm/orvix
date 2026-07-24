const BASE = "/api/v1";

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    credentials: "include",
    headers: { "Content-Type": "application/json", ...options?.headers },
    ...options,
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `${res.status} ${res.statusText}`);
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
  listMailboxes: () => request<any>("/enterprise/mailboxes"),
  createMailbox: (data: any) =>
    request("/enterprise/mailboxes", { method: "POST", body: JSON.stringify(data) }),
  deleteMailbox: (id: number) =>
    request(`/enterprise/mailboxes/${id}`, { method: "DELETE" }),

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

  // Platform admin summary/users/firewall/modules (superadmin/admin scope,
  // distinct from the tenant-scoped /enterprise/* endpoints above)
  getAdminSummary: () => request<any>("/admin/summary"),
  listPlatformUsers: () => request<any[]>("/users"),
  deleteUser: (userId: number) => request(`/users/${userId}`, { method: "DELETE" }),
  listFirewallRules: () => request<any[]>("/firewall/rules"),
  listFirewallLogs: () => request<any[]>("/firewall/logs"),
  listModules: () => request<any[]>("/modules"),

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
