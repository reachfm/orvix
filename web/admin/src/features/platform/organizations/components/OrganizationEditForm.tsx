import { useState } from "react";
import { useUpdateOrganizationMutation } from "../mutations";
import type { OrganizationDetail, UpdateOrganizationRequest } from "../contract";

// Real, previously-unwired PATCH /platform/organizations/:id
// (UpdateOrganization, organization_admin.go). Only supported fields
// are editable: name/domain/plan/max_domains/max_mailboxes. Only
// fields the operator actually changed are sent — the backend's
// UpdateOrganizationRequest uses optional pointer fields, where an
// absent key means "leave unchanged," not "clear to zero."
export default function OrganizationEditForm({ org, id, onDone }: { org: OrganizationDetail; id: number; onDone: () => void }) {
  const [name, setName] = useState(org.name);
  const [domain, setDomain] = useState(org.domain);
  const [plan, setPlan] = useState(org.plan);
  const [maxDomains, setMaxDomains] = useState(String(org.max_domains));
  const [maxMailboxes, setMaxMailboxes] = useState(String(org.max_mailboxes));
  const updateMut = useUpdateOrganizationMutation(id);

  const buildPatch = (): UpdateOrganizationRequest => {
    const patch: UpdateOrganizationRequest = {};
    if (name !== org.name) patch.name = name;
    if (domain !== org.domain) patch.domain = domain;
    if (plan !== org.plan) patch.plan = plan;
    if (Number(maxDomains) !== org.max_domains) patch.max_domains = Number(maxDomains);
    if (Number(maxMailboxes) !== org.max_mailboxes) patch.max_mailboxes = Number(maxMailboxes);
    return patch;
  };
  const patch = buildPatch();
  const dirty = Object.keys(patch).length > 0;

  return (
    <div className="space-y-3 mb-4 bg-[var(--bg-elevated)] border border-[var(--border)] rounded-xl p-4">
      <Field label="Name" value={name} onChange={setName} />
      <Field label="Domain" value={domain} onChange={setDomain} />
      <Field label="Plan" value={plan} onChange={setPlan} />
      <Field label="Max domains" type="number" value={maxDomains} onChange={setMaxDomains} />
      <Field label="Max mailboxes" type="number" value={maxMailboxes} onChange={setMaxMailboxes} />

      <div className="flex gap-2 pt-2">
        <button
          disabled={!dirty || updateMut.isPending}
          onClick={() => updateMut.mutate(patch, { onSuccess: onDone })}
          className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
        >
          {updateMut.isPending ? "Saving…" : "Save changes"}
        </button>
        <button onClick={onDone} className="px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]">Cancel</button>
      </div>
      {updateMut.isError && <p className="text-[var(--danger)] text-xs">{(updateMut.error as Error).message}</p>}
    </div>
  );
}

function Field({ label, value, onChange, type = "text" }: { label: string; value: string; onChange: (v: string) => void; type?: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <label className="text-sm text-[var(--text-secondary)]">{label}</label>
      <input
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="px-2 py-1 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-48 text-right"
      />
    </div>
  );
}
