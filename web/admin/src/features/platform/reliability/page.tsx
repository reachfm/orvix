import { useState } from "react";
import { HardDrive, RefreshCw, AlertCircle, Server, Boxes, Network } from "lucide-react";
import BackupsPanel from "./components/BackupsPanel";
import UpdatesPanel from "./components/UpdatesPanel";
import MonitoringPanel from "./components/MonitoringPanel";
import StoragePanel from "./components/StoragePanel";
import ClusterPanel from "./components/ClusterPanel";

type Tab = "backups" | "updates" | "monitoring" | "storage" | "cluster";

const TABS: { id: Tab; label: string; icon: typeof HardDrive }[] = [
  { id: "backups", label: "Backups", icon: HardDrive },
  { id: "updates", label: "Updates", icon: RefreshCw },
  { id: "monitoring", label: "Monitoring", icon: AlertCircle },
  { id: "storage", label: "Storage", icon: Boxes },
  { id: "cluster", label: "Cluster", icon: Network },
];

export default function ReliabilityPage() {
  const [tab, setTab] = useState<Tab>("backups");
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><Server size={22} className="text-[var(--accent)]" /> Reliability</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)]">
        {TABS.map((t) => {
          const Icon = t.icon;
          return (
            <button key={t.id} onClick={() => setTab(t.id)} className={`flex items-center gap-1.5 px-3 py-2 text-sm border-b-2 ${tab === t.id ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>
              <Icon size={14} /> {t.label}
            </button>
          );
        })}
      </div>
      {tab === "backups" && <BackupsPanel />}
      {tab === "updates" && <UpdatesPanel />}
      {tab === "monitoring" && <MonitoringPanel />}
      {tab === "storage" && <StoragePanel />}
      {tab === "cluster" && <ClusterPanel />}
    </div>
  );
}
