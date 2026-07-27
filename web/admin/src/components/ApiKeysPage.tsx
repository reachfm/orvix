import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Key, PlusCircle, Copy, AlertTriangle, Eye, EyeOff } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import DataTable from "./ui/DataTable";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import Dialog from "./ui/Dialog";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import { useToast } from "./ui/Toast";
import type { ApiKey } from "../types/apikeys";

export default function ApiKeysPage() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [showCreate, setShowCreate] = useState(false);
  const [showRevoke, setShowRevoke] = useState<ApiKey | null>(null);
  const [newKeySecret, setNewKeySecret] = useState("");
  const [copyMsg, setCopyMsg] = useState("");
  const [createForm, setCreateForm] = useState({ name: "", scopes: [] as string[], ttl: "" });
  const [secretVisible, setSecretVisible] = useState(false);

  const { data: keys, isLoading, isError, error } = useQuery({
    queryKey: ["enterprise-api-keys"],
    queryFn: api.listEnterpriseApiKeys,
  });

  const createMutation = useMutation({
    mutationFn: (data: typeof createForm) => api.createEnterpriseApiKey({ name: data.name, scopes: data.scopes.length ? data.scopes : undefined, ttl: data.ttl || undefined }),
    onSuccess: (data: any) => {
      setNewKeySecret(data.secret || data.api_key || "");
      setShowCreate(false);
      setCreateForm({ name: "", scopes: [], ttl: "" });
      queryClient.invalidateQueries({ queryKey: ["enterprise-api-keys"] });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to create key", variant: "danger" }),
  });

  const revokeMutation = useMutation({
    mutationFn: (id: number) => api.revokeEnterpriseApiKey(id),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["enterprise-api-keys"] }); setShowRevoke(null); toast({ message: "API key revoked.", variant: "info" }); },
    onError: (err: any) => toast({ message: err?.message || "Failed to revoke", variant: "danger" }),
  });

  const copySecret = (text: string) => { navigator.clipboard.writeText(text); setCopyMsg("Copied!"); setTimeout(() => setCopyMsg(""), 2000); };

  const keyList: ApiKey[] = Array.isArray(keys) ? keys : [];
  const scopes = ["read", "write", "admin"];

  const cols = [
    { key: "name", label: "Name", render: (row: any) => <span className="font-medium text-[var(--text-primary)]">{row.name}</span> },
    { key: "prefix", label: "Prefix", render: (row: any) => <code className="text-xs font-mono text-[var(--text-secondary)]">{row.key_prefix || row.prefix}</code> },
    { key: "scopes", label: "Scopes", render: (row: any) => (row.scopes || row.scopes_str || "").split(",").filter(Boolean).map((s: string) => <Badge key={s} variant="teal" size="sm">{s}</Badge>) },
    { key: "created_at", label: "Created", render: (row: any) => new Date(row.created_at).toLocaleDateString() },
    { key: "actions", label: "", width: "60px", render: (row: any) => <Button variant="danger" size="sm" disabled={!row.active && row.active !== undefined} onClick={(e) => { e?.stopPropagation?.(); setShowRevoke(row); }}>Revoke</Button> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="API Keys" subtitle="Scoped API access tokens"
        actions={<Button variant="primary" onClick={() => setShowCreate(true)} iconLeft={<PlusCircle size={16} />}>Create Key</Button>}
      />

      {isError && <ErrorBanner message={(error as any)?.message || "Failed to load API keys"} />}

      <DataTable columns={cols} rows={keyList} loading={isLoading}
        emptyState={<EmptyState icon={Key} title="No API keys yet" />}
      />

      {copyMsg && <div className="text-sm text-[var(--accent)]">{copyMsg}</div>}

      {/* One-time secret display */}
      {newKeySecret && (
        <div className="orvix-surface-card p-4 border-[var(--accent)]/30">
          <div className="flex items-center gap-2 mb-2">
            <AlertTriangle size={16} className="text-[var(--warning)]" />
            <p className="text-sm font-medium text-[var(--text-primary)]">Your new API key — save it now!</p>
          </div>
          <div className="flex items-center gap-2 bg-[var(--bg-base)] rounded-lg p-3">
            <code className="text-sm font-mono text-[var(--accent-blue)] flex-1 break-all">{secretVisible ? newKeySecret : "●".repeat(40)}</code>
            <button onClick={() => setSecretVisible((v) => !v)} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]" title="Toggle visibility"><Eye size={16} /></button>
            <button onClick={() => copySecret(newKeySecret)} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]" title="Copy"><Copy size={16} /></button>
          </div>
          <p className="text-xs text-[var(--text-muted)] mt-2">This key will not be shown again after you dismiss this message.</p>
          <Button variant="ghost" size="sm" onClick={() => setNewKeySecret("")} className="mt-2">Dismiss</Button>
        </div>
      )}

      {/* Create dialog */}
      <Dialog open={showCreate} onClose={() => setShowCreate(false)} title="Create API Key"
        footer={<><Button variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button><Button variant="primary" loading={createMutation.isPending} disabled={!createForm.name} onClick={() => createMutation.mutate(createForm)}>Generate</Button></>}
      >
        <div className="space-y-4">
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Name *</label><input value={createForm.name} onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })} placeholder="e.g. CI/CD token" className="orvix-input" /></div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-2">Scopes</label>
            <div className="flex gap-3">{scopes.map((s) => <label key={s} className="flex items-center gap-1.5 text-sm"><input type="checkbox" checked={createForm.scopes.includes(s)} onChange={(e) => { setCreateForm({ ...createForm, scopes: e.target.checked ? [...createForm.scopes, s] : createForm.scopes.filter((x) => x !== s) }); }} className="accent-[var(--accent)]" />{s}</label>)}</div>
          </div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Expiry</label><select value={createForm.ttl} onChange={(e) => setCreateForm({ ...createForm, ttl: e.target.value })} className="orvix-select"><option value="">Never</option><option value="720h">30 days</option><option value="2160h">90 days</option><option value="8760h">1 year</option></select></div>
        </div>
      </Dialog>

      {/* Revoke confirmation */}
      <Dialog open={!!showRevoke} onClose={() => setShowRevoke(null)} title="Revoke API Key"
        description={`Revoke "${showRevoke?.name}"? Any services using this key will lose access immediately.`}
        footer={<><Button variant="ghost" onClick={() => setShowRevoke(null)}>Cancel</Button><Button variant="danger" loading={revokeMutation.isPending} onClick={() => showRevoke && revokeMutation.mutate(showRevoke.id)}>Confirm Revoke</Button></>}
      />
    </div>
  );
}
