import { useState } from "react";
import { Loader2, AlertCircle, Search } from "lucide-react";
import TenantScopeBanner from "../tenant-context/components/TenantScopeBanner";
import { useTenantScope } from "../tenant-context/queries";
import { usePlatformDomains } from "./queries";
import DomainTable from "./components/DomainTable";
import DomainDetailDrawer from "./components/DomainDetailDrawer";
import PaginationControls from "../components/PaginationControls";
import { DOMAIN_STATUSES } from "./contract";
import { domainStatusLabel } from "./formatters";
import { safeErrorInfo } from "../errors";

const PAGE_SIZE = 25;

/**
 * Platform Domains — platform-wide inventory for one EXPLICIT tenant
 * via /api/v1/platform/domains/:tenant_id. No support-access grant and
 * no X-Support-Tenant-ID: the tenant id is an explicit route-path
 * filter, and the operator's platform identity is the only auth.
 */
export default function DomainsPage() {
  const { data: scope } = useTenantScope();
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(0);
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const tenantId = scope?.tenantId ?? null;

  const listQ = usePlatformDomains(tenantId, {
    q: query || undefined,
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  });

  const domains = listQ.data?.domains ?? [];
  const total = listQ.data?.total ?? 0;

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Domains</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Platform-wide domain inventory per tenant. Tenant ids are explicit filters on the platform routes — no
          support grant is involved.
        </p>
      </div>

      <TenantScopeBanner />

      {tenantId === null ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Select a tenant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Platform domain routes require an explicit target tenant id in the path. No tenant is assumed or derived.
          </p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative flex-1 max-w-sm">
              <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
              <input
                value={query}
                onChange={(e) => { setQuery(e.target.value); setPage(0); }}
                placeholder="Search domain name…"
                aria-label="Search domains"
                className="w-full pl-8 pr-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              />
            </div>
            <label className="text-sm text-[var(--text-secondary)]">
              Status
              <select
                value={statusFilter}
                onChange={(e) => { setStatusFilter(e.target.value); setPage(0); }}
                className="ml-2 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
              >
                <option value="">All</option>
                {DOMAIN_STATUSES.map((s) => (
                  <option key={s} value={s}>{domainStatusLabel(s)}</option>
                ))}
              </select>
            </label>
          </div>

          {listQ.isLoading ? (
            <div className="flex items-center justify-center h-48">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : listQ.error ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-start gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)] shrink-0" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">{safeErrorInfo(listQ.error).title}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(listQ.error).detail}</p>
              </div>
            </div>
          ) : domains.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
              {query || statusFilter ? "No domains match the current filters." : `No domains for tenant ${tenantId}.`}
            </div>
          ) : (
            <>
              <DomainTable
                domains={domains}
                onSelect={(d) => setSelectedId(d.id)}
              />
              <PaginationControls page={page} pageSize={PAGE_SIZE} total={total} onChange={setPage} />
            </>
          )}
        </>
      )}

      {tenantId !== null && selectedId !== null && (
        <DomainDetailDrawer tenantId={tenantId} id={selectedId} onClose={() => setSelectedId(null)} />
      )}
    </div>
  );
}
