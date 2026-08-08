import { useState, useEffect } from "react";
import { LayoutDashboard, Globe, Users, Shield, Zap, Activity, Settings, Server, Building, Mail, Monitor, HardDrive, HeartPulse, CreditCard, Keyboard, User, AtSign, BarChart, AlertTriangle, UserPlus, Send, LogOut, FileText, Bell } from "lucide-react";
import Dashboard from "./components/Dashboard";
import Domains from "./components/Domains";
import UsersPage from "./components/UsersPage";
import Firewall from "./components/Firewall";
import Modules from "./components/Modules";
import AuditLog from "./components/AuditLog";
import EnterpriseDashboard from "./components/EnterpriseDashboard";
import MailboxList from "./components/MailboxList";
import OrganizationList from "./components/OrganizationList";
import BackupStatus from "./components/BackupStatus";
import SystemHealth from "./components/SystemHealth";
import BillingPage from "./components/BillingPage";
import ApiKeysPage from "./components/ApiKeysPage";
import SignupPage from "./components/SignupPage";
import LoginPage from "./components/LoginPage";
import ForgotPasswordPage from "./components/ForgotPasswordPage";
import ResetPasswordPage from "./components/ResetPasswordPage";
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
import PlatformHome from "./components/PlatformHome";
import { initCSRF, api } from "./api";

type Tab = "dashboard" | "domains" | "users" | "firewall" | "modules" | "audit" | "settings"
  | "enterprise" | "mailboxes" | "organizations" | "backups" | "health" | "platform-home"
  | "billing" | "onboarding" | "apikeys"
  | "account-settings" | "org-overview" | "invitations" | "members-roles" | "ownership-transfer"
  | "suspension-deletion" | "customer-mailboxes" | "aliases" | "groups" | "usage-quotas"
  | "invoices" | "security" | "support" | "preferences"
  | "login" | "signup" | "forgot-password" | "reset-password";

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
//     /firewall/rules, /firewall/logs, /modules, /monitoring/health
//   tenant-owned:    /enterprise/* (dashboard, domains, mailboxes,
//     audit/logs, ...), /users (tenantCompatMW), /customer/*
//   self-scoped (safe for either portal): /account/*
//
// NOTE on "domains" (Domains.tsx -> /enterprise/domains): the pre-existing
// code showed this tab only to the "platform" side of its old ad hoc
// filter, but the handler (ListAdminDomains) calls auth.RequireTenantID
// and 403s a NULL-tenant caller — it is, and always was, tenant-owned.
// It is listed under ORGANIZATION_TAB_IDS here, matching the real backend
// authorization, and is correctly absent from PLATFORM_TAB_IDS.
const PLATFORM_TAB_IDS: Tab[] = [
  "platform-home", "organizations", "backups", "firewall", "modules", "health",
  "account-settings", "security", "preferences",
];
const ORGANIZATION_TAB_IDS: Tab[] = [
  "dashboard", "domains", "org-overview", "customer-mailboxes", "aliases", "groups", "usage-quotas",
  "invitations", "members-roles", "ownership-transfer", "suspension-deletion", "invoices",
  "billing", "apikeys", "account-settings", "security", "preferences", "support",
];

const tabs: { id: Tab; label: string; icon: typeof LayoutDashboard; section?: string }[] = [
  { id: "platform-home", label: "Overview", icon: LayoutDashboard },
  { id: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { id: "enterprise", label: "Enterprise", icon: Monitor, section: "Customer Admin" },
  { id: "mailboxes", label: "Mailboxes", icon: Mail },
  { id: "organizations", label: "Organizations", icon: Building },
  { id: "domains", label: "Domains", icon: Globe },
  { id: "users", label: "Users", icon: Users },
  { id: "firewall", label: "Firewall", icon: Shield },
  { id: "modules", label: "Modules", icon: Zap },
  { id: "audit", label: "Audit Log", icon: Activity },
  { id: "backups", label: "Backups", icon: HardDrive },
  { id: "health", label: "Health", icon: HeartPulse },
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
    fetch("/api/v1/me", { credentials: "include" })
      .then(async (r) => {
        // A non-ok /me response (401/403/5xx) means there is no valid
        // authenticated session: clear local auth state and fall back
        // to the login screen. No portal-specific request has been
        // issued yet at this point, so there is nothing else to unwind.
        setAuthenticated(r.ok);
        if (r.ok) {
          try {
            const u = await r.json();
            setUserRole(u.role || "");
            setUserEmail(u.email || "");
            // portal is the ONLY authoritative shell selector. Anything
            // other than the two known values fails closed to "" —
            // never inferred from `role`.
            const resolvedPortal = (u.portal === "platform" || u.portal === "organization") ? u.portal : "";
            setPortal(resolvedPortal);
            // Resolve the landing tab from the portal BEFORE authLoading
            // flips false, so the first real paint already shows the
            // correct shell — no flash of the wrong portal.
            setCurrentTab(resolvedPortal === "platform" ? "platform-home" : "dashboard");
            initCSRF().catch(() => {});
          } catch { setUserRole(""); setUserEmail(""); setPortal(""); }
        }
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
    };
    setCurrentTab(tabMap[route] || "dashboard");
  };

  const tabFromPath = (path: string): Tab => {
    if (path === "/admin" || path === "/admin/" || path === "/admin/login") return "login";
    if (path === "/admin/signup") return "signup";
    if (path === "/admin/forgot-password") return "forgot-password";
    if (path === "/admin/reset-password") return "reset-password";
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
    return <div className="h-screen bg-[#0C0E12] flex items-center justify-center"><p className="text-gray-400">Loading...</p></div>;
  }

  if (!authenticated) {
    switch (currentTab) {
      case "signup": return <SignupPage />;
      case "forgot-password": return <ForgotPasswordPage />;
      case "reset-password": return <ResetPasswordPage />;
      default: return <LoginPage />;
    }
  }

  // Fail-closed: an authenticated identity whose /me response did not
  // carry a recognized portal value gets NO business navigation and NO
  // shell — neither Platform Administration nor Customer Portal. This
  // must never fall back to inferring the shell from `role`.
  if (portal === "") {
    return (
      <div className="h-screen bg-[#0C0E12] flex items-center justify-center">
        <div className="text-center max-w-sm">
          <h1 className="text-lg font-semibold text-[#E8EAF0] mb-2">Access Unavailable</h1>
          <p className="text-sm text-[#8B92A8] mb-6">
            This account could not be authorized for the admin console. Contact your administrator.
          </p>
          <button
            onClick={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
            className="text-sm text-[#4F7CFF] hover:underline"
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
      return portal === "platform" ? <PlatformHome email={userEmail} onNavigate={setCurrentTab} /> : <Dashboard />;
    }
    switch (currentTab) {
      case "platform-home": return <PlatformHome email={userEmail} onNavigate={setCurrentTab} />;
      case "dashboard": return <Dashboard />;
      case "domains": return <Domains />;
      case "users": return <UsersPage />;
      case "firewall": return <Firewall />;
      case "modules": return <Modules />;
      case "audit": return <AuditLog />;
      case "enterprise": return <EnterpriseDashboard />;
      case "mailboxes": return <MailboxList />;
      case "organizations": return <OrganizationList />;
      case "backups": return <BackupStatus />;
      case "health": return <SystemHealth />;
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
      default: return portal === "platform" ? <PlatformHome email={userEmail} onNavigate={setCurrentTab} /> : <Dashboard />;
    }
  };

  return (
    <div className="flex h-screen overflow-hidden">
      <aside className="w-64 bg-[#13161C] border-r border-[#2A2F3E] flex flex-col">
        <div className="p-4 border-b border-[#2A2F3E] flex items-center gap-3">
          <Server size={24} className="text-[#4F7CFF]" />
          <div>
            <h1 className="text-sm font-semibold text-[#E8EAF0]">Orvix Admin</h1>
            <p className="text-xs text-[#555D73]">Console v1.0.0</p>
          </div>
        </div>

        <nav className="flex-1 px-2 py-3 space-y-0.5 overflow-y-auto">
          {filteredTabs.map((t) => {
            const Icon = t.icon;
            const active = currentTab === t.id;
            if (t.section) {
              return (
                <div key={t.id}>
                  <div className="px-3 pt-4 pb-1 text-xs font-semibold text-[#555D73] uppercase tracking-wider">{t.section}</div>
                  <button
                    onClick={() => setCurrentTab(t.id)}
                    className={`w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                      active ? "bg-[#222736] text-[#E8EAF0]" : "text-[#8B92A8] hover:bg-[#1A1E26] hover:text-[#E8EAF0]"
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
                  active ? "bg-[#222736] text-[#E8EAF0]" : "text-[#8B92A8] hover:bg-[#1A1E26] hover:text-[#E8EAF0]"
                }`}
              >
                <Icon size={18} />
                <span>{t.label}</span>
              </button>
            );
          })}
        </nav>

        <div className="p-3 border-t border-[#2A2F3E]">
          <button onClick={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
            className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-[#8B92A8] hover:bg-[#1A1E26] hover:text-[#E8EAF0]">
            <LogOut size={18} /> Logout
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-auto bg-[#0C0E12]">
        <div className="max-w-7xl mx-auto p-6">
          {renderContent()}
        </div>
      </main>
    </div>
  );
}
