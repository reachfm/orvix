import { useState, useEffect } from "react";
import { Building, Search, AlertTriangle, Ban, CheckCircle, X, RefreshCw } from "lucide-react";
import { api } from "../api";

interface Tenant {
  id: number;
  name: string;
  slug: string;
  domain: string;
  plan: string;
  active: boolean;
  domains: number;
  mailboxes: number;
  storage_bytes: number;
  login_failures: number;
  deferred_count: number;
  rejected_count: number;
}

export default function TenantManagement() {
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [selectedTenant, setSelectedTenant] = useState<Tenant | null>(null);
  const [actionLoading, setActionLoading] = useState(false);

  const loadTenants = async () => {
    setLoading(true);
    setError("");
    try {
      const data = await api.listInternalTenants();
      setTenants(data?.tenants || []);
    } catch (e: any) {
      setError(e.message || "Failed to load tenants");
    }
    setLoading(false);
  };

  useEffect(() => { loadTenants(); }, []);

  const handleSuspendReactivate = async (tenant: Tenant) => {
    setActionLoading(true);
    try {
      await api.setTenantActive(tenant.id, !tenant.active);
      await loadTenants();
      if (selectedTenant?.id === tenant.id) {
        setSelectedTenant({ ...tenant, active: !tenant.active });
      }
    } catch (e: any) {
      setError(e.message || "Failed to update tenant status");
    }
    setActionLoading(false);
  };

  const filtered = tenants.filter((t) => {
    if (!search) return true;
    const q = search.toLowerCase();
    return t.name.toLowerCase().includes(q) || t.domain.toLowerCase().includes(q) || t.slug.toLowerCase().includes(q);
  });

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return "0 B";
    const k = 1024;
    const sizes = ["B", "KB", "MB", "GB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
  };

  if (loading) {
    return <p className="text-[#8B92A8]">Loading tenants...</p>;
  }

  if (error && tenants.length === 0) {
    return (
      <div className="space-y-4">
        <h2 className="text-xl font-semibold text-white">Tenant Management</h2>
        <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-4 text-sm text-red-400">
          {error}
          <button onClick={loadTenants} className="ml-2 underline">Retry</button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-white">Tenant Management</h2>
        <button onClick={loadTenants} className="text-gray-400 hover:text-white p-1" title="Refresh">
          <RefreshCw className="w-4 h-4" />
        </button>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-3 text-sm text-red-400 flex items-center justify-between">
          <span>{error}</span>
          <button onClick={() => setError("")} className="underline">Dismiss</button>
        </div>
      )}

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-500" />
        <input
          type="text" value={search} onChange={(e) => setSearch(e.target.value)}
          placeholder="Search tenants by name, domain, or slug..."
          className="w-full bg-[#0C0E12] border border-[#262A33] rounded-lg pl-10 pr-4 py-2 text-white text-sm"
        />
      </div>

      {filtered.length === 0 && (
        <div className="text-center py-12">
          <Building className="w-8 h-8 text-gray-500 mx-auto mb-2" />
          <p className="text-gray-400 text-sm">{search ? "No tenants match your search." : "No tenants found."}</p>
        </div>
      )}

      <div className="grid gap-3">
        {filtered.map((t) => (
          <div
            key={t.id}
            className={`bg-[#1A1D24] border rounded-lg p-4 cursor-pointer transition-colors ${
              selectedTenant?.id === t.id ? "border-[#4F7CFF]" : "border-[#262A33] hover:border-[#3A3F4E]"
            }`}
            onClick={() => setSelectedTenant(selectedTenant?.id === t.id ? null : t)}
          >
            <div className="flex items-center justify-between mb-2">
              <div className="flex items-center gap-3">
                <Building className="w-5 h-5 text-[#4F7CFF]" />
                <div>
                  <span className="text-white font-medium">{t.name}</span>
                  <span className="ml-2 text-xs text-gray-500">{t.slug}</span>
                </div>
                <span className={`text-xs px-2 py-0.5 rounded ${t.active ? "bg-green-500/10 text-green-400" : "bg-red-500/10 text-red-400"}`}>
                  {t.active ? "Active" : "Suspended"}
                </span>
              </div>
              <div className="flex items-center gap-2 text-xs text-gray-400">
                <span>{t.domains} domains</span>
                <span>|</span>
                <span>{t.mailboxes} mailboxes</span>
              </div>
            </div>

            {selectedTenant?.id === t.id && (
              <div className="mt-4 pt-4 border-t border-[#262A33]">
                <div className="grid grid-cols-3 gap-4 mb-4">
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Domain</p>
                    <p className="text-sm text-white">{t.domain}</p>
                  </div>
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Plan</p>
                    <p className="text-sm text-white">{t.plan || "Free"}</p>
                  </div>
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Storage</p>
                    <p className="text-sm text-white">{formatBytes(t.storage_bytes)}</p>
                  </div>
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Login Failures</p>
                    <p className="text-sm text-white">{t.login_failures}</p>
                  </div>
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Deferred</p>
                    <p className="text-sm text-[#FBBF24]">{t.deferred_count}</p>
                  </div>
                  <div className="bg-[#0C0E12] rounded p-3">
                    <p className="text-xs text-gray-500 mb-1">Bounced/Rejected</p>
                    <p className="text-sm text-[#F87171]">{t.rejected_count}</p>
                  </div>
                </div>
                <div className="flex gap-2">
                  {t.active ? (
                    <button
                      onClick={(e) => { e.stopPropagation(); handleSuspendReactivate(t); }}
                      disabled={actionLoading}
                      className="flex items-center gap-1 bg-red-500/10 border border-red-500/30 text-red-400 rounded px-3 py-1.5 text-xs hover:bg-red-500/20 disabled:opacity-50"
                    >
                      <Ban className="w-3 h-3" /> Suspend
                    </button>
                  ) : (
                    <button
                      onClick={(e) => { e.stopPropagation(); handleSuspendReactivate(t); }}
                      disabled={actionLoading}
                      className="flex items-center gap-1 bg-green-500/10 border border-green-500/30 text-green-400 rounded px-3 py-1.5 text-xs hover:bg-green-500/20 disabled:opacity-50"
                    >
                      <CheckCircle className="w-3 h-3" /> Reactivate
                    </button>
                  )}
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
