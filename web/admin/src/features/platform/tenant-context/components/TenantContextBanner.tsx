import { useState } from "react";
import { useSetTenantContext, useClearTenantContext, useTenantContext, useActiveGrantForSelectedTenant } from "../queries";
import { useOrganizationsQuery } from "../../organizations/queries";

/**
 * TenantContextBanner is the operational tenant-context control for
 * Platform Super Admin mail-control pages. It lets the operator select
 * the tenant whose inventory they are inspecting (from the real
 * organization list) and shows the active support-access grant the
 * backend will validate on each read.
 *
 * It is NEVER authentication: the selection only drives which tenant
 * the X-Support-Tenant-ID header names, and the backend re-validates
 * the operator's grant on every request.
 */
export default function TenantContextBanner() {
  const { data: context } = useTenantContext();
  const setContext = useSetTenantContext();
  const clearContext = useClearTenantContext();
  const grant = useActiveGrantForSelectedTenant();
  const { data: orgs } = useOrganizationsQuery("", 500, 0);
  const [selected, setSelected] = useState("");

  const apply = () => {
    const id = Number.parseInt(selected, 10);
    if (!Number.isFinite(id) || id <= 0) return;
    const org = orgs?.organizations.find((o) => o.id === id);
    setContext.mutate({ tenantId: id, tenantName: org?.name, grant: undefined });
  };

  return (
    <div className="border border-[var(--border)] rounded-lg p-3 bg-[var(--bg-surface)] space-y-2">
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm font-medium text-[var(--text-primary)]">Tenant context</span>
        {context?.tenantId ? (
          <>
            <span className="px-2 py-0.5 rounded text-xs bg-[var(--accent)]/10 text-[var(--accent)]">
              tenant:{context.tenantId}{context.tenantName ? ` · ${context.tenantName}` : ""}
            </span>
            {grant ? (
              <span className="px-2 py-0.5 rounded text-xs bg-[var(--success)]/10 text-[var(--success)]">
                grant #{grant.id} · {grant.scope}
              </span>
            ) : (
              <span className="px-2 py-0.5 rounded text-xs bg-[var(--warning)]/10 text-[var(--warning)]">
                no active grant — reads will be denied
              </span>
            )}
            <button
              onClick={() => clearContext.mutate()}
              className="text-xs text-[var(--text-secondary)] hover:underline"
            >
              Clear context
            </button>
          </>
        ) : (
          <span className="text-xs text-[var(--text-muted)]">No tenant selected — mail-control reads are unavailable.</span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select
          value={selected}
          onChange={(e) => setSelected(e.target.value)}
          aria-label="Select tenant"
          className="px-3 py-1.5 text-sm bg-[var(--bg-base)] border border-[var(--border)] rounded"
        >
          <option value="">Select a tenant…</option>
          {(orgs?.organizations ?? []).map((o) => (
            <option key={o.id} value={o.id}>{o.name} (tenant {o.id})</option>
          ))}
        </select>
        <button
          onClick={apply}
          disabled={!selected || setContext.isPending}
          className="px-3 py-1.5 text-sm bg-[var(--accent)] text-white rounded disabled:opacity-50"
        >
          Open tenant context
        </button>
        <span className="text-xs text-[var(--text-muted)]">
          Requires an active support-access grant for this tenant (Support Access page).
        </span>
      </div>
    </div>
  );
}
