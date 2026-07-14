import { useEffect, useState } from "react";
import { AlertCircle, Calendar, Key, Loader2, Shield } from "lucide-react";

interface LicenseData {
  status: "offline" | "public_key_missing" | "license_missing" | "expired" | "valid";
  tier?: string;
  expires_at?: string;
  customer_id?: string;
  reason?: string;
  warnings?: string[];
}

export default function LicenseStatus() {
  const [data, setData] = useState<LicenseData | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/v1/license")
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then(setData)
      .catch((err: Error) => setError(err.message));
  }, []);

  if (!data && !error) return <div className="flex h-64 items-center justify-center"><Loader2 size={24} className="animate-spin text-[#4F7CFF]" /></div>;
  if (error) return <div className="flex items-center gap-3 rounded-lg border border-[#F87171]/30 bg-[#13161C] p-6 text-sm text-[#F87171]"><AlertCircle size={20} />Failed to load license: {error}</div>;
  if (!data) return null;

  const valid = data.status === "valid";
  const expires = data.expires_at ? new Date(data.expires_at) : null;
  const statusClass = valid ? "bg-[#34D399]/10 text-[#34D399]" : "bg-[#F87171]/10 text-[#F87171]";

  return <div>
    <h2 className="mb-6 text-2xl font-semibold text-[#E8EAF0]">License</h2>
    <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-3">
      <Summary icon={Key} label="Tier" value={data.tier || "Unavailable"} />
      <Summary icon={Calendar} label="Expiration" value={expires ? expires.toLocaleDateString() : "Unavailable"} />
      <Summary icon={Shield} label="Validation" value={data.status} />
    </div>
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
      <section className="rounded-lg border border-[#2A2F3E] bg-[#13161C] p-6">
        <h3 className="mb-4 text-sm font-medium text-[#E8EAF0]">Status</h3>
        <Row label="Customer" value={data.customer_id || "Unavailable"} />
        <div className="mt-3 flex items-center justify-between"><span className="text-sm text-[#8B92A8]">State</span><span className={`rounded px-2 py-1 text-xs ${statusClass}`}>{data.status}</span></div>
        <div className="mt-3"><span className="text-sm text-[#8B92A8]">Reason</span><p className="mt-1 text-sm text-[#E8EAF0]">{data.reason || "None"}</p></div>
      </section>
      <section className="rounded-lg border border-[#2A2F3E] bg-[#13161C] p-6">
        <h3 className="mb-4 text-sm font-medium text-[#E8EAF0]">Warnings</h3>
        {data.warnings?.length ? <ul className="space-y-2 text-sm text-[#FBBF24]">{data.warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul> : <p className="text-sm text-[#555D73]">No license warnings</p>}
      </section>
    </div>
  </div>;
}

function Summary({ icon: Icon, label, value }: { icon: typeof Key; label: string; value: string }) {
  return <div className="rounded-lg border border-[#2A2F3E] bg-[#13161C] p-4"><div className="mb-2 flex items-center gap-2"><Icon size={16} className="text-[#4F7CFF]" /><span className="text-xs text-[#8B92A8]">{label}</span></div><p className="text-lg font-bold text-[#E8EAF0]">{value}</p></div>;
}

function Row({ label, value }: { label: string; value: string }) {
  return <div className="flex items-center justify-between"><span className="text-sm text-[#8B92A8]">{label}</span><span className="text-sm text-[#E8EAF0]">{value}</span></div>;
}
