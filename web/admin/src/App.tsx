import { useState, useEffect } from "react";
import { LayoutDashboard, Globe, Users, Shield, Zap, Activity, Settings, Server, Building, Mail, Monitor, HardDrive, HeartPulse, CreditCard, Keyboard, User, AtSign, BarChart, AlertTriangle, UserPlus, Send, LogOut, FileText, Bell, ShieldAlert, Upload, MessageSquare } from "lucide-react";
import Dashboard from "./components/Dashboard";
import Domains from "./components/Domains";
import UsersPage from "./components/UsersPage";
import SecurityPageFeature from "./features/platform/security/page";
import Modules from "./components/Modules";
import AuditLog from "./components/AuditLog";
import EnterpriseDashboard from "./components/EnterpriseDashboard";
import MailboxList from "./components/MailboxList";
import OrganizationsPage from "./features/platform/organizations/page";
import SystemHealth from "./components/SystemHealth";
import BillingPage from "./components/BillingPage";
import ApiKeysPage from "./components/ApiKeysPage";
import SignupPage from "./components/SignupPage";
import LoginPage from "./components/LoginPage";
import ForgotPasswordPage from "./components/ForgotPasswordPage";
import ResetPasswordPage from "./components/ResetPasswordPage";
import InvitationAcceptPage from "./components/InvitationAcceptPage";
import AccountSettingsPage from "./components/AccountSettingsPage";
import OrganizationOverviewPage from "./components/OrganizationOverviewPage";
import InvitationsPage from "./components/InvitationsPage";
import MembersRolesPage from "./components/MembersRolesPage";
import OwnershipTransferPage from "./components/OwnershipTransferPage";
import SuspensionDeletionPage from "./components/SuspensionDeletionPage";
import CustomerMailboxesPage from "./components/CustomerMailboxesPage";
import AliasesPage from "./components/AliasesPage";
import GroupsPage from "./components/GroupsPage";
import UsageQuotasPage from "./components/UsageQuotasPage";
import InvoicesPage from "./components/InvoicesPage";
import SecurityPage from "./components/SecurityPage";
import SupportPage from "./components/SupportPage";
import PreferencesPage from "./components/PreferencesPage";
import OverviewPage from "./features/platform/overview/page";
import MailOperationsPage from "./features/platform/mail-operations/page";
import ReliabilityPage from "./features/platform/reliability/page";

import ConfigurationPage from "./features/platform/configuration/page";
import PlatformBillingPage from "./features/platform/platform-billing/page";
import ImportsPage from "./features/platform/imports/page";
import AutomationJobsPage from "./features/platform/automation-jobs/page";
import SupportAccessPage from "./features/platform/support-access/page";
import IncidentsPage from "./features/platform/incidents/page";
import RetentionPage from "./features/platform/retention/page";
import AuditPage from "./features/platform/audit/page";
import DRPage from "./features/platform/dr/page";
import ConfigTruthPage from "./features/platform/config-truth/page";
import PlatformDomainsPage from "./features/platform/domains/page";
import PlatformMailboxesPage from "./features/platform/mailboxes/page";
import PlatformAliasesPage from "./features/platform/aliases/page";
import PlatformGroupsPage from "./features/platform/groups/page";
import PlatformRelaysPage from "./features/platform/relay/page";
import PlatformSuppressionsPage from "./features/platform/suppressions/page";
import PlatformDeliverabilityPage from "./features/platform/deliverability/page";
import BulkMailboxesPage from "./features/platform/bulk-mailboxes/page";
import BulkMailboxImportPage from "./features/platform/bulk-mailbox-import/page";
import SupportInboxPage from "./features/platform/support-inbox/page";
import { initCSRF, api } from "./api";
import ThemeToggle from "./shared/theme/ThemeToggle";
import PlatformShell from "./features/platform/shell/PlatformShell";

type Tab = "dashboard" | "domains" | "users" | "firewall" | "modules" | "audit" | "settings"
  | "enterprise" | "mailboxes" | "organizations" | "health" | "platform-home"
  | "mail-operations" | "reliability" | "platform-security" | "platform-configuration"
  | "billing" | "onboarding" | "apikeys"
  | "account-settings" | "org-overview" | "invitations" | "members-roles" | "ownership-transfer"
  | "suspension-deletion" | "customer-mailboxes" | "aliases" | "groups" | "usage-quotas"
  | "invoices" | "security" | "support" | "preferences"
  | "login" | "signup" | "forgot-password" | "reset-password" | "invitation-accept"
  | "platform-billing" | "platform-imports" | "automation-jobs" | "support-access"
  | "platform-incidents" | "platform-retention" | "platform-audit" | "platform-dr"
  | "platform-config-truth" | "platform-domains" | "platform-mailboxes"
  | "platform-aliases" | "platform-groups" | "platform-relays"
  | "platform-suppressions" | "platform-deliverability" | "platform-bulk-mailboxes"
  | "platform-bulk-mailbox-import" | "platform-support-inbox";

// PORTAL-SEPARATION-PHASE1 / PLATFORM-SHELL: the explicit allow-list for
// each portal. portal="platform" (Platform Super Admin, tenant_id=NULL)
// gets ONLY tabs whose underlying API calls are verified platform-owned
// routes (platformMW-gated in internal/api/router.go) plus the
// self-scoped /account/* pages. It must NEVER include a tab backed by a
// tenant-owned endpoint (/enterprise/*, /users, /customer/*, /firewall
// is platform-owned — verified below) — doing so would let a NULL-tenant
// identity trigger a tenant-scoped request that the backend correctly
// rejects, producing the "Failed to load dashboard" defect this fixes.
//
// Verified ownership (internal/api/router.go, as of this change):
//   platform-owned: /platform/organizations, /admin/backups,
//     /firewall/rules, /firewall/logs, /modules, /monitoring/health,
//     /admin/summary (EnterpriseDashboard.tsx, despite its name — see
//     below), /license
//   tenant-owned:    /enterprise/* EXCEPT /admin/summary (dashboard,
//     domains, mailboxes, audit/logs, ...), /users (tenantCompatMW),
//     /customer/*
//   self-scoped (safe for either portal): /account/*
//
// NOTE on "domains" (Domains.tsx -> /enterprise/domains): the pre-existing
// code showed this tab only to the "platform" side of its old ad hoc
// filter, but the handler (ListAdminDomains) calls auth.RequireTenantID
// and 403s a NULL-tenant caller — it is, and always was, tenant-owned.
// It is listed under ORGANIZATION_TAB_IDS here, matching the real backend
// authorization, and is correctly absent from PLATFORM_TAB_IDS.
//
// NOTE on "enterprise" (EnterpriseDashboard.tsx -> api.getAdminSummary ->
// /admin/summary): despite the component's own on-page copy ("Customer
// administration overview") and its historical "Customer Admin" section
// label, the route is platformMW-gated and AdminSummary aggregates
// PLATFORM-WIDE totals across every tenant — it is genuinely
// platform-owned. PLATFORM-SHELL's first pass incorrectly excluded it
// (a real regression, restored here as the "Summary" item under
// Operations); the misleading on-page copy is corrected in
// EnterpriseDashboard.tsx itself.
//
// NOTE on removing "license": GetLicense (internal/api/handlers/handlers.go)
// unconditionally returns {"status":"not_required", "reason":"Local product
// licensing is disabled; SaaS plans and quotas apply."} and ValidateLicense
// returns 410 Gone — Orvix is a hosted SaaS, not a self-hosted licensed
// product, so this backend concept has no real operational meaning here.
// The removed LicenseStatus.tsx page also called fetch() directly and
// rendered a tier/expires_at/customer_id/warnings schema the handler has
// never returned since this retirement — a stale, fake-looking UI on top
// of a conceptually wrong feature. Commercial plans/subscriptions/billing
// are a distinct future bounded context (platform-commercial-control-plane),
// not a repurposing of this endpoint.
const PLATFORM_TAB_IDS: Tab[] = [
  "platform-home", "organizations", "platform-billing", "platform-imports", "automation-jobs",
  "platform-incidents", "platform-retention", "platform-dr", "enterprise", "mail-operations",
  "reliability", "health", "platform-audit", "platform-support-inbox", "support-access",
  "platform-security", "modules", "platform-configuration", "platform-config-truth",
  "platform-domains", "platform-mailboxes", "platform-aliases", "platform-groups",
  "platform-relays", "platform-suppressions", "platform-deliverability", "platform-bulk-mailboxes",
  "platform-bulk-mailbox-import",
  "account-settings", "security", "preferences",
];
const ORGANIZATION_TAB_IDS: Tab[] = [
  "dashboard", "domains", "org-overview", "customer-mailboxes", "aliases", "groups", "usage-quotas",
  "invitations", "members-roles", "ownership-transfer", "suspension-deletion", "invoices",
  "billing", "apikeys", "account-settings", "security", "preferences", "support",
  "audit",
];

const tabs: { id: Tab; label: string; icon: typeof LayoutDashboard; section?: string }[] = [
  // Platform (portal="platform") navigation. Grouped per PLATFORM-SHELL-2:
  // Overview / Organizations / Operations / Security / System / Account.
  { id: "platform-home", label: "Overview", icon: LayoutDashboard },
  { id: "organizations", label: "Organizations", icon: Building, section: "Commercial" },
  { id: "platform-billing", label: "Platform Billing", icon: CreditCard },
  { id: "platform-imports", label: "Imports", icon: Send, section: "Operations" },
  { id: "automation-jobs", label: "Automation Jobs", icon: Zap },
  // Mail Control group — platform identity inventory (PSA only).
  { id: "platform-domains", label: "Domains", icon: Globe, section: "Mail Control" },
  { id: "platform-mailboxes", label: "Mailboxes", icon: Mail },
  { id: "platform-aliases", label: "Aliases", icon: AtSign },
  { id: "platform-groups", label: "Groups", icon: Users },
  { id: "platform-relays", label: "Relays", icon: Send },
  // Operations group — queue + delivery operations.
  { id: "mail-operations", label: "Mail Queue", icon: Send, section: "Operations" },
  { id: "platform-suppressions", label: "Suppressions", icon: Shield },
  { id: "platform-deliverability", label: "Deliverability", icon: BarChart },
  { id: "platform-bulk-mailboxes", label: "Bulk Mailboxes", icon: Users },
  { id: "platform-bulk-mailbox-import", label: "Bulk Import", icon: Upload },
  { id: "platform-incidents", label: "Incidents", icon: AlertTriangle },
  { id: "platform-retention", label: "Retention", icon: FileText },
  { id: "platform-dr", label: "DR", icon: HardDrive },
  { id: "enterprise", label: "Summary", icon: Monitor },
  { id: "reliability", label: "Reliability", icon: HardDrive },
  { id: "health", label: "Health", icon: HeartPulse },
  { id: "platform-audit", label: "Audit Log", icon: Activity, section: "Security" },
  { id: "support-access", label: "Support Access", icon: ShieldAlert },
  { id: "platform-support-inbox", label: "Support Inbox", icon: MessageSquare },
  { id: "platform-security", label: "Security", icon: ShieldAlert },
  { id: "modules", label: "Modules", icon: Zap, section: "System" },
  { id: "platform-configuration", label: "Configuration", icon: Settings },
  { id: "platform-config-truth", label: "Config Truth", icon: Settings },
  // Tabs below are pre-existing tenant-owned entries that were never
  // reachable by a real Platform Super Admin (see PLATFORM-SHELL final
  // report's ownership matrix) — their labels/sections are irrelevant to
  // the platform portal since PLATFORM_TAB_IDS excludes them; they exist
  // here only for the organization portal's tabs.
  { id: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { id: "mailboxes", label: "Mailboxes", icon: Mail },
  { id: "domains", label: "Domains", icon: Globe },
  { id: "users", label: "Users", icon: Users },
  { id: "audit", label: "Audit Log", icon: Activity },
  { id: "settings", label: "Settings", icon: Settings },
  { id: "org-overview", label: "Organization", icon: Building, section: "Customer Portal" },
  { id: "customer-mailboxes", label: "Mailboxes", icon: Mail },
  { id: "aliases", label: "Aliases", icon: AtSign },
  { id: "groups", label: "Groups", icon: Users },
  { id: "usage-quotas", label: "Usage", icon: BarChart },
  { id: "invitations", label: "Invitations", icon: UserPlus },
  { id: "members-roles", label: "Members", icon: Shield },
  { id: "ownership-transfer", label: "Ownership", icon: Send },
  { id: "suspension-deletion", label: "Status", icon: AlertTriangle },
  { id: "invoices", label: "Invoices", icon: FileText },
  { id: "billing", label: "Billing", icon: CreditCard },
  { id: "apikeys", label: "API Keys", icon: Keyboard },
  { id: "account-settings", label: "Account", icon: User, section: "Account" },
  { id: "security", label: "Security", icon: Shield },
  { id: "preferences", label: "Preferences", icon: Bell },
  { id: "support", label: "Support", icon: HeartPulse },
];

export default function App() {
  const [currentTab, setCurrentTab] = useState<Tab>("dashboard");
  const [authenticated, setAuthenticated] = useState(false);
  const [authLoading, setAuthLoading] = useState(true);
  const [userRole, setUserRole] = useState("");
  // PORTAL-SEPARATION-PHASE1: portal is derived on the server from the
  // authenticated user's role + tenant (see /api/v1/me). The client
  // never sends this — it only reads it. "" means the backend refused
  // to classify the user; the UI shows nothing platform-scoped.
  const [portal, setPortal] = useState<"platform" | "organization" | "">("");

  const [userEmail, setUserEmail] = useState("");

  useEffect(() => {
    // A failed api.getMe() (401/403/5xx, or a network error) means
    // there is no valid authenticated session: clear local auth state
    // and fall back to the login screen. No portal-specific request
    // has been issued yet at this point, so there is nothing else to
    // unwind.
    api.getMe()
      .then((u) => {
        setAuthenticated(true);
        setUserRole(u.role || "");
        setUserEmail(u.email || "");
        // portal is the ONLY authoritative shell selector. Anything
        // other than the two known values fails closed to "" — never
        // inferred from `role`.
        const resolvedPortal = (u.portal === "platform" || u.portal === "organization") ? u.portal : "";
        setPortal(resolvedPortal);
        // Resolve the landing tab from the portal BEFORE authLoading
        // flips false, so the first real paint already shows the
        // correct shell — no flash of the wrong portal.
        setCurrentTab(resolvedPortal === "platform" ? "platform-home" : "dashboard");
        initCSRF().catch(() => {});
        setAuthLoading(false);
      })
      .catch(() => { setAuthenticated(false); setAuthLoading(false); });
  }, []);

  void userRole; // kept for display/debugging; portal is the sole authorization gate.

  // Explicit allow-list per portal — see PLATFORM_TAB_IDS/ORGANIZATION_TAB_IDS
  // above. An unknown/empty portal renders zero navigation items
  // (fail-closed), never a mix of both shells.
  const allowedTabIds: Tab[] =
    portal === "platform" ? PLATFORM_TAB_IDS :
    portal === "organization" ? ORGANIZATION_TAB_IDS :
    [];
  const filteredTabs = tabs.filter((t) => allowedTabIds.includes(t.id));

  const navigateTo = (route: string) => {
    const tabMap: Record<string, Tab> = {
      "/": "dashboard", "/login": "login", "/signup": "signup",
      "/forgot-password": "forgot-password", "/reset-password": "reset-password",
      "/invitations/accept": "invitation-accept",
    };
    setCurrentTab(tabMap[route] || "dashboard");
  };

  const tabFromPath = (path: string): Tab => {
    if (path === "/admin" || path === "/admin/" || path === "/admin/login") return "login";
    if (path === "/admin/signup") return "signup";
    if (path === "/admin/forgot-password") return "forgot-password";
    if (path === "/admin/reset-password") return "reset-password";
    if (path === "/admin/invitations/accept") return "invitation-accept";
    return "dashboard";
  };

  useEffect(() => {
    const onPopState = () => setCurrentTab(tabFromPath(window.location.pathname));
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    setCurrentTab(tabFromPath(window.location.pathname));
  }, []);

  if (authLoading) {
    return <div className="h-screen bg-[var(--bg-base)] flex items-center justify-center"><p className="text-[var(--text-secondary)]">Loading...</p></div>;
  }

  if (!authenticated) {
    switch (currentTab) {
      case "signup": return <SignupPage />;
      case "forgot-password": return <ForgotPasswordPage />;
      case "reset-password": return <ResetPasswordPage />;
      case "invitation-accept": return <InvitationAcceptPage />;
      default: return <LoginPage />;
    }
  }

  // Fail-closed: an authenticated identity whose /me response did not
  // carry a recognized portal value gets NO business navigation and NO
  // shell — neither Platform Administration nor Customer Portal. This
  // must never fall back to inferring the shell from `role`.
  if (portal === "") {
    return (
      <div className="h-screen bg-[var(--bg-base)] flex items-center justify-center">
        <div className="text-center max-w-sm">
          <h1 className="text-lg font-semibold text-[var(--text-primary)] mb-2">Access Unavailable</h1>
          <p className="text-sm text-[var(--text-secondary)] mb-6">
            This account could not be authorized for the admin console. Contact your administrator.
          </p>
          <button
            onClick={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
            className="text-sm text-[var(--accent)] hover:underline"
          >
            Return to login
          </button>
        </div>
      </div>
    );
  }

  const renderContent = () => {
    // Defense-in-depth structural gate: even if currentTab somehow holds
    // an id outside the current portal's allow-list (stale state, a
    // future regression in the sidebar), never mount the other portal's
    // component — fail back to that portal's own landing page instead.
    if (!allowedTabIds.includes(currentTab)) {
      return portal === "platform" ? <OverviewPage email={userEmail} onNavigate={setCurrentTab} /> : <Dashboard />;
    }
    switch (currentTab) {
      case "platform-home": return <OverviewPage email={userEmail} onNavigate={setCurrentTab} />;
      case "dashboard": return <Dashboard />;
      case "domains": return <Domains />;
      case "users": return <UsersPage />;
      case "modules": return <Modules />;
      case "audit": return <AuditLog />;
      case "enterprise": return <EnterpriseDashboard />;
      case "mailboxes": return <MailboxList />;
      case "organizations": return <OrganizationsPage />;
      case "platform-billing": return <PlatformBillingPage />;
      case "platform-imports": return <ImportsPage />;
      case "automation-jobs": return <AutomationJobsPage />;
      case "platform-incidents": return <IncidentsPage />;
      case "platform-retention": return <RetentionPage />;
      case "platform-dr": return <DRPage />;
      case "platform-audit": return <AuditPage />;
      case "support-access": return <SupportAccessPage />;
      case "platform-config-truth": return <ConfigTruthPage />;
      case "platform-domains": return <PlatformDomainsPage />;
      case "platform-mailboxes": return <PlatformMailboxesPage />;
      case "platform-aliases": return <PlatformAliasesPage />;
      case "platform-groups": return <PlatformGroupsPage />;
      case "platform-relays": return <PlatformRelaysPage />;
      case "platform-suppressions": return <PlatformSuppressionsPage />;
      case "platform-deliverability": return <PlatformDeliverabilityPage />;
      case "platform-bulk-mailboxes": return <BulkMailboxesPage />;
      case "platform-bulk-mailbox-import": return <BulkMailboxImportPage />;
      case "platform-support-inbox": return <SupportInboxPage />;
      case "health": return <SystemHealth />;
      case "mail-operations": return <MailOperationsPage />;
      case "reliability": return <ReliabilityPage />;
      case "platform-security": return <SecurityPageFeature />;
      case "platform-configuration": return <ConfigurationPage />;
      case "billing": return <BillingPage />;
      // Legacy "Domain Setup" route. It rendered a second, inferior copy of the
      // DNS record UI against the customer endpoints (and read fields such as
      // `mx_status`/`mx_record` that the DNS response never actually contained,
      // so every record showed "pending"). Rather than maintain two DNS
      // implementations, the route now resolves to the domains console, whose
      // per-domain "DNS" action opens the canonical DNS Records modal. The
      // customer DNS endpoints themselves are left untouched.
      case "onboarding": return <Domains />;
      case "apikeys": return <ApiKeysPage />;
      case "account-settings": return <AccountSettingsPage />;
      case "org-overview": return <OrganizationOverviewPage />;
      case "invitations": return <InvitationsPage />;
      case "members-roles": return <MembersRolesPage />;
      case "ownership-transfer": return <OwnershipTransferPage />;
      case "suspension-deletion": return <SuspensionDeletionPage />;
      case "customer-mailboxes": return <CustomerMailboxesPage />;
      case "aliases": return <AliasesPage />;
      case "groups": return <GroupsPage />;
      case "usage-quotas": return <UsageQuotasPage />;
      case "invoices": return <InvoicesPage />;
      case "security": return <SecurityPage />;
      case "support": return <SupportPage />;
      case "preferences": return <PreferencesPage />;
      default: return portal === "platform" ? <OverviewPage email={userEmail} onNavigate={setCurrentTab} /> : <Dashboard />;
    }
  };

  // PLATFORM-SHELL-REDESIGN: portal === "platform" renders the premium
  // PlatformShell (sidebar + top bar with search/alerts/theme). The
  // organization portal keeps its original layout below completely
  // unchanged — this is a purely visual branch, not a new
  // authorization path; filteredTabs/allowedTabIds/renderContent are
  // exactly the same values either way.
  if (portal === "platform") {
    return (
      <PlatformShell
        tabs={filteredTabs}
        currentTab={currentTab}
        onSelectTab={setCurrentTab}
        userEmail={userEmail}
        onLogout={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
      >
        {renderContent()}
      </PlatformShell>
    );
  }

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="w-64 bg-[var(--bg-surface)] border-r border-[var(--border)] flex flex-col">
        <div className="p-4 border-b border-[var(--border)] flex items-center gap-3">
          <Server size={24} className="text-[var(--accent)]" />
          <div className="flex-1 min-w-0">
            <h1 className="text-sm font-semibold text-[var(--text-primary)]">Orvix Admin</h1>
            <p className="text-xs text-[var(--text-muted)]">Console v1.0.0</p>
          </div>
          <ThemeToggle compact />
        </div>

        <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
          {filteredTabs.map((t) => {
            const Icon = t.icon;
            const active = currentTab === t.id;
            if (t.section) {
              return (
                <div key={t.id}>
                  <div className="px-3 pt-4 pb-1 text-xs font-semibold text-[var(--text-muted)] uppercase tracking-wider">{t.section}</div>
                  <button
                    onClick={() => setCurrentTab(t.id)}
                    className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                      active ? "bg-[var(--bg-subtle)] text-[var(--text-primary)]" : "text-[var(--text-secondary)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <Icon size={18} />
                    <span>{t.label}</span>
                  </button>
                </div>
              );
            }
            return (
              <button
                key={t.id}
                onClick={() => setCurrentTab(t.id)}
                className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                  active ? "bg-[var(--bg-subtle)] text-[var(--text-primary)]" : "text-[var(--text-secondary)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text-primary)]"
                }`}
              >
                <Icon size={18} />
                <span>{t.label}</span>
              </button>
            );
          })}
        </nav>

        <div className="p-3 border-t border-[var(--border)]">
          <button onClick={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
            className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-[var(--text-secondary)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text-primary)]">
            <LogOut size={18} /> Logout
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-auto bg-[var(--bg-base)]">
        <div className="max-w-7xl mx-auto p-6">
          {renderContent()}
        </div>
      </main>
    </div>
  );
}
