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
    request("/enterprise/abuse/signals/acknowledge", { method: "POST", body: JSON.stringify({ id }) }),
  resolveSignal: (id: number) =>
    request("/enterprise/abuse/signals/resolve", { method: "POST", body: JSON.stringify({ id }) }),
  checkSendLimit: () => request<any>("/enterprise/abuse/send-limit"),

  // Auth helpers
  login: (email: string, password: string) =>
    request<any>("/auth/login", { method: "POST", body: JSON.stringify({ email, password }) }),
  refresh: () => request<any>("/auth/refresh", { method: "POST" }),

  // Current user
  getMe: () => request<any>("/me"),
};
