import { useState } from "react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useCreateFirewallRuleMutation } from "../mutations";

// CreateFirewallRule (internal/api/handlers/handlers.go) binds
// directly into models.FirewallRule — there is no separate IP/CIDR/
// port/protocol/direction schema on this backend; "condition" is a
// single free-text field the mail-firewall module evaluates. No
// edit/update endpoint exists for firewall rules, so only create is
// offered here — never inventing an edit action the backend doesn't
// support.
export default function FirewallRuleCreateForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [condition, setCondition] = useState("");
  const [action, setAction] = useState<"block" | "throttle" | "allow">("block");
  const [priority, setPriority] = useState("0");
  const [confirming, setConfirming] = useState(false);
  const createMut = useCreateFirewallRuleMutation();

  const priorityNum = Number(priority);
  const canSubmit = name.trim().length > 0 && condition.trim().length > 0 && Number.isInteger(priorityNum);

  return (
    <div className="bg-[var(--bg-elevated)] border border-[var(--border)] rounded-xl p-4 mb-4 space-y-3">
      <h4 className="text-sm font-semibold text-[var(--text-primary)]">New firewall rule</h4>
      <Field label="Name" value={name} onChange={setName} />
      <Field label="Condition" value={condition} onChange={setCondition} placeholder="e.g. sender_ip in blocklist" />
      <div className="flex items-center justify-between gap-4">
        <label className="text-sm text-[var(--text-secondary)]">Action</label>
        <select value={action} onChange={(e) => setAction(e.target.value as typeof action)} className="px-2 py-1 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-48">
          <option value="block">block</option>
          <option value="throttle">throttle</option>
          <option value="allow">allow</option>
        </select>
      </div>
      <Field label="Priority" value={priority} onChange={setPriority} type="number" />

      <div className="flex gap-2">
        <button disabled={!canSubmit || createMut.isPending} onClick={() => setConfirming(true)} className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40">
          {createMut.isPending ? "Creating…" : "Create rule"}
        </button>
        <button onClick={onDone} className="px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]">Cancel</button>
      </div>
      {createMut.isError && <p className="text-[var(--danger)] text-xs">{(createMut.error as Error).message}</p>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Create firewall rule"
        description={`This rule takes effect immediately for all mail traffic. A misconfigured "${action}" rule can block legitimate mail or access.`}
        requireTypedName={name}
        danger={action === "block"}
        pending={createMut.isPending}
        onConfirm={() => {
          createMut.mutate({ name, condition, action, priority: priorityNum, enabled: true }, {
            onSuccess: () => { setConfirming(false); onDone(); },
          });
        }}
      />
    </div>
  );
}

function Field({ label, value, onChange, type = "text", placeholder }: { label: string; value: string; onChange: (v: string) => void; type?: string; placeholder?: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <label className="text-sm text-[var(--text-secondary)]">{label}</label>
      <input type={type} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} className="px-2 py-1 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-48" />
    </div>
  );
}
