import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, useMemo, useRef, Fragment } from "react";
import {
  CheckCircle, XCircle, AlertTriangle, Plus, Trash2, RefreshCw, Search,
  ChevronRight, ChevronDown, Globe, ChevronLeft,
} from "lucide-react";
import { api, domainErrorMessage } from "../api";
import DNSRecordsModal from "./DNSRecordsModal";

/**
 * Mirrors admin/domain.AdminDomain as returned by GET /enterprise/domains.
 * storage_used_bytes / message_count / dns_health / dns_score /
 * dns_last_checked_at are REAL aggregates computed in that endpoint's single
 * batched query — never placeholders, and never re-fetched per row from here.
 */
interface EnterpriseDomain {
  id: number;
  name: string;
  status: string;
  plan?: string;
  description?: string;
  max_mailboxes?: number;
  max_aliases?: number;
  max_quota_mb?: number;
  dkim_enabled?: boolean;
  dkim_selector?: string;
  dmarc_enabled?: boolean;
  mailbox_count?: number;
  alias_count?: number;
  storage_used_bytes?: number;
  storage_limit_bytes?: number;
  message_count?: number;
  dns_health?: string;
  dns_score?: number;
  dns_last_checked_at?: string;
  created_at?: string;
}

const PAGE_SIZES = [25, 50, 100];

/** Byte formatter for the storage column. 0 renders as "0 B", not "—". */
export function formatBytes(bytes: number | undefined | null): string {
  if (bytes == null) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value >= 100 || i === 0 ? Math.round(value) : value.toFixed(1)} ${units[i]}`;
}

function formatCount(n: number | undefined | null): string {
  if (n == null) return "—";
  return n.toLocaleString();
}

/** "used / limit", where a limit of 0 or absent means unlimited. */
function UsageCell({ used, limit }: { used?: number; limit?: number }) {
  const u = used ?? 0;
  const over = limit != null && limit > 0 && u >= limit;
  return (
    <span className={over ? "text-[#FBBF24]" : "text-[#8B92A8]"}>
      {formatCount(u)}
      <span className="text-[#555D73]"> / {limit != null && limit > 0 ? formatCount(limit) : "∞"}</span>
    </span>
  );
}

/**
 * DNS health for one row.
 *
 * A domain that has never been checked has NO score — not a score of zero.
 * Rendering an absent score as "0%" would read as "this domain failed every
 * check", which is a different and false claim. So the score is shown only
 * when a check has actually run AND the backend reports a numeric score;
 * every other case renders an explicit textual state instead.
 */
function DNSHealthCell({ d }: { d: EnterpriseDomain }) {
  const health = (d.dns_health || "").toLowerCase();
  if (!d.dns_last_checked_at) {
    return <span className="text-[#555D73]">Not checked</span>;
  }
  const color =
    health === "pass" ? "#34D399"
      : health === "warning" ? "#FBBF24"
      : health === "fail" ? "#F87171"
      : "#8B92A8";
  return (
    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
      <span className="w-1.5 h-1.5 rounded-full shrink-0" style={{ backgroundColor: color }} />
      <span style={{ color }}>
        {typeof d.dns_score === "number" ? `${d.dns_score}%` : "Unavailable"}
      </span>
      <span className="text-[#555D73] capitalize">{health || "unknown"}</span>
    </span>
  );
}

function BoolBadge({ on, label }: { on?: boolean; label: string }) {
  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] ${
        on ? "bg-[#34D399]/10 text-[#34D399]" : "bg-[#555D73]/10 text-[#8B92A8]"
      }`}
    >
      {on ? <CheckCircle size={9} /> : <XCircle size={9} />}
      {label}
    </span>
  );
}

function StatusBadge({ status }: { status: string }) {
  const isActive = status === "active";
  const isSuspended = status === "suspended";
  return (
    <span
      className={`inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] rounded-full ${
        isActive
          ? "bg-[#34D399]/10 text-[#34D399]"
          : isSuspended
          ? "bg-[#F87171]/10 text-[#F87171]"
          : "bg-[#FBBF24]/10 text-[#FBBF24]"
      }`}
    >
      {isActive ? <CheckCircle size={9} /> : isSuspended ? <XCircle size={9} /> : <AlertTriangle size={9} />}
      {status || "unknown"}
    </span>
  );
}

function errorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object" && "code" in (err as any) && typeof (err as any).code === "string") {
    return domainErrorMessage((err as any).code, (err as any).message || fallback);
  }
  return (err as Error)?.message || fallback;
}

export default function Domains() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(25);
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [showCreate, setShowCreate] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<EnterpriseDomain | null>(null);
  /** The domain whose DNS modal is open. Set ONLY by the dedicated DNS action. */
  const [dnsDomain, setDnsDomain] = useState<EnterpriseDomain | null>(null);
  /** Focus is returned to the exact DNS button that opened the modal. */
  const dnsButtonRefs = useRef<Record<number, HTMLButtonElement | null>>({});

  const { data, isLoading, isFetching, error, refetch } = useQuery({
    queryKey: ["enterprise-domains"],
    queryFn: () => api.listDomainsEnterprise(),
    // Keep the rendered table during background refetches instead of blanking it.
    placeholderData: (prev: any) => prev,
  });

  const createMutation = useMutation({
    mutationFn: (name: string) => api.createDomainEnterprise({ name }),
    onSuccess: () => {
      setShowCreate(false);
      setNewDomain("");
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteDomainEnterprise(id),
    onSuccess: () => {
      setConfirmDelete(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const raw = (data as any)?.domains ?? (Array.isArray(data) ? data : []);
  const items: EnterpriseDomain[] = Array.isArray(raw) ? raw : [];

  const filtered = useMemo(
    () => items.filter((d) => !search || (d.name || "").toLowerCase().includes(search.toLowerCase())),
    [items, search]
  );
  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, pageCount - 1);
  const visible = filtered.slice(currentPage * pageSize, currentPage * pageSize + pageSize);

  const toggle = (set: Set<number>, id: number) => {
    const next = new Set(set);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return next;
  };

  if (isLoading && items.length === 0) return <p className="text-[#8B92A8]">Loading…</p>;
  if (error && items.length === 0) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-[#F87171]">{errorMessage(error, "Failed to load domains.")}</p>
        <button onClick={() => refetch()} className="text-sm text-[#4F7CFF] hover:underline text-left">
          Retry
        </button>
      </div>
    );
  }

  return (
    <div>
      {/* ── Header ── */}
      <div className="flex flex-wrap items-center gap-3 mb-4">
        <h2 className="text-lg font-semibold text-[#E8EAF0]">
          Domains <span className="text-[#555D73] font-normal text-sm">({filtered.length})</span>
        </h2>

        <div className="relative ml-auto">
          <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[#555D73]" />
          <input
            type="text"
            placeholder="Search domains…"
            aria-label="Search domains"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setPage(0); }}
            className="pl-8 pr-3 py-1.5 w-56 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs"
          />
        </div>

        <button
          onClick={() => refetch()}
          aria-label="Refresh domains"
          className="inline-flex items-center gap-1.5 px-2.5 py-1.5 border border-[#2A2F3E] rounded-lg text-[#8B92A8] text-xs hover:text-[#E8EAF0] hover:bg-[#1A1E26]"
        >
          <RefreshCw size={13} className={isFetching ? "animate-spin" : ""} /> Refresh
        </button>

        <button
          onClick={() => setShowCreate((v) => !v)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 bg-[#4F7CFF] text-white rounded-lg text-xs hover:bg-[#3B5FD9]"
        >
          <Plus size={13} /> Add Domain
        </button>
      </div>

      {showCreate && (
        <form
          onSubmit={(e) => { e.preventDefault(); if (newDomain.trim()) createMutation.mutate(newDomain.trim()); }}
          className="flex gap-2 mb-3 bg-[#13161C] border border-[#2A2F3E] rounded-lg p-2.5"
        >
          <input
            type="text"
            required
            placeholder="example.com"
            value={newDomain}
            onChange={(e) => setNewDomain(e.target.value)}
            aria-label="New domain name"
            className="flex-1 px-3 py-1.5 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs font-mono"
          />
          <button
            type="submit"
            disabled={createMutation.isPending}
            className="px-3 py-1.5 bg-[#4F7CFF] text-white rounded-lg text-xs hover:bg-[#3B5FD9] disabled:opacity-50"
          >
            {createMutation.isPending ? "Adding…" : "Add"}
          </button>
        </form>
      )}
      {createMutation.isError && (
        <p className="text-[#F87171] text-xs mb-3" role="alert">
          {errorMessage(createMutation.error, "Failed to add domain.")}
        </p>
      )}

      {/* ── Table ── */}
      {filtered.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <Globe size={20} className="text-[#555D73] mx-auto mb-2" />
          <p className="text-[#8B92A8] text-sm">
            {items.length === 0 ? "No domains configured yet." : "No domains match your search."}
          </p>
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full text-xs border-collapse">
              <thead>
                <tr className="border-b border-[#2A2F3E] bg-[#0F1218]">
                  <th className="w-6 px-1 py-2" />
                  <th className="text-left font-medium text-[#8B92A8] px-3 py-2">Domain</th>
                  <th className="text-right font-medium text-[#8B92A8] px-3 py-2">Aliases</th>
                  <th className="text-right font-medium text-[#8B92A8] px-3 py-2">Mailboxes</th>
                  <th className="text-right font-medium text-[#8B92A8] px-3 py-2">Storage</th>
                  <th className="text-right font-medium text-[#8B92A8] px-3 py-2">Messages</th>
                  <th className="text-left font-medium text-[#8B92A8] px-3 py-2">DKIM</th>
                  <th className="text-left font-medium text-[#8B92A8] px-3 py-2">DMARC</th>
                  <th className="text-left font-medium text-[#8B92A8] px-3 py-2">DNS health</th>
                  <th className="text-left font-medium text-[#8B92A8] px-3 py-2">Status</th>
                  <th className="text-right font-medium text-[#8B92A8] px-3 py-2">Actions</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((d) => (
                  <Fragment key={d.id}>
                    <tr className="border-b border-[#222736] hover:bg-[#161A21]">
                      <td className="px-1 py-2">
                        <button
                          onClick={() => setExpanded((s) => toggle(s, d.id))}
                          aria-label={`${expanded.has(d.id) ? "Collapse" : "Expand"} ${d.name}`}
                          aria-expanded={expanded.has(d.id)}
                          className="text-[#8B92A8] hover:text-[#E8EAF0]"
                        >
                          {expanded.has(d.id) ? <ChevronDown size={13} /> : <ChevronRight size={13} />}
                        </button>
                      </td>
                      <td className="px-3 py-2 font-mono text-[#E8EAF0]">{d.name}</td>
                      <td className="px-3 py-2 text-right">
                        <UsageCell used={d.alias_count} limit={d.max_aliases} />
                      </td>
                      <td className="px-3 py-2 text-right">
                        <UsageCell used={d.mailbox_count} limit={d.max_mailboxes} />
                      </td>
                      <td className="px-3 py-2 text-right text-[#8B92A8] whitespace-nowrap">
                        {formatBytes(d.storage_used_bytes)}
                        <span className="text-[#555D73]">
                          {" / "}
                          {d.storage_limit_bytes ? formatBytes(d.storage_limit_bytes) : "∞"}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right text-[#8B92A8]">{formatCount(d.message_count)}</td>
                      <td className="px-3 py-2">
                        <BoolBadge on={d.dkim_enabled} label={d.dkim_enabled ? "Enabled" : "Off"} />
                      </td>
                      <td className="px-3 py-2">
                        <BoolBadge on={d.dmarc_enabled} label={d.dmarc_enabled ? "Enabled" : "Off"} />
                      </td>
                      <td className="px-3 py-2">
                        <DNSHealthCell d={d} />
                      </td>
                      <td className="px-3 py-2">
                        <StatusBadge status={d.status} />
                      </td>
                      <td className="px-3 py-2">
                        <div className="flex items-center justify-end gap-1.5">
                          {/*
                            Dedicated DNS action. The modal opens ONLY from here —
                            clicking anywhere else on the row must never open it.
                          */}
                          <button
                            ref={(el) => { dnsButtonRefs.current[d.id] = el; }}
                            onClick={() => setDnsDomain(d)}
                            aria-label={`Open DNS records for ${d.name}`}
                            className="px-2 py-1 rounded border border-[#2A2F3E] text-[#4F7CFF] hover:bg-[#1A1E26]"
                          >
                            DNS
                          </button>
                          <button
                            onClick={() => setConfirmDelete(d)}
                            aria-label={`Delete ${d.name}`}
                            className="p-1 rounded text-[#F87171] hover:bg-[#1A1E26]"
                          >
                            <Trash2 size={13} />
                          </button>
                        </div>
                      </td>
                    </tr>
                    {expanded.has(d.id) && (
                      <tr className="border-b border-[#222736] bg-[#0F1218]">
                        <td colSpan={11} className="px-10 py-3">
                          <dl className="grid grid-cols-2 sm:grid-cols-4 gap-x-6 gap-y-1.5 text-[11px]">
                            <div>
                              <dt className="text-[#555D73]">Plan</dt>
                              <dd className="text-[#8B92A8]">{d.plan || "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-[#555D73]">DKIM selector</dt>
                              <dd className="text-[#8B92A8] font-mono">{d.dkim_selector || "—"}</dd>
                            </div>
                            <div>
                              <dt className="text-[#555D73]">DNS last checked</dt>
                              <dd className="text-[#8B92A8]">
                                {d.dns_last_checked_at
                                  ? new Date(d.dns_last_checked_at).toLocaleString()
                                  : "Never"}
                              </dd>
                            </div>
                            <div>
                              <dt className="text-[#555D73]">Quota</dt>
                              <dd className="text-[#8B92A8]">
                                {d.max_quota_mb ? `${formatCount(d.max_quota_mb)} MB` : "Unlimited"}
                              </dd>
                            </div>
                            {d.description && (
                              <div className="col-span-2 sm:col-span-4">
                                <dt className="text-[#555D73]">Description</dt>
                                <dd className="text-[#8B92A8]">{d.description}</dd>
                              </div>
                            )}
                          </dl>
                        </td>
                      </tr>
                    )}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>

          {/* ── Pagination ── */}
          <div className="flex flex-wrap items-center gap-3 px-3 py-2 border-t border-[#2A2F3E] text-[11px] text-[#8B92A8]">
            <label className="flex items-center gap-1.5">
              Rows
              <select
                aria-label="Rows per page"
                value={pageSize}
                onChange={(e) => { setPageSize(Number(e.target.value)); setPage(0); }}
                className="bg-[#1A1E26] border border-[#2A2F3E] rounded px-1.5 py-1 text-[#E8EAF0]"
              >
                {PAGE_SIZES.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </label>
            <div className="ml-auto flex items-center gap-2">
              <span>
                Page {currentPage + 1} of {pageCount}
              </span>
              <button
                onClick={() => setPage((p) => Math.max(0, p - 1))}
                disabled={currentPage === 0}
                aria-label="Previous page"
                className="p-1 rounded border border-[#2A2F3E] disabled:opacity-40 hover:bg-[#1A1E26]"
              >
                <ChevronLeft size={13} />
              </button>
              <button
                onClick={() => setPage((p) => Math.min(pageCount - 1, p + 1))}
                disabled={currentPage >= pageCount - 1}
                aria-label="Next page"
                className="p-1 rounded border border-[#2A2F3E] disabled:opacity-40 hover:bg-[#1A1E26]"
              >
                <ChevronRight size={13} />
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ── DNS modal ── */}
      {dnsDomain && (
        <DNSRecordsModal
          key={dnsDomain.id}
          domain={dnsDomain}
          onClose={() => {
            const id = dnsDomain.id;
            setDnsDomain(null);
            // Return focus to the DNS button that opened the dialog.
            setTimeout(() => dnsButtonRefs.current[id]?.focus(), 0);
          }}
        />
      )}

      {/* ── Delete confirmation ── */}
      {confirmDelete && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="delete-domain-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5 w-96 max-w-full">
            <h3 id="delete-domain-title" className="text-sm font-semibold text-[#E8EAF0] mb-2">
              Delete Domain
            </h3>
            <p className="text-xs text-[#8B92A8] mb-5">
              Permanently delete <span className="text-[#E8EAF0] font-mono">{confirmDelete.name}</span>?{" "}
              {(confirmDelete.mailbox_count ?? 0) > 0
                ? "This domain still has mailboxes — delete them first, then remove the domain."
                : "This cannot be undone."}
            </p>
            {deleteMutation.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(deleteMutation.error, "Deletion failed.")}
              </p>
            )}
            <div className="flex gap-2 justify-end">
              <button
                onClick={() => setConfirmDelete(null)}
                className="px-3 py-1.5 text-xs text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]"
              >
                Cancel
              </button>
              <button
                onClick={() => { if (!deleteMutation.isPending) deleteMutation.mutate(confirmDelete.id); }}
                disabled={deleteMutation.isPending}
                className="px-3 py-1.5 text-xs rounded-lg bg-[#F87171] text-white hover:bg-red-500 disabled:opacity-50"
              >
                {deleteMutation.isPending ? "Deleting…" : "Delete Domain"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
