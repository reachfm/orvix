import { useState } from "react";
import { ShieldAlert } from "lucide-react";
import AuditPanel from "./components/AuditPanel";
import SslPanel from "./components/SslPanel";
import AntivirusPanel from "./components/AntivirusPanel";
import FirewallPanel from "./components/FirewallPanel";
import GuardianPanel from "./components/GuardianPanel";
import SelfHealPanel from "./components/SelfHealPanel";
import LogRulesPanel from "./components/LogRulesPanel";

type Tab = "audit" | "ssl" | "antivirus" | "firewall" | "guardian" | "self-heal" | "log-rules";

const TABS: { id: Tab; label: string }[] = [
  { id: "audit", label: "Audit Log" },
  { id: "ssl", label: "SSL / ACME" },
  { id: "antivirus", label: "Antivirus" },
  { id: "firewall", label: "Firewall" },
  { id: "guardian", label: "Guardian" },
  { id: "self-heal", label: "Self-Heal" },
  { id: "log-rules", label: "Log Rules" },
];

export default function SecurityPage() {
  const [tab, setTab] = useState<Tab>("audit");
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><ShieldAlert size={22} className="text-[var(--accent)]" /> Security</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)] flex-wrap">
        {TABS.map((t) => (
          <button key={t.id} onClick={() => setTab(t.id)} className={`px-3 py-2 text-sm border-b-2 ${tab === t.id ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>
            {t.label}
          </button>
        ))}
      </div>
      {tab === "audit" && <AuditPanel />}
      {tab === "ssl" && <SslPanel />}
      {tab === "antivirus" && <AntivirusPanel />}
      {tab === "firewall" && <FirewallPanel />}
      {tab === "guardian" && <GuardianPanel />}
      {tab === "self-heal" && <SelfHealPanel />}
      {tab === "log-rules" && <LogRulesPanel />}
    </div>
  );
}
