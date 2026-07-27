import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Shield, PlusCircle, Monitor, AlertTriangle } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import DataTable from "./ui/DataTable";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import Dialog from "./ui/Dialog";
import Tabs from "./ui/Tabs";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import { useToast } from "./ui/Toast";
import type { FirewallRule, Session } from "../types/security";

export default function SecurityPage() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [activeTab, setActiveTab] = useState("firewall");
  const [showAddRule, setShowAddRule] = useState(false);
  const [showDeleteRule, setShowDeleteRule] = useState<FirewallRule | null>(null);
  const [showTerminate, setShowTerminate] = useState<Session | null>(null);
  const [ruleForm, setRuleForm] = useState({ type: "allow" as "allow" | "block", ip_range: "", description: "" });

  const { data: rules, isLoading: rulesLoading, isError: rulesError, error: rulesErr } = useQuery({
    queryKey: ["firewall-rules"], queryFn: api.listFirewallRules, enabled: activeTab === "firewall",
  });

  const { data: sessions, isLoading: sessionsLoading } = useQuery({
    queryKey: ["sessions-list"], queryFn: api.listSessions, enabled: activeTab === "sessions",
  });

  const addRuleMutation = useMutation({
    mutationFn: (data: typeof ruleForm) => api.addFirewallRule(data),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["firewall-rules"] }); setShowAddRule(false); setRuleForm({ type: "allow", ip_range: "", description: "" }); toast({ message: "Rule added.", variant: "success" }); },
    onError: (err: any) => toast({ message: err?.message || "Failed to add rule", variant: "danger" }),
  });

  const deleteRuleMutation = useMutation({
    mutationFn: (id: number) => api.deleteFirewallRule(id),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["firewall-rules"] }); setShowDeleteRule(null); toast({ message: "Rule deleted.", variant: "info" }); },
    onError: (err: any) => toast({ message: err?.message || "Failed to delete", variant: "danger" }),
  });

  const terminateMutation = useMutation({
    mutationFn: (id: string) => api.revokeSession(id),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["sessions-list"] }); setShowTerminate(null); toast({ message: "Session terminated.", variant: "info" }); },
    onError: (err: any) => toast({ message: err?.message || "Failed to terminate", variant: "danger" }),
  });

  const ruleList: FirewallRule[] = Array.isArray(rules) ? rules : [];
  const sessionList: Session[] = Array.isArray(sessions) ? sessions : sessions?.sessions || [];

  const ruleCols = [
    { key: "type", label: "Type", render: (row: FirewallRule) => <Badge variant={row.type === "allow" ? "teal" : "danger"}>{row.type}</Badge> },
    { key: "ip_range", label: "IP Range", render: (row: FirewallRule) => <code className="text-xs font-mono text-[var(--accent-blue)]">{row.ip_range}</code> },
    { key: "description", label: "Description" },
    { key: "created_at", label: "Created", render: (row: FirewallRule) => new Date(row.created_at).toLocaleDateString() },
    { key: "actions", label: "", width: "60px", render: (row: FirewallRule) => <Button variant="danger" size="sm" onClick={() => setShowDeleteRule(row)}>Delete</Button> },
  ];

  const sessionCols = [
    { key: "user_email", label: "User", render: (row: Session) => <span className={row.is_current ? "font-medium text-[var(--accent)]" : ""}>{row.user_email}{row.is_current ? " (current)" : ""}</span> },
    { key: "ip_address", label: "IP" },
    { key: "user_agent", label: "Client", render: (row: Session) => <span className="text-xs text-[var(--text-muted)]">{row.user_agent?.slice(0, 60)}</span> },
    { key: "created_at", label: "Created", render: (row: Session) => new Date(row.created_at).toLocaleString() },
    { key: "last_active_at", label: "Last Active", render: (row: Session) => new Date(row.last_active_at).toLocaleString() },
    { key: "actions", label: "", width: "60px", render: (row: Session) => !row.is_current && <Button variant="danger" size="sm" onClick={() => setShowTerminate(row)}>Terminate</Button> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Security" subtitle="Firewall rules and session management" />

      <Tabs tabs={[{ id: "firewall", label: "Firewall Rules" }, { id: "sessions", label: "Active Sessions" }]} activeTab={activeTab} onChange={setActiveTab} />

      {activeTab === "firewall" && (
        <div className="space-y-4">
          {rulesError && <ErrorBanner message={(rulesErr as any)?.message || "Failed to load rules"} />}
          <Button variant="primary" size="sm" onClick={() => setShowAddRule(true)} iconLeft={<PlusCircle size={14} />}>Add Rule</Button>
          <DataTable columns={ruleCols} rows={ruleList} loading={rulesLoading}
            emptyState={<EmptyState icon={Shield} title="No firewall rules" />}
          />
        </div>
      )}

      {activeTab === "sessions" && (
        <DataTable columns={sessionCols} rows={sessionList} loading={sessionsLoading}
          emptyState={<EmptyState icon={Monitor} title="No active sessions" />}
        />
      )}

      {/* Add rule dialog */}
      <Dialog open={showAddRule} onClose={() => setShowAddRule(false)} title="Add Firewall Rule"
        footer={<><Button variant="ghost" onClick={() => setShowAddRule(false)}>Cancel</Button><Button variant="primary" loading={addRuleMutation.isPending} disabled={!ruleForm.ip_range} onClick={() => addRuleMutation.mutate(ruleForm)}>Add</Button></>}
      >
        <div className="space-y-4">
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Type</label><select value={ruleForm.type} onChange={(e) => setRuleForm({ ...ruleForm, type: e.target.value as "allow" | "block" })} className="orvix-select"><option value="allow">Allow</option><option value="block">Block</option></select></div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">IP Range *</label><input value={ruleForm.ip_range} onChange={(e) => setRuleForm({ ...ruleForm, ip_range: e.target.value })} placeholder="192.168.1.0/24" className="orvix-input" /></div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Description</label><input value={ruleForm.description} onChange={(e) => setRuleForm({ ...ruleForm, description: e.target.value })} placeholder="Why this rule?" className="orvix-input" /></div>
        </div>
      </Dialog>

      {/* Delete rule */}
      <Dialog open={!!showDeleteRule} onClose={() => setShowDeleteRule(null)} title="Delete Rule"
        description={`Delete rule for ${showDeleteRule?.ip_range}?`}
        footer={<><Button variant="ghost" onClick={() => setShowDeleteRule(null)}>Cancel</Button><Button variant="danger" loading={deleteRuleMutation.isPending} onClick={() => showDeleteRule && deleteRuleMutation.mutate(showDeleteRule.id)}>Delete</Button></>}
      />

      {/* Terminate session */}
      <Dialog open={!!showTerminate} onClose={() => setShowTerminate(null)} title="Terminate Session"
        description={`Terminate session for ${showTerminate?.user_email}?`}
        footer={<><Button variant="ghost" onClick={() => setShowTerminate(null)}>Cancel</Button><Button variant="danger" loading={terminateMutation.isPending} onClick={() => showTerminate && terminateMutation.mutate(showTerminate.id)}>Terminate</Button></>}
      />
    </div>
  );
}
