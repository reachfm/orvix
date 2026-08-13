import { useState } from "react";
import { Loader2, AlertCircle, Search } from "lucide-react";
import { useOrganizationsQuery } from "./queries";
import OrganizationsTable from "./components/OrganizationsTable";
import OrganizationDetailDrawer from "./components/OrganizationDetailDrawer";

const PAGE_SIZE = 25;

export default function OrganizationsPage() {
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<number | null>(null);

  const listQ = useOrganizationsQuery(search, PAGE_SIZE, page * PAGE_SIZE);
  const orgs = listQ.data?.organizations ?? [];
  const total = listQ.data?.total ?? 0;
  const hasNextPage = (page + 1) * PAGE_SIZE < total;

  return (
    <div>
      <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-4">Organizations</h2>

      <div className="flex items-center gap-2 mb-4">
        <div className="relative flex-1 max-w-sm">
          <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)]" />
          <input
            value={search}
            onChange={(e) => { setSearch(e.target.value); setPage(0); }}
            placeholder="Search name, slug, or domain…"
            className="w-full pl-8 pr-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
          />
        </div>
      </div>

      {listQ.isLoading ? (
        <div className="flex items-center justify-center h-48"><Loader2 size={24} className="text-[var(--accent)] animate-spin" /></div>
      ) : listQ.error ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3">
          <AlertCircle size={20} className="text-[var(--danger)]" />
          <span className="text-[var(--danger)] text-sm">Failed to load organizations: {(listQ.error as Error).message}</span>
        </div>
      ) : orgs.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">
          {search ? "No organizations match this search." : "No organizations found."}
        </div>
      ) : (
        <>
          <OrganizationsTable organizations={orgs} onSelect={setSelected} />
          <div className="flex items-center justify-between mt-3 text-sm text-[var(--text-secondary)]">
            <span>{total} organization{total === 1 ? "" : "s"}</span>
            <div className="flex gap-2">
              <button
                disabled={page === 0}
                onClick={() => setPage((p) => Math.max(0, p - 1))}
                className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-40"
              >
                Previous
              </button>
              <button
                disabled={!hasNextPage}
                onClick={() => setPage((p) => p + 1)}
                className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-40"
              >
                Next
              </button>
            </div>
          </div>
        </>
      )}

      {selected !== null && <OrganizationDetailDrawer id={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
