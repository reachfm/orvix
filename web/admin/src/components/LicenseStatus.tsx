import { useState, useEffect } from "react";
import { Key, Calendar, Globe, Shield, AlertCircle, Loader2 } from "lucide-react";

interface LicenseData {
  edition: string;
  expiresAt: string;
  domainsCount: number;
  maxDomains: number;
  features: string[];
  isDevKey: boolean;
  licensee: string;
}

export default function LicenseStatus() {
  const [data, setData] = useState<LicenseData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    fetch("/api/v1/license")
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.json();
      })
      .then((json) => {
        setData(json);
        setLoading(false);
      })
      .catch((err) => {
        setError(err.message);
        setLoading(false);
      });
  }, []);

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 size={24} className="text-[#4F7CFF] animate-spin" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="bg-[#13161C] border border-[#F87171]/30 rounded-xl p-6 flex items-center gap-3">
        <AlertCircle size={20} className="text-[#F87171]" />
        <span className="text-[#F87171] text-sm">Failed to load license: {error}</span>
      </div>
    );
  }

  if (!data) return null;

  const expires = new Date(data.expiresAt);
  const isExpired = expires < new Date();
  const isValid = !isExpired && data.features.length > 0;

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">License</h2>

      <div className="grid grid-cols-3 gap-4 mb-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <div className="flex items-center gap-2 mb-2">
            <Key size={16} className="text-[#4F7CFF]" />
            <span className="text-xs text-[#8B92A8]">Edition</span>
          </div>
          <p className="text-lg font-bold text-[#E8EAF0]">{data.edition || "Standard"}</p>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <div className="flex items-center gap-2 mb-2">
            <Calendar size={16} className="text-[#34D399]" />
            <span className="text-xs text-[#8B92A8]">Expiration</span>
          </div>
          <p className={`text-lg font-bold ${isExpired ? "text-[#F87171]" : "text-[#E8EAF0]"}`}>
            {data.expiresAt ? expires.toLocaleDateString() : "No expiration"}
          </p>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <div className="flex items-center gap-2 mb-2">
            <Globe size={16} className="text-[#FBBF24]" />
            <span className="text-xs text-[#8B92A8]">Domains</span>
          </div>
          <p className="text-lg font-bold text-[#E8EAF0]">
            {data.domainsCount ?? 0} / {data.maxDomains ?? "Unlimited"}
          </p>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Status</h3>
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Licensee</span>
              <span className="text-sm text-[#E8EAF0]">{data.licensee || "—"}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Status</span>
              <span
                className={`px-2 py-1 text-xs rounded-full ${
                  isValid
                    ? "bg-[#34D399]/10 text-[#34D399]"
                    : "bg-[#F87171]/10 text-[#F87171]"
                }`}
              >
                {isValid ? "Valid" : "Invalid / Expired"}
              </span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-sm text-[#8B92A8]">Dev Key</span>
              <span className="text-sm text-[#E8EAF0]">{data.isDevKey ? "Yes" : "No"}</span>
            </div>
          </div>
        </div>

        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4 flex items-center gap-2">
            <Shield size={14} className="text-[#4F7CFF]" />
            Features
          </h3>
          {data.features && data.features.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {data.features.map((f) => (
                <span
                  key={f}
                  className="px-2 py-1 text-xs rounded bg-[#4F7CFF]/10 text-[#4F7CFF]"
                >
                  {f}
                </span>
              ))}
            </div>
          ) : (
            <p className="text-sm text-[#555D73]">No features listed</p>
          )}
        </div>
      </div>
    </div>
  );
}
