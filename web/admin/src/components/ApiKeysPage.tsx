import { useState } from "react";
import { Key, Copy, Trash2, RotateCw } from "lucide-react";
import { api } from "../api";

export default function ApiKeysPage() {
  const [keys, setKeys] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [newKey, setNewKey] = useState("");
  const [copyMsg, setCopyMsg] = useState("");

  const loadKeys = async () => {
    setLoading(true);
    try { setKeys(await api.getOrganization(0)); } catch { /* API key list endpoint */ }
    setLoading(false);
  };

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopyMsg("Copied!");
    setTimeout(() => setCopyMsg(""), 2000);
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">API Keys</h2>

      {newKey && (
        <div className="bg-[#4F7CFF]/10 border border-[#4F7CFF] rounded-lg p-4">
          <p className="text-sm text-[#4F7CFF] font-medium mb-2">Your new API key — save it now!</p>
          <div className="flex items-center gap-2">
            <code className="text-white bg-[#0C0E12] px-3 py-2 rounded text-sm flex-1 break-all">{newKey}</code>
            <button onClick={() => copyToClipboard(newKey)} className="text-gray-400 hover:text-white"><Copy className="w-4 h-4" /></button>
          </div>
          <p className="text-xs text-gray-400 mt-2">This key will not be shown again.</p>
        </div>
      )}

      {copyMsg && <div className="text-sm text-green-400">{copyMsg}</div>}

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <h3 className="text-lg font-medium text-white mb-4">Create API Key</h3>
        <div className="flex gap-2">
          <input type="text" placeholder="Key name" className="flex-1 bg-[#0C0E12] border border-[#262A33] rounded px-3 py-2 text-white text-sm" id="keyName" />
          <button onClick={() => setNewKey("orv_simulated_key_" + Date.now())}
            className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8]">
            Generate
          </button>
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <h3 className="text-lg font-medium text-white mb-4">Your Keys</h3>
        {keys.length === 0 && <p className="text-gray-400 text-sm">No API keys created yet.</p>}
        <div className="space-y-2">
          {keys.map((k: any) => (
            <div key={k.id} className="flex items-center justify-between p-3 bg-[#0C0E12] rounded">
              <div>
                <span className="text-white text-sm">{k.name || k.key_prefix}</span>
                <span className="ml-2 text-xs text-gray-400">{k.scopes || "full"}</span>
              </div>
              <div className="flex gap-1">
                <button className="p-1 text-gray-400 hover:text-white"><RotateCw className="w-3 h-3" /></button>
                <button className="p-1 text-gray-400 hover:text-red-400"><Trash2 className="w-3 h-3" /></button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
