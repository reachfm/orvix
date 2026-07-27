import { useEffect, useState } from "react";
import {
  Activity, AlertTriangle, AtSign, BarChart2, Bell, BookOpen,
  Building, CreditCard, Database, FileText, Globe, HardDrive,
  HeartPulse, Keyboard, LayoutDashboard, Lock, Mail, Monitor,
  Package, RefreshCw, Send, Server, Settings, Shield,
  ShieldCheck, Terminal, TrendingUp, User, UserPlus, Users,
  Wifi, Workflow, Zap,
} from "lucide-react";
import AccountSettingsPage from "./components/AccountSettingsPage";
import AliasesPage from "./components/AliasesPage";
import AuditLog from "./components/AuditLog";
import BackupStatus from "./components/BackupStatus";
import BillingPage from "./components/BillingPage";
import CustomerMailboxesPage from "./components/CustomerMailboxesPage";
import Dashboard from "./components/Dashboard";
import DomainOnboarding from "./components/DomainOnboarding";
import Domains from "./components/Domains";
import EnterpriseDashboard from "./components/EnterpriseDashboard";
import ForgotPasswordPage from "./components/ForgotPasswordPage";
import GroupsPage from "./components/GroupsPage";
import InvitationsPage from "./components/InvitationsPage";
import InvoicesPage from "./components/InvoicesPage";
import ApiKeysPage from "./components/ApiKeysPage";
import LoginPage from "./components/LoginPage";
import MailboxList from "./components/MailboxList";
import MailQueuePage from "./components/MailQueuePage";
import MembersRolesPage from "./components/MembersRolesPage";
import Modules from "./components/Modules";
import OrganizationOverviewPage from "./components/OrganizationOverviewPage";
import Organizations from "./components/Organizations";
import OwnershipTransferPage from "./components/OwnershipTransferPage";
import PreferencesPage from "./components/PreferencesPage";
import ResetPasswordPage from "./components/ResetPasswordPage";
import SecurityPage from "./components/SecurityPage";
import SignupPage from "./components/SignupPage";
import SupportPage from "./components/SupportPage";
import SuspensionDeletionPage from "./components/SuspensionDeletionPage";
import SystemHealth from "./components/SystemHealth";
import UsageQuotasPage from "./components/UsageQuotasPage";
import UsersPage from "./components/UsersPage";
import AppShell from "./components/layout/AppShell";
import { ToastProvider } from "./components/ui/Toast";
import { initCSRF, api } from "./api";
import type { NavSection } from "./types/navigation";

// Lazy-load heavy new pages (graceful fallback if not yet created by OpenCode)
import { lazy, Suspense } from "react";
const PortsServicesPage = lazy(() =>
  import("./components/PortsServicesPage").catch(() => ({ default: () => <PlaceholderPage title="Ports & Services" /> }))
);
const PackagesAdminPage = lazy(() =>
  import("./components/PackagesAdminPage").catch(() => ({ default: () => <PlaceholderPage title="Packages & Plans" /> }))
);
const WorkflowPage = lazy(() =>
  import("./components/WorkflowPage").catch(() => ({ default: () => <PlaceholderPage title="Workflow & Automation" /> }))
);
const CompliancePage = lazy(() =>
  import("./components/CompliancePage").catch(() => ({ default: () => <PlaceholderPage title="Compliance" /> }))
);
const ReportsPage = lazy(() =>
  import("./components/ReportsPage").catch(() => ({ default: () => <PlaceholderPage title="Reports & Analytics" /> }))
);
const AntiSpamPage = lazy(() =>
  import("./components/AntiSpamPage").catch(() => ({ default: () => <PlaceholderPage title="Anti-Spam" /> }))
);
const TLSCertsPage = lazy(() =>
  import("./components/TLSCertsPage").catch(() => ({ default: () => <PlaceholderPage title="TLS Certificates" /> }))
);
const LogsPage = lazy(() =>
  import("./components/LogsPage").catch(() => ({ default: () => <PlaceholderPage title="System Logs" /> }))
);
const CompanyPage = lazy(() =>
  import("./components/CompanyPage").catch(() => ({ default: () => <PlaceholderPage title="Company" /> }))
);

function PageSpinner() {
  return (
    <div className="flex h-64 items-center justify-center">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-[var(--border)] border-t-[var(--accent)]" />
    </div>
  );
}

function PlaceholderPage({ title }: { title: string }) {
  return (
    <div className="flex flex-col items-center justify-center gap-4 py-24 text-center">
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] px-8 py-8">
        <RefreshCw size={32} className="mx-auto mb-4 animate-spin text-[var(--accent)]" />
        <p className="text-lg font-semibold text-[var(--text-primary)]">{title}</p>
        <p className="mt-2 text-sm text-[var(--text-secondary)]">
          Building this page — it will appear shortly.
        </p>
      </div>
    </div>
  );
}

type Tab = string;

const tabConfig: {
  id: Tab;
  label: string;
  icon: typeof LayoutDashboard;
  section: string;
  description: string;
  platformOnly?: boolean;
  badge?: string;
}[] = [
  // ── Command ──────────────────────────────────────────────────────────────
  { id: "dashboard", label: "Dashboard", icon: LayoutDashboard, section: "Command", description: "Platform overview — tenants, mail flow, services", platformOnly: true },

  // ── Platform ─────────────────────────────────────────────────────────────
  { id: "enterprise", label: "Enterprise", icon: Monitor, section: "Platform", description: "Platform-wide customer operations", platformOnly: true },
  { id: "organizations", label: "Organizations", icon: Building, section: "Platform", description: "Tenant inventory and ownership", platformOnly: true },

  // ── Mail Operations ───────────────────────────────────────────────────────
  { id: "domains", label: "Domains", icon: Globe, section: "Mail Operations", description: "DNS readiness and domain health", platformOnly: true },
  { id: "mailboxes", label: "Mailboxes", icon: Mail, section: "Mail Operations", description: "Mailbox lifecycle and quota state", platformOnly: true },
  { id: "users", label: "Users", icon: Users, section: "Mail Operations", description: "Platform users and access control", platformOnly: true },

  // ── Mail Services ─────────────────────────────────────────────────────────
  { id: "mail-queue", label: "SMTP Queue", icon: Send, section: "Mail Services", description: "View and manage SMTP delivery queue", platformOnly: true },
  { id: "anti-spam", label: "Anti-Spam", icon: ShieldCheck, section: "Mail Services", description: "Spam filtering, blacklists, whitelists", platformOnly: true },
  { id: "tls-certs", label: "TLS Certificates", icon: Lock, section: "Mail Services", description: "SSL/TLS certificate status and renewal", platformOnly: true },
  { id: "workflow", label: "Workflow & Auto", icon: Workflow, section: "Mail Services", description: "Routing rules, webhooks, templates", platformOnly: true },

  // ── Packages ──────────────────────────────────────────────────────────────
  { id: "packages", label: "Plans & Packages", icon: Package, section: "Packages", description: "Create and manage subscription plans", platformOnly: true },

  // ── Infrastructure ────────────────────────────────────────────────────────
  { id: "ports-services", label: "Ports & Services", icon: Wifi, section: "Infrastructure", description: "Live port and service monitoring", platformOnly: true },
  { id: "health", label: "System Health", icon: HeartPulse, section: "Infrastructure", description: "Runtime health and capacity", platformOnly: true },
  { id: "backups", label: "Backups", icon: HardDrive, section: "Infrastructure", description: "Backup and restore readiness", platformOnly: true },
  { id: "settings", label: "Settings", icon: Settings, section: "Infrastructure", description: "Global platform configuration", platformOnly: true },
  { id: "logs", label: "System Logs", icon: Terminal, section: "Infrastructure", description: "Platform logs with search and filter", platformOnly: true },

  // ── Security ──────────────────────────────────────────────────────────────
  { id: "firewall", label: "Firewall", icon: Shield, section: "Security", description: "Network access controls and IP rules", platformOnly: true },
  { id: "modules", label: "Modules", icon: Zap, section: "Security", description: "Runtime module posture", platformOnly: true },
  { id: "audit", label: "Audit Log", icon: Activity, section: "Security", description: "Operator and tenant audit trail", platformOnly: true },

  // ── Compliance ────────────────────────────────────────────────────────────
  { id: "compliance", label: "Compliance", icon: BookOpen, section: "Compliance", description: "Retention, legal holds, DLP, eDiscovery", platformOnly: true },

  // ── Reports ───────────────────────────────────────────────────────────────
  { id: "reports", label: "Reports", icon: TrendingUp, section: "Reports", description: "Email traffic, delivery, and tenant analytics", platformOnly: true },

  // ── Company ───────────────────────────────────────────────────────────────
  { id: "company", label: "Company", icon: Building, section: "Company", description: "Company profile, departments, branding", platformOnly: true },

  // ── Customer Portal ───────────────────────────────────────────────────────
  { id: "org-overview", label: "Organization", icon: Building, section: "Customer Portal", description: "Tenant profile and membership" },
  { id: "customer-mailboxes", label: "Mailboxes", icon: Mail, section: "Customer Portal", description: "Tenant mailbox inventory" },
  { id: "aliases", label: "Aliases", icon: AtSign, section: "Customer Portal", description: "Tenant alias management" },
  { id: "groups", label: "Groups", icon: Users, section: "Customer Portal", description: "Group addresses and members" },
  { id: "usage-quotas", label: "Usage", icon: BarChart2, section: "Customer Portal", description: "Quota consumption and limits" },
  { id: "onboarding", label: "Domain Setup", icon: Globe, section: "Customer Portal", description: "Domain onboarding workflow" },
  { id: "invitations", label: "Invitations", icon: UserPlus, section: "Customer Portal", description: "Invite and revoke members" },
  { id: "members-roles", label: "Members", icon: Shield, section: "Customer Portal", description: "Tenant roles and membership" },
  { id: "ownership-transfer", label: "Ownership", icon: Send, section: "Customer Portal", description: "Tenant ownership transfer" },
  { id: "suspension-deletion", label: "Status", icon: AlertTriangle, section: "Customer Portal", description: "Suspension and deletion state" },

  // ── Billing ───────────────────────────────────────────────────────────────
  { id: "invoices", label: "Invoices", icon: FileText, section: "Billing", description: "Invoice history" },
  { id: "billing", label: "Billing", icon: CreditCard, section: "Billing", description: "Subscription and plan controls" },

  // ── Integrations ──────────────────────────────────────────────────────────
  { id: "apikeys", label: "API Keys", icon: Keyboard, section: "Integrations", description: "Scoped API access credentials" },

  // ── Account ───────────────────────────────────────────────────────────────
  { id: "account-settings", label: "Account", icon: User, section: "Account", description: "Profile and password" },
  { id: "security", label: "Security", icon: Shield, section: "Account", description: "Sessions and MFA" },
  { id: "preferences", label: "Preferences", icon: Bell, section: "Account", description: "Notification preferences" },
  { id: "support", label: "Support", icon: HeartPulse, section: "Account", description: "Support requests" },
];

const PLATFORM_SECTION_ORDER = [
  "Command", "Platform", "Mail Operations", "Mail Services",
  "Packages", "Infrastructure", "Security", "Compliance", "Reports", "Company",
  "Customer Portal", "Billing", "Integrations", "Account",
];

const TENANT_SECTION_ORDER = [
  "Customer Portal", "Billing", "Integrations", "Account",
];

const arabicLabels: Record<string, string> = {
  dashboard: "لوحة القيادة",
  enterprise: "المؤسسة",
  organizations: "المنظمات",
  domains: "النطاقات",
  mailboxes: "صناديق البريد",
  users: "المستخدمون",
  firewall: "الجدار الناري",
  modules: "الوحدات",
  audit: "سجل التدقيق",
  health: "الصحة",
  backups: "النسخ الاحتياطي",
  settings: "الإعدادات",
  billing: "الفوترة",
  apikeys: "مفاتيح API",
  security: "الأمان",
  support: "الدعم",
  "mail-queue": "قائمة انتظار البريد",
  "anti-spam": "مكافحة البريد المزعج",
  "tls-certs": "شهادات TLS",
  workflow: "سير العمل",
  packages: "الباقات والخطط",
  "ports-services": "المنافذ والخدمات",
  compliance: "الامتثال",
  reports: "التقارير",
  company: "الشركة",
  logs: "السجلات",
};

export default function App() {
  const [currentTab, setCurrentTab] = useState<Tab>("dashboard");
  const [authenticated, setAuthenticated] = useState(false);
  const [authLoading, setAuthLoading] = useState(true);
  const [userRole, setUserRole] = useState("");
  const [userEmail, setUserEmail] = useState("");

  useEffect(() => {
    fetch("/api/v1/me", { credentials: "include" })
      .then(async (r) => {
        setAuthenticated(r.ok);
        if (r.ok) {
          try {
            const u = await r.json();
            setUserRole(u.role || "");
            setUserEmail(u.email || u.name || "");
            initCSRF().catch(() => {});
          } catch { setUserRole(""); }
        }
        setAuthLoading(false);
      })
      .catch(() => { setAuthenticated(false); setAuthLoading(false); });
  }, []);

  const isPlatformRole = userRole === "admin" || userRole === "superadmin" || userRole === "operator";

  const filteredTabs = tabConfig.filter((t) => {
    if (isPlatformRole) return true;
    if (t.platformOnly) return false;
    return true;
  });

  const sectionOrder = isPlatformRole ? PLATFORM_SECTION_ORDER : TENANT_SECTION_ORDER;

  const navSections: NavSection[] = sectionOrder
    .map((section) => ({
      label: section,
      items: filteredTabs
        .filter((t) => t.section === section)
        .map((t) => ({
          id: t.id,
          label: t.label,
          icon: t.icon,
          description: t.description,
          badge: t.badge,
        })),
    }))
    .filter((s) => s.items.length > 0);

  const activeTabConfig = tabConfig.find((t) => t.id === currentTab) || tabConfig[0];

  const renderContent = () => {
    switch (currentTab) {
      // ── Command ─────────────────────────────────────────────────────────
      case "dashboard": return <Dashboard />;

      // ── Platform ─────────────────────────────────────────────────────────
      case "enterprise": return <EnterpriseDashboard />;
      case "organizations": return <Organizations />;

      // ── Mail Operations ──────────────────────────────────────────────────
      case "domains": return <Domains />;
      case "mailboxes": return <MailboxList />;
      case "users": return <UsersPage />;

      // ── Mail Services ────────────────────────────────────────────────────
      case "mail-queue": return <MailQueuePage />;
      case "anti-spam": return <Suspense fallback={<PageSpinner />}><AntiSpamPage /></Suspense>;
      case "tls-certs": return <Suspense fallback={<PageSpinner />}><TLSCertsPage /></Suspense>;
      case "workflow": return <Suspense fallback={<PageSpinner />}><WorkflowPage /></Suspense>;

      // ── Packages ─────────────────────────────────────────────────────────
      case "packages": return <Suspense fallback={<PageSpinner />}><PackagesAdminPage /></Suspense>;

      // ── Infrastructure ───────────────────────────────────────────────────
      case "ports-services": return <Suspense fallback={<PageSpinner />}><PortsServicesPage /></Suspense>;
      case "health": return <SystemHealth />;
      case "backups": return <BackupStatus />;
      case "settings": return <div className="p-6 text-[var(--text-secondary)]">Settings coming soon.</div>;
      case "logs": return <Suspense fallback={<PageSpinner />}><LogsPage /></Suspense>;

      // ── Security ─────────────────────────────────────────────────────────
      case "firewall": return <SecurityPage />;
      case "modules": return <Modules />;
      case "audit": return <AuditLog />;

      // ── Compliance ───────────────────────────────────────────────────────
      case "compliance": return <Suspense fallback={<PageSpinner />}><CompliancePage /></Suspense>;

      // ── Reports ──────────────────────────────────────────────────────────
      case "reports": return <Suspense fallback={<PageSpinner />}><ReportsPage /></Suspense>;

      // ── Company ──────────────────────────────────────────────────────────
      case "company": return <Suspense fallback={<PageSpinner />}><CompanyPage /></Suspense>;

      // ── Customer Portal ──────────────────────────────────────────────────
      case "org-overview": return <OrganizationOverviewPage />;
      case "customer-mailboxes": return <CustomerMailboxesPage />;
      case "aliases": return <AliasesPage />;
      case "groups": return <GroupsPage />;
      case "usage-quotas": return <UsageQuotasPage />;
      case "onboarding": return <DomainOnboarding />;
      case "invitations": return <InvitationsPage />;
      case "members-roles": return <MembersRolesPage />;
      case "ownership-transfer": return <OwnershipTransferPage />;
      case "suspension-deletion": return <SuspensionDeletionPage />;

      // ── Billing ──────────────────────────────────────────────────────────
      case "invoices": return <InvoicesPage />;
      case "billing": return <BillingPage />;

      // ── Integrations ─────────────────────────────────────────────────────
      case "apikeys": return <ApiKeysPage />;

      // ── Account ──────────────────────────────────────────────────────────
      case "account-settings": return <AccountSettingsPage />;
      case "security": return <SecurityPage />;
      case "support": return <SupportPage />;
      case "preferences": return <PreferencesPage />;

      default: return <Dashboard />;
    }
  };

  if (authLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-[var(--bg-base)]">
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-5 py-4 text-sm text-[var(--text-secondary)]">
          Loading secure console…
        </div>
      </div>
    );
  }

  if (!authenticated) {
    switch (currentTab) {
      case "signup": return <SignupPage />;
      case "forgot-password": return <ForgotPasswordPage />;
      case "reset-password": return <ResetPasswordPage />;
      default: return <LoginPage />;
    }
  }

  return (
    <ToastProvider>
      <AppShell
        sections={navSections}
        activeTab={currentTab}
        onNavigate={(id) => setCurrentTab(id)}
        userEmail={userEmail}
        userRole={userRole}
        onLogout={() => { api.logout().catch(() => {}); setAuthenticated(false); }}
        arabicLabels={arabicLabels}
        headerTitle={undefined}
        headerSubtitle={activeTabConfig.description}
      >
        {renderContent()}
      </AppShell>
    </ToastProvider>
  );
}
