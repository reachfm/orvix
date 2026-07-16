import { useState, useEffect } from "react";
import { LayoutDashboard, Globe, Users, Shield, Zap, Activity, Settings, Server, Building, Mail, Monitor, Key, HardDrive, HeartPulse, CreditCard, Keyboard, User, AtSign, BarChart, AlertTriangle, UserPlus, Send, LogOut, FileText, Bell } from "lucide-react";
import Dashboard from "./components/Dashboard";
import Domains from "./components/Domains";
import UsersPage from "./components/UsersPage";
import Firewall from "./components/Firewall";
import Modules from "./components/Modules";
import AuditLog from "./components/AuditLog";
import EnterpriseDashboard from "./components/EnterpriseDashboard";
import MailboxList from "./components/MailboxList";
import OrganizationList from "./components/OrganizationList";
import LicenseStatus from "./components/LicenseStatus";
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
import SecurityPage from "./components/SecurityPage";
import SupportPage from "./components/SupportPage";
import PreferencesPage from "./components/PreferencesPage";

type Tab = "dashboard" | "domains" | "users" | "firewall" | "modules" | "audit" | "settings"
  | "enterprise" | "mailboxes" | "organizations" | "license" | "backups" | "health"
  | "billing" | "onboarding" | "apikeys"
  | "account-settings" | "org-overview" | "invitations" | "members-roles" | "ownership-transfer"
  | "suspension-deletion" | "customer-mailboxes" | "aliases" | "groups" | "usage-quotas"
  | "invoices" | "security" | "support" | "preferences"
  | "login" | "signup" | "forgot-password" | "reset-password";

const tabs: { id: Tab; label: string; icon: typeof LayoutDashboard; section?: string }[] = [
  { id: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { id: "enterprise", label: "Enterprise", icon: Monitor, section: "Customer Admin" },
  { id: "mailboxes", label: "Mailboxes", icon: Mail },
  { id: "organizations", label: "Organizations", icon: Building },
  { id: "domains", label: "Domains", icon: Globe },
  { id: "users", label: "Users", icon: Users },
  { id: "firewall", label: "Firewall", icon: Shield },
  { id: "modules", label: "Modules", icon: Zap },
  { id: "audit", label: "Audit Log", icon: Activity },
  { id: "license", label: "License", icon: Key, section: "System" },
  { id: "backups", label: "Backups", icon: HardDrive },
  { id: "health", label: "Health", icon: HeartPulse },
  { id: "settings", label: "Settings", icon: Settings },
  { id: "org-overview", label: "Organization", icon: Building, section: "Customer Portal" },
  { id: "customer-mailboxes", label: "Mailboxes", icon: Mail },
  { id: "aliases", label: "Aliases", icon: AtSign },
  { id: "groups", label: "Groups", icon: Users },
  { id: "usage-quotas", label: "Usage", icon: BarChart },
  { id: "onboarding", label: "Domain Setup", icon: Globe },
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

  useEffect(() => {
    fetch("/api/v1/me", { credentials: "include" })
      .then((r) => { setAuthenticated(r.ok); setAuthLoading(false); })
      .catch(() => { setAuthenticated(false); setAuthLoading(false); });
  }, []);

  const navigateTo = (route: string) => {
    const tabMap: Record<string, Tab> = {
      "/": "dashboard", "/login": "login", "/signup": "signup",
      "/forgot-password": "forgot-password", "/reset-password": "reset-password",
    };
    setCurrentTab(tabMap[route] || "dashboard");
  };

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

  const renderContent = () => {
    switch (currentTab) {
      case "dashboard": return <Dashboard />;
      case "domains": return <Domains />;
      case "users": return <UsersPage />;
      case "firewall": return <Firewall />;
      case "modules": return <Modules />;
      case "audit": return <AuditLog />;
      case "enterprise": return <EnterpriseDashboard />;
      case "mailboxes": return <MailboxList />;
      case "organizations": return <OrganizationList />;
      case "license": return <LicenseStatus />;
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
          {tabs.map((t) => {
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
          <button onClick={() => { fetch("/api/v1/auth/logout", { method: "POST", credentials: "include" }); setAuthenticated(false); }}
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
