// Shared bounded pagination controls used by every paginated platform
// inventory (domains, mailboxes, aliases, groups, suppressions,
// deliverability events, relays). Deterministic offset/limit paging
// matching the backend list envelopes.
export default function PaginationControls({
  page,
  pageSize,
  total,
  onChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  onChange: (page: number) => void;
}) {
  const hasNext = (page + 1) * pageSize < total;
  return (
    <div className="flex items-center justify-between mt-3 text-sm text-[var(--text-secondary)]">
      <span>
        {total} item{total === 1 ? "" : "s"}
      </span>
      <div className="flex gap-2">
        <button
          type="button"
          disabled={page === 0}
          onClick={() => onChange(Math.max(0, page - 1))}
          className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-40"
        >
          Previous
        </button>
        <button
          type="button"
          disabled={!hasNext}
          onClick={() => onChange(page + 1)}
          className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-40"
        >
          Next
        </button>
      </div>
    </div>
  );
}
