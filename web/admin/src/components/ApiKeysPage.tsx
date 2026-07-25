import { useState, useEffect } from "react";
import { Copy, Trash2, RotateCw, AlertTriangle, X, Key } from "lucide-react";
import { api } from "../api";

interface ApiKey {
  id: number;
  name: string;
  key_prefix: string;
  enabled: boolean;
  last_used?: string;
  expires_at?: string;
  created_at: string;
}

export default function ApiKeysPage() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [newKeySecret, setNewKeySecret] = useState("");
  const [keyName, setKeyName] = useState("");
  const [copyMsg, setCopyMsg] = useState("");
  const [generating, setGenerating] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [rotateTarget, setRotateTarget] = useState<ApiKey | null>(null);
  const [revokeTarget, setRevokeTarget] = useState<ApiKey | null>(null);
  const [reauthScope, setReauthScope] = useState("");
  const [reauthPassword, setReauthPassword] = useState("");
  const [reauthError, setReauthError] = useState("");
  const [pendingAction, setPendingAction] = useState<(() => Promise<void>) | null>(null);

  const loadKeys = async () => {
    setLoading(true);
    setError("");
    try {
      const data = await api.listApiKeys();
      setKeys(data || []);
    } catch (e: any) {
      setError(e.message || "Failed to load API keys");
    }
    setLoading(false);
  };

  useEffect(() => { loadKeys(); }, []);

  const dismissNewKey = () => setNewKeySecret("");

  const handleGenerate = async () => {
    if (!keyName.trim()) return;
    setGenerating(true);
    setError("");
    try {
      const data = await api.createApiKey({ name: keyName.trim() });
      setNewKeySecret(data.api_key || data.key || "");
      setKeyName("");
      setShowCreate(false);
      await loadKeys();
    } catch (e: any) {
      const msg = e.message || "";
      if (msg.toLowerCase().includes("reauth") || msg.toLowerCase().includes("recent authentication")) {
        setReauthScope("api_key_management");
        setPendingAction(() => async () => {
          const d = await api.createApiKey({ name: keyName.trim() });
          setNewKeySecret(d.api_key || d.key || "");
          setKeyName("");
          setShowCreate(false);
          await loadKeys();
        });
        return;
      }
      setError(msg || "Failed to generate API key");
    }
    setGenerating(false);
  };

  const handleRotate = async () => {
    if (!rotateTarget) return;
    setError("");
    try {
      const data = await api.rotateApiKey(rotateTarget.id);
      setNewKeySecret(data.api_key || data.key || "");
      setRotateTarget(null);
      await loadKeys();
    } catch (e: any) {
      const msg = e.message || "";
      if (msg.toLowerCase().includes("reauth") || msg.toLowerCase().includes("recent authentication")) {
        setReauthScope("api_key_management");
        setPendingAction(() => async () => {
          const d = await api.rotateApiKey(rotateTarget.id);
          setNewKeySecret(d.api_key || d.key || "");
          setRotateTarget(null);
          await loadKeys();
        });
        return;
      }
      setError(msg || "Failed to rotate key");
    }
  };

  const handleRevoke = async () => {
    if (!revokeTarget) return;
    setError("");
    try {
      await api.revokeApiKey(revokeTarget.id);
      setRevokeTarget(null);
      await loadKeys();
    } catch (e: any) {
      const msg = e.message || "";
      if (msg.toLowerCase().includes("reauth") || msg.toLowerCase().includes("recent authentication")) {
        setReauthScope("api_key_management");
        setPendingAction(() => async () => {
          await api.revokeApiKey(revokeTarget.id);
          setRevokeTarget(null);
          await loadKeys();
        });
        return;
      }
      setError(msg || "Failed to revoke key");
    }
  };

  const handleReauthSubmit = async () => {
    setReauthError("");
    try {
      await api.changePassword({ current_password: reauthPassword, new_password: "" });
      if (pendingAction) {
        await pendingAction();
      }
      setReauthScope("");
      setReauthPassword("");
      setPendingAction(null);
    } catch (e: any) {
      setReauthError(e.message || "Re-authentication failed");
    }
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopyMsg("Copied!");
    setTimeout(() => setCopyMsg(""), 2000);
  };

  if (loading) {
    return <p className="text-[#8B92A8]">Loading API keys...</p>;
  }

  if (error && keys.length === 0) {
    return (
      <div className="space-y-4">
        <h2 className="text-xl font-semibold text-white">API Keys</h2>
        <p className="text-[#F87171]">Failed to load API keys: {error}</p>
        <button onClick={loadKeys} className="text-sm text-[#4F7CFF] hover:underline">Retry</button>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-white">API Keys</h2>
        <button onClick={() => setShowCreate(true)}
          className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8]">
          Create Key
        </button>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/30 rounded-lg p-3 text-sm text-red-400">
          {error}
          <button onClick={() => setError("")} className="ml-2 underline">Dismiss</button>
        </div>
      )}

      {newKeySecret && (
        <div className="bg-[#4F7CFF]/10 border border-[#4F7CFF] rounded-lg p-4">
          <div className="flex items-center justify-between mb-2">
            <p className="text-sm text-[#4F7CFF] font-medium">Your new API key — save it now!</p>
            <button onClick={dismissNewKey} className="text-gray-400 hover:text-white"><X className="w-4 h-4" /></button>
          </div>
          <div className="flex items-center gap-2">
            <code className="text-white bg-[#0C0E12] px-3 py-2 rounded text-sm flex-1 break-all">{newKeySecret}</code>
            <button onClick={() => copyToClipboard(newKeySecret)} className="text-gray-400 hover:text-white" title="Copy">
              <Copy className="w-4 h-4" />
            </button>
          </div>
          <p className="text-xs text-gray-400 mt-2">
            This key will not be shown again after you dismiss this message.
          </p>
        </div>
      )}

      {copyMsg && <div className="text-sm text-green-400">{copyMsg}</div>}

      {/* Create modal */}
      {showCreate && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setShowCreate(false)}>
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-xl p-6 w-full max-w-md mx-4" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-lg font-medium text-white mb-4">Create API Key</h3>
            <input
              type="text" value={keyName} onChange={(e) => setKeyName(e.target.value)}
              placeholder="Key name (e.g. ci-cd, monitoring)"
              className="w-full bg-[#0C0E12] border border-[#262A33] rounded px-3 py-2 text-white text-sm mb-4"
              onKeyDown={(e) => { if (e.key === "Enter") handleGenerate(); }}
              autoFocus
            />
            <div className="flex gap-2 justify-end">
              <button onClick={() => setShowCreate(false)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
              <button onClick={handleGenerate} disabled={generating || !keyName.trim()}
                className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
                {generating ? "Generating..." : "Generate"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Rotate confirmation */}
      {rotateTarget && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setRotateTarget(null)}>
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-xl p-6 w-full max-w-md mx-4" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center gap-3 mb-4">
              <RotateCw className="w-5 h-5 text-[#FBBF24]" />
              <h3 className="text-lg font-medium text-white">Rotate API Key</h3>
            </div>
            <p className="text-sm text-gray-400 mb-4">
              Key <span className="text-white font-mono">{rotateTarget.key_prefix}***</span> will be disabled and a new key generated with the same name and scopes.
            </p>
            <div className="flex gap-2 justify-end">
              <button onClick={() => setRotateTarget(null)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
              <button onClick={handleRotate}
                className="bg-[#FBBF24] text-black rounded px-4 py-2 text-sm hover:bg-[#F59E0B]">
                Confirm Rotation
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Revoke confirmation */}
      {revokeTarget && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setRevokeTarget(null)}>
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-xl p-6 w-full max-w-md mx-4" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center gap-3 mb-4">
              <AlertTriangle className="w-5 h-5 text-red-400" />
              <h3 className="text-lg font-medium text-white">Revoke API Key</h3>
            </div>
            <p className="text-sm text-gray-400 mb-4">
              Key <span className="text-white font-mono">{revokeTarget.key_prefix}***</span> will be permanently disabled. Any services using this key will lose access immediately.
            </p>
            <div className="flex gap-2 justify-end">
              <button onClick={() => setRevokeTarget(null)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
              <button onClick={handleRevoke}
                className="bg-red-500 text-white rounded px-4 py-2 text-sm hover:bg-red-600">
                Confirm Revocation
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Re-auth modal */}
      {reauthScope && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => { setReauthScope(""); setPendingAction(null); }}>
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-xl p-6 w-full max-w-md mx-4" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center gap-3 mb-4">
              <Key className="w-5 h-5 text-[#4F7CFF]" />
              <h3 className="text-lg font-medium text-white">Re-authentication Required</h3>
            </div>
            <p className="text-sm text-gray-400 mb-4">
              For security, please confirm your password to perform this API key operation.
            </p>
            {reauthError && <p className="text-sm text-red-400 mb-2">{reauthError}</p>}
            <input
              type="password" value={reauthPassword} onChange={(e) => setReauthPassword(e.target.value)}
              placeholder="Current password"
              className="w-full bg-[#0C0E12] border border-[#262A33] rounded px-3 py-2 text-white text-sm mb-4"
              onKeyDown={(e) => { if (e.key === "Enter") handleReauthSubmit(); }}
              autoFocus
            />
            <div className="flex gap-2 justify-end">
              <button onClick={() => { setReauthScope(""); setPendingAction(null); }} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
              <button onClick={handleReauthSubmit}
                className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8]">
                Confirm
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Key list */}
      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <h3 className="text-lg font-medium text-white mb-4">Your Keys</h3>
        {keys.length === 0 && (
          <div className="text-center py-8">
            <Key className="w-8 h-8 text-gray-500 mx-auto mb-2" />
            <p className="text-gray-400 text-sm">No API keys created yet.</p>
            <button onClick={() => setShowCreate(true)} className="text-sm text-[#4F7CFF] hover:underline mt-2">
              Create your first key
            </button>
          </div>
        )}
        <div className="space-y-2">
          {keys.map((k) => (
            <div key={k.id} className="flex items-center justify-between p-3 bg-[#0C0E12] rounded">
              <div className="flex items-center gap-3">
                <div>
                  <span className="text-white text-sm font-medium">{k.name}</span>
                  <span className="ml-2 text-xs text-gray-500 font-mono">{k.key_prefix}***</span>
                </div>
                <span className={`text-xs px-2 py-0.5 rounded ${k.enabled ? "bg-green-500/10 text-green-400" : "bg-red-500/10 text-red-400"}`}>
                  {k.enabled ? "Active" : "Revoked"}
                </span>
              </div>
              <div className="flex gap-1">
                <button onClick={() => setRotateTarget(k)} disabled={!k.enabled}
                  className="p-1.5 text-gray-400 hover:text-white disabled:opacity-30 disabled:cursor-not-allowed rounded hover:bg-[#222736]"
                  title="Rotate key"><RotateCw className="w-3.5 h-3.5" /></button>
                <button onClick={() => setRevokeTarget(k)} disabled={!k.enabled}
                  className="p-1.5 text-gray-400 hover:text-red-400 disabled:opacity-30 disabled:cursor-not-allowed rounded hover:bg-[#222736]"
                  title="Revoke key"><Trash2 className="w-3.5 h-3.5" /></button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
