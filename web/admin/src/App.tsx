import { useState } from "react";
import { LayoutDashboard, Globe, Users, Shield, Zap, Activity, Settings, Server, Building, Mail, Monitor, Key, HardDrive, HeartPulse } from "lucide-react";
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

type Tab = "dashboard" | "domains" | "users" | "firewall" | "modules" | "audit" | "settings" | "enterprise" | "mailboxes" | "organizations" | "license" | "backups" | "health";

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
];

export default function App() {
  const [currentTab, setCurrentTab] = useState<Tab>("dashboard");

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
      </aside>

      <main className="flex-1 overflow-auto bg-[#0C0E12]">
        <div className="max-w-7xl mx-auto p-6">
          {renderContent()}
        </div>
      </main>
    </div>
  );
}
