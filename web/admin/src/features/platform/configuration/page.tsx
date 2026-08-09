import { useState } from "react";
import { Settings, Flag } from "lucide-react";
import SettingsPanel from "./components/SettingsPanel";
import FeatureFlagsPanel from "./components/FeatureFlagsPanel";

type Tab = "settings" | "feature-flags";

export default function ConfigurationPage() {
  const [tab, setTab] = useState<Tab>("settings");
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><Settings size={22} className="text-[var(--accent)]" /> Configuration</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)]">
        <button onClick={() => setTab("settings")} className={`px-3 py-2 text-sm border-b-2 ${tab === "settings" ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>Settings</button>
        <button onClick={() => setTab("feature-flags")} className={`flex items-center gap-1 px-3 py-2 text-sm border-b-2 ${tab === "feature-flags" ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}><Flag size={12} /> Feature Flags</button>
      </div>
      {tab === "settings" ? <SettingsPanel /> : <FeatureFlagsPanel />}
    </div>
  );
}
