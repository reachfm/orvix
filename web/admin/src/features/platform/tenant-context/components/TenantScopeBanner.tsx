import { useEffect, useState } from "react";
import { Filter } from "lucide-react";
import { useClearTenantScope, useSetTenantScope, useTenantOptions, useTenantScope } from "../queries";

/**
 * Explicit tenant-scope selector for Platform Super Admin mail control.
 *
 * The selected tenant id is a filter/mutation TARGET in the platform
 * route path (/platform/domains/:tenant_id, ...). It is never sent as
 * an authentication header and never impersonates a tenant. Options
 * come from the real organization inventory (GET /platform/organizations).
 */
export default function TenantScopeBanner() {
  const { data: scope } = useTenantScope();
  const { data: orgs } = useTenantOptions();
  const setScope = useSetTenantScope();
  const clearScope = useClearTenantScope();
  const [draft, setDraft] = useState("");

  const tenantId = scope?.tenantId ?? null;
  const tenantName = scope?.tenantName;
  const options = orgs?.organizations ?? [];

  // Diagnosis (Phase H): the <select> below used to be a controlled
  // component whose `value` was bound to the APPLIED scope (`tenantId`)
  // while `onChange` only wrote to a separate `draft` state. Since `value`
  // never changed until "Apply scope" was clicked and its mutation
  // resolved, React re-rendered the <select> back to the old `tenantId` on
  // every keystroke/selection — an operator picking a tenant would watch
  // the dropdown immediately snap back to "— Select a tenant —" (or the
  // previous scope), making every tenant-scoped platform page (Platform
  // Billing foremost, since it renders nothing at all until a tenant is
  // applied) look like it silently refused to respond. This effect keeps
  // `draft` — the value the <select> is now actually bound to — in sync
  // with the authoritative applied scope on load and after Clear/Apply,
  // while still letting onChange update it immediately for in-progress
  // picks.
  useEffect(() => {
    setDraft(tenantId === null ? "" : String(tenantId));
  }, [tenantId]);

  return (
    <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)]" role="region" aria-label="Tenant scope">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2 text-sm font-medium text-[var(--text-primary)]">
          <Filter size={16} className="text-[var(--accent)]" aria-hidden="true" />
          Tenant scope
        </div>
        <select
          aria-label="Select tenant"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
        >
          <option value="">— Select a tenant —</option>
          {options.map((o) => (
            <option key={o.id} value={String(o.id)}>
              {o.name} (tenant {o.id})
            </option>
          ))}
        </select>
        <button
          type="button"
          disabled={!draft || setScope.isPending}
          onClick={() => {
            const id = Number(draft);
            const org = options.find((o) => o.id === id);
            setScope.mutate({ tenantId: id, tenantName: org?.name });
          }}
          className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
        >
          Apply scope
        </button>
        {tenantId !== null && (
          <button
            type="button"
            onClick={() => clearScope.mutate()}
            className="px-3 py-1.5 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          >
            Clear
          </button>
        )}
      </div>
      <p className="text-xs text-[var(--text-secondary)] mt-2">
        {tenantId === null
          ? "Select an explicit tenant to scope mail control. Platform routes require an explicit tenant target — none is assumed."
          : `Scoped to tenant ${tenantId}${tenantName ? ` (${tenantName})` : ""}. All list, detail, and mutation requests target this tenant id in the platform route path.`}
      </p>
    </div>
  );
}
