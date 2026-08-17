import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Copy, Trash2, RotateCw, Plus } from "lucide-react";
import { api, ApiError } from "../api";

// Public API-key scopes the backend accepts for tenant-scoped keys
// (internal/api/handlers/handlers_enterprise_portal.go publicScopePermission).
const AVAILABLE_SCOPES = [
  { id: "organization.read", label: "Organization (read)" },
  { id: "domains.read", label: "Domains (read)" },
  { id: "domains.write", label: "Domains (write)" },
  { id: "mailboxes.read", label: "Mailboxes (read)" },
  { id: "mailboxes.write", label: "Mailboxes (write)" },
  { id: "aliases.read", label: "Aliases (read)" },
  { id: "aliases.write", label: "Aliases (write)" },
  { id: "groups.read", label: "Groups (read)" },
  { id: "groups.write", label: "Groups (write)" },
  { id: "usage.read", label: "Usage (read)" },
];

export default function ApiKeysPage() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [selectedScopes, setSelectedScopes] = useState<string[]>(["domains.read", "mailboxes.read", "usage.read"]);
  const [newKey, setNewKey] = useState("");
  const [copyMsg, setCopyMsg] = useState("");

  const { data: keys, isLoading, error } = useQuery({
    queryKey: ["enterprise_api_keys"],
    queryFn: api.listEnterpriseApiKeys,
  });

  const create = useMutation({
    mutationFn: () => api.createEnterpriseApiKey({ name: name.trim() || "api-key", scopes: selectedScopes }),
    onSuccess: (res: any) => {
      setNewKey(res?.api_key || "");
      setName("");
      queryClient.invalidateQueries({ queryKey: ["enterprise_api_keys"] });
    },
  });

  const rotate = useMutation({
    mutationFn: (id: number) => api.rotateEnterpriseApiKey(id),
    onSuccess: (res: any) => {
      setNewKey(res?.api_key || "");
      queryClient.invalidateQueries({ queryKey: ["enterprise_api_keys"] });
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteEnterpriseApiKey(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["enterprise_api_keys"] }),
  });

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopyMsg("Copied!");
    setTimeout(() => setCopyMsg(""), 2000);
  };

  const toggleScope = (scope: string) => {
    setSelectedScopes((prev) => (prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]));
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">API Keys</h2>

      {newKey && (
        <div className="bg-[var(--accent)]/10 border border-[var(--accent)] rounded-lg p-4">
          <p className="text-sm text-[var(--accent)] font-medium mb-2">Your new API key — save it now!</p>
          <div className="flex items-center gap-2">
            <code className="text-[var(--text-primary)] bg-[var(--bg-base)] px-3 py-2 rounded text-sm flex-1 break-all">{newKey}</code>
            <button onClick={() => copyToClipboard(newKey)} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]"><Copy className="w-4 h-4" /></button>
          </div>
          <p className="text-xs text-[var(--text-secondary)] mt-2">This key will not be shown again.</p>
        </div>
      )}

      {copyMsg && <div className="text-sm text-[var(--success)]">{copyMsg}</div>}

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Create API Key</h3>
        <div className="flex gap-2">
          <input
            type="text"
            placeholder="Key name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="flex-1 bg-[var(--bg-base)] border border-[var(--bg-subtle)] rounded px-3 py-2 text-[var(--text-primary)] text-sm"
            aria-label="Key name"
          />
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          {AVAILABLE_SCOPES.map((s) => (
            <label key={s.id} className="flex items-center gap-1.5 text-xs text-[var(--text-secondary)] bg-[var(--bg-base)] border border-[var(--bg-subtle)] rounded px-2 py-1 cursor-pointer">
              <input type="checkbox" checked={selectedScopes.includes(s.id)} onChange={() => toggleScope(s.id)} />
              {s.label}
            </label>
          ))}
        </div>
        <button
          onClick={() => create.mutate()}
          disabled={create.isPending || selectedScopes.length === 0}
          className="mt-4 flex items-center gap-2 bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50"
        >
          <Plus className="w-4 h-4" /> {create.isPending ? "Creating..." : "Generate"}
        </button>
        {create.error && (
          <p className="mt-2 text-sm text-[var(--danger)]">
            {(create.error as ApiError).message || "Failed to create API key"}
          </p>
        )}
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Your Keys</h3>
        {isLoading && <p className="text-[var(--text-secondary)] text-sm">Loading...</p>}
        {error && <p className="text-[var(--danger)] text-sm">{(error as ApiError).message}</p>}
        {!isLoading && !error && (!keys || keys.length === 0) && (
          <p className="text-[var(--text-secondary)] text-sm">No API keys created yet.</p>
        )}
        <div className="space-y-2">
          {(keys || []).map((k: any) => (
            <div key={k.id} className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded">
              <div>
                <span className="text-[var(--text-primary)] text-sm">{k.name || k.key_prefix || `Key #${k.id}`}</span>
                <span className="ml-2 text-xs text-[var(--text-secondary)]">{k.scopes || "full"}</span>
              </div>
              <div className="flex gap-1">
                <button
                  onClick={() => rotate.mutate(k.id)}
                  disabled={rotate.isPending || remove.isPending}
                  title="Rotate key"
                  className="p-1 text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-40"
                >
                  <RotateCw className="w-3 h-3" />
                </button>
                <button
                  onClick={() => remove.mutate(k.id)}
                  disabled={remove.isPending || rotate.isPending}
                  title="Delete key"
                  className="p-1 text-[var(--text-secondary)] hover:text-[var(--danger)] disabled:opacity-40"
                >
                  <Trash2 className="w-3 h-3" />
                </button>
              </div>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
