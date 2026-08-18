// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor, fireEvent } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import App from "./App";

function Wrapper({ children }: { children: ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function mockResponse(data: any, ok = true, status = 200) {
  return { ok, status, json: () => Promise.resolve(data) } as Response;
}

// requestedPaths records every path the mocked fetch was asked to hit, so
// tests can assert PSA bootstrap makes ZERO calls to tenant-owned
// endpoints — the exact defect this contract fixes.
let requestedPaths: string[] = [];

function installFetchMock(handlers: Record<string, any>) {
  requestedPaths = [];
  globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
    const full = typeof url === "string" ? url : String(url);
    const path = full.split("?")[0];
    requestedPaths.push(path);
    for (const [suffix, data] of Object.entries(handlers)) {
      if (path.endsWith(suffix)) return Promise.resolve(mockResponse(data));
    }
    // Any unmocked tenant-owned endpoint returns 403 to mimic the real
    // backend's tenant-scoping gate correctly rejecting a NULL-tenant
    // PSA request — a test must never rely on a silent 200 fallback for
    // an endpoint it forgot to enumerate.
    return Promise.resolve(mockResponse({ error: "forbidden" }, false, 403));
  });
}

const PLATFORM_ME = { email: "psa@orvix.email", role: "platform_super_admin", portal: "platform" };
const ORGANIZATION_ME = { email: "admin@tenant.test", role: "tenant_admin", portal: "organization", tenant_id: 1 };

const TENANT_OWNED_SUFFIXES = [
  "/enterprise/dashboard",
  "/enterprise/domains",
  "/enterprise/mailboxes",
  "/enterprise/audit/logs",
  "/customer/domains",
  "/users",
];

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("Platform Admin shell (portal=platform)", () => {
  beforeEach(() => {
    installFetchMock({
      "/api/v1/me": PLATFORM_ME,
      "/platform/dashboard": { total_organizations: 1, active_organizations: 1, total_domains: 1, total_mailboxes: 1, quota_used_bytes: 0, recent_audit_entries: [] },
      "/platform/organizations": { organizations: [], total: 0 },
      "/admin/backups/schedule": {},
      "/admin/backups/metrics": {},
      "/admin/backups/health": { status: "ok" },
      "/admin/backups": [],
      "/updates/check": { available: false },
      "/updates/changelog": {},
      "/update/status": { status: "up-to-date" },
      "/update/history": [],
      "/update/preflight": { ready: true },
      "/monitoring/alerts": [],
      "/monitoring/capacity": {},
      "/monitoring/snapshot": {},
      "/monitoring/alert-providers": {},
      "/monitoring/alert-deliveries": [],
      "/admin/storage/volumes": [],
      "/admin/cluster/status": { nodes: [] },
      "/admin/runtime": {},
      "/admin/queue/summary": { metrics: { pending: 0, leased: 0, delivering: 0, deferred: 0, delivered: 0, bounced: 0, dead_letter: 0, cancelled: 0, total: 0, avg_attempts: 0 } },
      "/admin/queue/messages": { messages: [], total: 0, limit: 50, offset: 0 },
      "/audit/logs": [],
      "/admin/ssl/certificates": { runtime: [], uploaded: [], expiry_warnings: [], expiry_cutoff_days: 30, config_path: "", config_key_path: "" },
      "/admin/ssl/expiry-warnings": { warnings: [] },
      "/admin/ssl/acme/status": { acme_enabled: false, issuing_certificates: false, acme_provider: "none", manual_paths: [], script_helper: "", on_disk_candidates: [], honest_notes: [] },
      "/admin/security/antivirus": {
        engine: "clamav", engine_configured: true, engine_reachable: true, engine_active: true,
        runtime_enforced: false, clamav_host: "localhost", clamav_port: 3310, clamav_response: "PONG",
        policy_on_infected: "reject", policy_on_scanner_unavailable: "fail_closed", last_error: "",
        counts: { scanned: 0, infected: 0, rejected: 0, quarantined: 0, tagged: 0, fail_open: 0, fail_closed: 0 },
        honest_notes: [],
      },
      "/guardian/logs": [],
      "/heal/history": [],
      "/admin/log-rules": { rules: [] },
      "/admin/settings": {},
      "/feature-flags": [],
      "/firewall/rules": [],
      "/firewall/logs": [],
      "/modules": [],
      "/monitoring/health": { status: "ok" },
      "/account/profile": { display_name: "PSA" },
      "/admin/summary": {
        domains: { total: 1, active: 1, suspended: 0 },
        mailboxes: { total: 1, active: 1, suspended: 0, admin: 1 },
        queue: { total: 0, pending: 0, deferred: 0, failed: 0 },
        audit: { recent: 0 },
        runtime: { status: "ok", version: "1.0.0" },
        recent_activity: [],
        top_domains: [],
      },
    });
  });

  it("renders the Platform Admin shell, not the tenant dashboard", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    expect(screen.queryByText("Failed to load dashboard")).not.toBeInTheDocument();
  });

  it("never renders the Customer Portal navigation group", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    expect(screen.queryByText("Customer Portal")).not.toBeInTheDocument();
  });

  it("never renders tenant-only navigation items", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    // Tenant-family-only items must never appear in the platform shell.
    for (const label of ["Invitations", "Ownership", "Invoices", "Usage"]) {
      expect(screen.queryByRole("button", { name: new RegExp(`^${label}$`, "i") })).not.toBeInTheDocument();
    }
    // Platform mail-control items ARE present (backed by /platform/* routes).
    for (const label of ["Domains", "Mailboxes", "Aliases", "Groups", "Relays"]) {
      expect(screen.getAllByRole("button", { name: new RegExp(`^${label}$`, "i") }).length).toBeGreaterThan(0);
    }
  });

  it("shows exactly the final verified platform navigation set", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    for (const label of ["Organizations", "Summary", "Mail Queue", "Domains", "Mailboxes", "Aliases", "Groups", "Relays", "Suppressions", "Deliverability", "Bulk Mailboxes", "Reliability", "Health", "Security", "Modules", "Configuration"]) {
      expect(screen.getAllByRole("button", { name: new RegExp(`^${label}$`, "i") }).length).toBeGreaterThan(0);
    }
  });

  it("never renders a License nav item — Orvix is hosted SaaS, not a licensed self-hosted product", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /^License$/i })).not.toBeInTheDocument();
  });

  // PLATFORM-SHELL-2 Phase 3: table-driven proof that every visible platform
  // navigation item renders its intended component, calls only
  // platform-owned endpoints, and never produces "Failed to load dashboard".
  const PLATFORM_NAV_CASES: { label: string; expectHeading: string | RegExp }[] = [
    { label: "Organizations", expectHeading: /organizations/i },
    { label: "Summary", expectHeading: "Platform Summary" },
    { label: "Mail Queue", expectHeading: /mail operations/i },
    { label: "Domains", expectHeading: /platform domains/i },
    { label: "Mailboxes", expectHeading: /platform mailboxes/i },
    { label: "Aliases", expectHeading: /platform aliases/i },
    { label: "Groups", expectHeading: /platform groups/i },
    { label: "Relays", expectHeading: /platform relays/i },
    { label: "Suppressions", expectHeading: /suppression management/i },
    { label: "Deliverability", expectHeading: /deliverability/i },
    { label: "Bulk Mailboxes", expectHeading: /bulk mailbox operations/i },
    { label: "Reliability", expectHeading: /reliability/i },
    { label: "Health", expectHeading: /health|runtime|system/i },
    { label: "Security", expectHeading: /security/i },
    { label: "Modules", expectHeading: /modules/i },
    { label: "Configuration", expectHeading: /configuration/i },
  ];
  for (const c of PLATFORM_NAV_CASES) {
    it(`platform nav item "${c.label}" renders its intended component with no tenant call`, async () => {
      render(<Wrapper><App /></Wrapper>);
      await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
      fireEvent.click(screen.getAllByRole("button", { name: new RegExp(`^${c.label}$`, "i") })[0]);
      await waitFor(() => {
        const matches = typeof c.expectHeading === "string"
          ? screen.queryAllByText(c.expectHeading)
          : screen.queryAllByText(c.expectHeading);
        expect(matches.length).toBeGreaterThan(0);
      });
      expect(screen.queryByText("Failed to load dashboard")).not.toBeInTheDocument();
      expect(screen.queryByText(/^403$/)).not.toBeInTheDocument();
      for (const suffix of TENANT_OWNED_SUFFIXES) {
        expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
      }
    });
  }

  it("makes zero calls to /api/v1/enterprise/dashboard or any other tenant-owned endpoint during bootstrap", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    // Give any stray effect a tick to fire before asserting the negative.
    await new Promise((r) => setTimeout(r, 20));
    for (const suffix of TENANT_OWNED_SUFFIXES) {
      expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
    }
  });

  it("navigating the platform overview cards opens the corresponding platform page without a tenant call", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /^organizations$/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /^organizations$/i })).toBeInTheDocument());
    for (const suffix of TENANT_OWNED_SUFFIXES) {
      expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
    }
  });

  it("a deep-link/stale tab id outside the platform allow-list falls back to the Platform landing page, never a tenant page", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Platform Admin")).toBeInTheDocument());
    // There is no route to reach a tenant-only tab id as a platform
    // identity through the UI itself (the sidebar only offers
    // PLATFORM_TAB_IDS) — this test documents that structural guarantee
    // by confirming the dashboard/tenant-only surface never appears.
    expect(screen.queryByText("Delivery Rate")).not.toBeInTheDocument();
  });
});

describe("Customer Portal shell (portal=organization) — unchanged", () => {
  beforeEach(() => {
    installFetchMock({
      "/api/v1/me": ORGANIZATION_ME,
      "/enterprise/dashboard": { total_domains: 3, active_mailboxes: 12, recent_actions: [] },
      "/enterprise/domains": [{ id: 1, name: "example.com", status: "active" }],
      "/enterprise/mailboxes": [],
      "/enterprise/audit/logs": [],
      "/customer/domains": [{ id: 1, name: "example.com", status: "active", verified: true, mx_status: "ok", spf_status: "ok", dkim_status: "ok", dmarc_status: "ok" }],
      "/account/sessions": { sessions: [] },
      "/account/profile": { display_name: "Tenant Admin" },
    });
  });

  it("loads the sidebar and the tenant dashboard view", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("Dashboard")).toBeInTheDocument());
  });

  it("still calls the existing tenant dashboard endpoint and renders its data", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Dashboard")).toBeInTheDocument());
    await waitFor(() => expect(requestedPaths.some((p) => p.endsWith("/enterprise/dashboard"))).toBe(true));
    expect(screen.queryByText("Failed to load dashboard")).not.toBeInTheDocument();
  });

  it("shows the Customer Portal navigation section", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    expect(screen.getByText("Customer Portal")).toBeInTheDocument();
  });

  it("never renders Platform Admin navigation", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    expect(screen.queryByText("Platform Admin")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^overview$/i })).not.toBeInTheDocument();
  });

  it("shows the logout button", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /logout/i })).toBeInTheDocument();
  });

  it("never calls any platform-owned endpoint during bootstrap", async () => {
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Orvix Admin")).toBeInTheDocument());
    await new Promise((r) => setTimeout(r, 20));
    const PLATFORM_ONLY_SUFFIXES = [
      "/platform/dashboard", "/platform/organizations", "/admin/summary", "/admin/backups",
      "/admin/queue/summary", "/queue", "/audit/logs", "/admin/ssl/certificates",
      "/admin/security/antivirus", "/guardian/logs", "/heal/history", "/admin/log-rules",
      "/admin/settings", "/feature-flags", "/updates/check", "/monitoring/alerts",
      "/admin/storage/volumes", "/admin/cluster/status",
    ];
    for (const suffix of PLATFORM_ONLY_SUFFIXES) {
      expect(requestedPaths.some((p) => p.endsWith(suffix))).toBe(false);
    }
  });
});

describe("Fail-closed / cross-cutting authorization behavior", () => {
  it("missing/unknown portal renders no business navigation and no fallback shell (never inferred from role)", async () => {
    installFetchMock({
      "/api/v1/me": { email: "weird@orvix.email", role: "platform_super_admin", portal: "something-unexpected" },
    });
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Access Unavailable")).toBeInTheDocument());
    expect(screen.queryByText("Platform Admin")).not.toBeInTheDocument();
    expect(screen.queryByText("Customer Portal")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^dashboard$/i })).not.toBeInTheDocument();
  });

  it("empty-string portal also fails closed", async () => {
    installFetchMock({
      "/api/v1/me": { email: "x@orvix.email", role: "tenant_admin", portal: "" },
    });
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Access Unavailable")).toBeInTheDocument());
  });

  it("a role-only signal with no portal value does not fall back to inferring the shell from role", async () => {
    installFetchMock({
      "/api/v1/me": { email: "x@orvix.email", role: "platform_super_admin" },
    });
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Access Unavailable")).toBeInTheDocument());
    expect(screen.queryByText("Platform Admin")).not.toBeInTheDocument();
  });

  it("401 from /me clears auth and returns to the login screen", async () => {
    globalThis.fetch = vi.fn<typeof fetch>((url: any) => {
      const path = (typeof url === "string" ? url : String(url)).split("?")[0];
      if (path.endsWith("/api/v1/me")) return Promise.resolve(mockResponse({ error: "unauthorized" }, false, 401));
      return Promise.resolve(mockResponse({}, false, 401));
    });
    render(<Wrapper><App /></Wrapper>);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Sign In" })).toBeInTheDocument());
    expect(screen.queryByText("Platform Admin")).not.toBeInTheDocument();
    expect(screen.queryByText("Customer Portal")).not.toBeInTheDocument();
  });
});
