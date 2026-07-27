import { useState, useEffect } from "react";
import {
  LayoutDashboard, Globe, Users, Shield, Zap, Activity, Settings,
  Server, Building, Mail, Monitor, HardDrive, HeartPulse, CreditCard,
  Keyboard, User, AtSign, BarChart, AlertTriangle, UserPlus, Send,
  FileText, Bell,
} from "lucide-react";
import Dashboard from "./components/Dashboard";
import Domains from "./components/Domains";
import UsersPage from "./components/UsersPage";
import SecurityPage from "./components/SecurityPage";
import Modules from "./components/Modules";
import AuditLog from "./components/AuditLog";
import EnterpriseDashboard from "./components/EnterpriseDashboard";
import MailboxList from "./components/MailboxList";
import Organizations from "./components/Organizations";
import BackupStatus from "./components/BackupStatus";
import SystemHealth from "./components/SystemHealth";
import BillingPage from "./components/BillingPage";
import DomainOnboarding from "./components/DomainOnboarding";
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
import SupportPage from "./components/SupportPage";
import PreferencesPage from "./components/PreferencesPage";
import { initCSRF, api } from "./api";
import AppShell from "./components/layout/AppShell";
import { ToastProvider } from "./components/ui/Toast";
import type { NavItem, NavSection } from "./types/navigation";

type Tab = string;

const tabConfig: { id: Tab; label: string; icon: typeof LayoutDashboard; section: string; description: string }[] = [
  { id: "dashboard", label: "Dashboard", icon: LayoutDashboard, section: "Command", description: "Tenant health, domains, mailbox posture" },
  { id: "enterprise", label: "Enterprise", icon: Monitor, section: "Platform", description: "Platform-wide customer operations" },
  { id: "organizations", label: "Organizations", icon: Building, section: "Platform", description: "Tenant inventory and ownership" },
  { id: "domains", label: "Domains", icon: Globe, section: "Mail Operations", description: "DNS readiness and domain status" },
  { id: "mailboxes", label: "Mailboxes", icon: Mail, section: "Mail Operations", description: "Mailbox lifecycle and quota state" },
  { id: "users", label: "Users", icon: Users, section: "Mail Operations", description: "Platform users and access" },
  { id: "firewall", label: "Firewall", icon: Shield, section: "Security", description: "Network access controls" },
  { id: "modules", label: "Modules", icon: Zap, section: "Security", description: "Runtime module posture" },
  { id: "audit", label: "Audit Log", icon: Activity, section: "Security", description: "Operator and tenant audit trail" },
  { id: "health", label: "Health", icon: HeartPulse, section: "Operations", description: "Runtime health and capacity" },
  { id: "backups", label: "Backups", icon: HardDrive, section: "Operations", description: "Backup and restore readiness" },
  { id: "settings", label: "Settings", icon: Settings, section: "Operations", description: "Global platform configuration" },
  { id: "org-overview", label: "Organization", icon: Building, section: "Customer Portal", description: "Tenant profile and membership" },
  { id: "customer-mailboxes", label: "Mailboxes", icon: Mail, section: "Customer Portal", description: "Tenant mailbox inventory" },
  { id: "aliases", label: "Aliases", icon: AtSign, section: "Customer Portal", description: "Tenant alias management" },
  { id: "groups", label: "Groups", icon: Users, section: "Customer Portal", description: "Group addresses and members" },
  { id: "usage-quotas", label: "Usage", icon: BarChart, section: "Customer Portal", description: "Quota consumption and limits" },
  { id: "onboarding", label: "Domain Setup", icon: Globe, section: "Customer Portal", description: "Domain onboarding workflow" },
  { id: "invitations", label: "Invitations", icon: UserPlus, section: "Customer Portal", description: "Invite and revoke members" },
  { id: "members-roles", label: "Members", icon: Shield, section: "Customer Portal", description: "Tenant roles and membership" },
  { id: "ownership-transfer", label: "Ownership", icon: Send, section: "Customer Portal", description: "Tenant ownership transfer" },
  { id: "suspension-deletion", label: "Status", icon: AlertTriangle, section: "Customer Portal", description: "Suspension and deletion state" },
  { id: "invoices", label: "Invoices", icon: FileText, section: "Billing", description: "Invoice history" },
  { id: "billing", label: "Billing", icon: CreditCard, section: "Billing", description: "Subscription and plan controls" },
  { id: "apikeys", label: "API Keys", icon: Keyboard, section: "Integrations", description: "Scoped API access" },
  { id: "account-settings", label: "Account", icon: User, section: "Account", description: "Profile and password" },
  { id: "security", label: "Security", icon: Shield, section: "Account", description: "Sessions and MFA" },
  { id: "preferences", label: "Preferences", icon: Bell, section: "Account", description: "Notification preferences" },
  { id: "support", label: "Support", icon: HeartPulse, section: "Account", description: "Support requests" },
];

const sectionOrder = ["Command", "Platform", "Mail Operations", "Security", "Operations", "Customer Portal", "Billing", "Integrations", "Account"];

const arabicLabels: Record<string, string> = {
  dashboard: "لوحة القيادة", enterprise: "المؤسسة", organizations: "المنظمات",
  domains: "النطاقات", mailboxes: "صناديق البريد", users: "المستخدمون",
  firewall: "الجدار الناري", modules: "الوحدات", audit: "سجل التدقيق",
  health: "الصحة", backups: "النسخ الاحتياطي", settings: "الإعدادات",
  billing: "الفوترة", apikeys: "مفاتيح API", security: "الأمان", support: "الدعم",
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
    if (t.id === "dashboard") return true;
    if (["enterprise","mailboxes","organizations","domains","users","firewall","modules","audit","backups","health","settings"].includes(t.id)) return false;
    return true;
  });

  const navSections: NavSection[] = sectionOrder
    .map((section) => ({
      label: section,
      items: filteredTabs.filter((t) => t.section === section).map((t) => ({
        id: t.id,
        label: t.label,
        icon: t.icon,
        description: t.description,
      })),
    }))
    .filter((s) => s.items.length > 0);

  const activeTabConfig = tabConfig.find((t) => t.id === currentTab) || tabConfig[0];
  const headerTitle = currentTab === "dashboard" ? "Dashboard" : (arabicLabels[currentTab] || activeTabConfig.label);

  const renderContent = () => {
    switch (currentTab) {
      case "dashboard": return <Dashboard />;
      case "domains": return <Domains />;
      case "users": return <UsersPage />;
      case "firewall": return <SecurityPage />;
      case "modules": return <Modules />;
      case "audit": return <AuditLog />;
      case "enterprise": return <EnterpriseDashboard />;
      case "mailboxes": return <MailboxList />;
      case "organizations": return <Organizations />;
      case "backups": return <BackupStatus />;
      case "health": return <SystemHealth />;
      case "billing": return <BillingPage />;
      case "onboarding": return <DomainOnboarding />;
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
      default: return <Dashboard />;
    }
  };

  if (authLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-[var(--bg-base)]">
        <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-5 py-4 text-sm text-[var(--text-secondary)]">
          Loading secure console...
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
        headerTitle={activeTabConfig.description ? undefined : headerTitle}
        headerSubtitle={activeTabConfig.description}
      >
        {renderContent()}
      </AppShell>
    </ToastProvider>
  );
}
