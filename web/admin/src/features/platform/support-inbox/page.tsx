import { useEffect, useMemo, useState } from "react";
import {
  MessageSquare,
  Search,
  ChevronRight,
  Send,
  Loader2,
  AlertCircle,
  X,
} from "lucide-react";
import {
  useTickets,
  useTicketDetail,
  useTicketReply,
  useTicketStatusChange,
} from "./queries";
import {
  SUPPORT_TICKET_CATEGORIES,
  SUPPORT_TICKET_STATUSES,
  statusTone,
  type SupportTicket,
  type SupportTicketStatus,
} from "./contract";

const PAGE_SIZE = 50;

function StatusBadge({ status }: { status: SupportTicketStatus }) {
  const tone = statusTone(status);
  const label = status.replace(/_/g, " ");
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${tone.bg} ${tone.fg}`}>
      {label}
    </span>
  );
}

function PriorityBadge({ priority }: { priority: string }) {
  const tone =
    priority === "urgent"
      ? "bg-[var(--danger)]/10 text-[var(--danger)]"
      : priority === "high"
        ? "bg-[var(--warning)]/10 text-[var(--warning)]"
        : "bg-[var(--bg-subtle)] text-[var(--text-secondary)]";
  return <span className={`px-2 py-0.5 rounded text-xs ${tone}`}>{priority}</span>;
}

function TicketRow({
  ticket,
  selected,
  onSelect,
}: {
  ticket: SupportTicket;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`w-full text-left p-3 rounded border transition-colors ${
        selected
          ? "border-[var(--accent)] bg-[var(--accent)]/5"
          : "border-[var(--border)] bg-[var(--bg-surface)] hover:bg-[var(--bg-elevated)]"
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="text-xs text-[var(--text-muted)] font-mono">
              {ticket.reference_id}
            </span>
            <StatusBadge status={ticket.status} />
            <PriorityBadge priority={ticket.priority} />
          </div>
          <div className="text-sm font-medium text-[var(--text-primary)] truncate mt-1">
            {ticket.subject}
          </div>
          <div className="text-xs text-[var(--text-secondary)] truncate mt-1">
            {ticket.user_email} · tenant {ticket.tenant_id} · {ticket.category}
          </div>
        </div>
        <ChevronRight size={16} className="text-[var(--text-muted)] mt-1 shrink-0" />
      </div>
    </button>
  );
}

function TicketDetail({
  ref,
  onClose,
}: {
  ref: string;
  onClose: () => void;
}) {
  const { data, isLoading, error } = useTicketDetail(ref);
  const reply = useTicketReply();
  const statusChange = useTicketStatusChange();
  const [replyBody, setReplyBody] = useState("");
  const [targetStatus, setTargetStatus] = useState<SupportTicketStatus | "">("");

  useEffect(() => {
    setReplyBody("");
    setTargetStatus("");
  }, [ref]);

  const onSendReply = () => {
    if (!replyBody.trim() || reply.isPending) return;
    reply.mutate(
      { ref, body: replyBody.trim() },
      { onSuccess: () => setReplyBody("") },
    );
  };

  const onChangeStatus = () => {
    if (!targetStatus || statusChange.isPending) return;
    statusChange.mutate({ ref, status: targetStatus as SupportTicketStatus });
  };

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)] p-6">
        <Loader2 className="w-4 h-4 animate-spin" /> Loading ticket…
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="p-6 space-y-2">
        <div className="flex items-center gap-2 text-sm text-[var(--danger)]">
          <AlertCircle size={16} />
          {(error as Error)?.message ?? "Ticket not found"}
        </div>
        <button onClick={onClose} className="text-xs text-[var(--accent)] hover:underline">
          Back to inbox
        </button>
      </div>
    );
  }

  const ticket = data.ticket;
  const messages = data.messages;

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-start justify-between gap-3 p-4 border-b border-[var(--border)]">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="text-xs font-mono text-[var(--text-muted)]">
              {ticket.reference_id}
            </span>
            <StatusBadge status={ticket.status} />
            <PriorityBadge priority={ticket.priority} />
            <span className="text-xs text-[var(--text-secondary)]">
              {ticket.user_email} · tenant {ticket.tenant_id}
            </span>
          </div>
          <h3 className="text-base font-semibold text-[var(--text-primary)] mt-2">
            {ticket.subject}
          </h3>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Created {new Date(ticket.created_at).toLocaleString()} ·{" "}
            {ticket.category}
          </p>
        </div>
        <button onClick={onClose} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]" aria-label="Close">
          <X size={18} />
        </button>
      </div>

      <div className="flex-1 overflow-auto p-4 space-y-4">
        <section className="bg-[var(--bg-base)] border border-[var(--border)] rounded p-3">
          <div className="text-xs text-[var(--text-muted)] mb-1">
            {ticket.user_email} · {new Date(ticket.created_at).toLocaleString()}
          </div>
          <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap">
            {ticket.description}
          </p>
        </section>

        {messages.length === 0 ? (
          <p className="text-xs text-[var(--text-muted)] italic">
            No replies yet — be the first to respond.
          </p>
        ) : (
          messages.map((m) => (
            <section
              key={m.id}
              className={`border rounded p-3 ${
                m.author_kind === "platform"
                  ? "bg-[var(--accent)]/5 border-[var(--accent)]/30"
                  : "bg-[var(--bg-base)] border-[var(--border)]"
              }`}
            >
              <div className="text-xs text-[var(--text-muted)] mb-1">
                <strong className="text-[var(--text-primary)]">
                  {m.author_kind === "platform" ? "Platform" : "Tenant"}
                </strong>
                {" · "}
                {m.author_email} · {new Date(m.created_at).toLocaleString()}
              </div>
              <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap">{m.body}</p>
            </section>
          ))
        )}
      </div>

      <div className="border-t border-[var(--border)] p-4 space-y-3 bg-[var(--bg-surface)]">
        {ticket.status !== "closed" && ticket.status !== "resolved" && (
          <div>
            <textarea
              value={replyBody}
              onChange={(e) => setReplyBody(e.target.value)}
              placeholder="Type a reply as Platform…"
              rows={3}
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] resize-none"
            />
            <div className="flex items-center justify-between mt-2">
              <div className="text-xs text-[var(--text-muted)]">
                Reply drives the canonical status transition automatically.
              </div>
              <button
                onClick={onSendReply}
                disabled={!replyBody.trim() || reply.isPending}
                className="flex items-center gap-2 bg-[var(--accent)] text-white rounded px-3 py-1.5 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50"
              >
                {reply.isPending ? (
                  <>
                    <Loader2 className="w-4 h-4 animate-spin" /> Sending…
                  </>
                ) : (
                  <>
                    <Send className="w-4 h-4" /> Reply
                  </>
                )}
              </button>
            </div>
            {reply.error && (
              <p className="text-xs text-[var(--danger)] mt-2">
                {(reply.error as Error).message}
              </p>
            )}
          </div>
        )}

        <div className="flex items-center gap-2 pt-2 border-t border-[var(--border)]">
          <select
            value={targetStatus}
            onChange={(e) => setTargetStatus(e.target.value as SupportTicketStatus)}
            className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            disabled={ticket.status === "closed"}
          >
            <option value="">Change status to…</option>
            {SUPPORT_TICKET_STATUSES.filter((s) => s !== ticket.status).map((s) => (
              <option key={s} value={s}>
                {s.replace(/_/g, " ")}
              </option>
            ))}
          </select>
          <button
            onClick={onChangeStatus}
            disabled={!targetStatus || statusChange.isPending || ticket.status === "closed"}
            className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm hover:bg-[var(--bg-subtle)] disabled:opacity-50"
          >
            {statusChange.isPending ? "Updating…" : "Update"}
          </button>
        </div>
        {statusChange.error && (
          <p className="text-xs text-[var(--danger)]">
            {(statusChange.error as Error).message}
          </p>
        )}
      </div>
    </div>
  );
}

export default function SupportInboxPage() {
  const [status, setStatus] = useState<SupportTicketStatus | "">("");
  const [category, setCategory] = useState<string>("");
  const [search, setSearch] = useState<string>("");
  const [page, setPage] = useState<number>(0);
  const [selectedRef, setSelectedRef] = useState<string | null>(null);

  const params = useMemo(
    () => ({
      limit: PAGE_SIZE,
      offset: page * PAGE_SIZE,
      status: status || undefined,
      category: category || undefined,
      search: search || undefined,
    }),
    [page, status, category, search],
  );

  const { data, isLoading, error } = useTickets(params);

  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Support Inbox</h2>
        <p className="text-sm text-[var(--text-secondary)] mt-1">
          Every support ticket across every tenant. Real round-trip persistence, real
          status transitions.
        </p>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-3 flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[200px]">
          <Search
            size={14}
            className="absolute left-2 top-1/2 -translate-y-1/2 text-[var(--text-muted)]"
          />
          <input
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(0);
            }}
            placeholder="Search subject or reference…"
            className="w-full pl-8 pr-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
          />
        </div>
        <select
          value={status}
          onChange={(e) => {
            setStatus(e.target.value as SupportTicketStatus | "");
            setPage(0);
          }}
          className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
        >
          <option value="">All statuses</option>
          {SUPPORT_TICKET_STATUSES.map((s) => (
            <option key={s} value={s}>
              {s.replace(/_/g, " ")}
            </option>
          ))}
        </select>
        <select
          value={category}
          onChange={(e) => {
            setCategory(e.target.value);
            setPage(0);
          }}
          className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
        >
          <option value="">All categories</option>
          {SUPPORT_TICKET_CATEGORIES.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label}
            </option>
          ))}
        </select>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="space-y-2">
          {isLoading ? (
            <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)] p-3">
              <Loader2 className="w-4 h-4 animate-spin" /> Loading tickets…
            </div>
          ) : error ? (
            <div className="text-sm text-[var(--danger)] p-3">
              {(error as Error).message}
            </div>
          ) : !data || data.entries.length === 0 ? (
            <div className="text-sm text-[var(--text-muted)] p-3">
              <MessageSquare className="w-4 h-4 inline mr-1" /> No tickets match the current filters.
            </div>
          ) : (
            <>
              {data.entries.map((t) => (
                <TicketRow
                  key={t.id}
                  ticket={t}
                  selected={t.reference_id === selectedRef}
                  onSelect={() => setSelectedRef(t.reference_id)}
                />
              ))}
              {totalPages > 1 && (
                <div className="flex items-center justify-between pt-2 text-xs text-[var(--text-muted)]">
                  <span>
                    Page {page + 1} of {totalPages} ({total} tickets)
                  </span>
                  <div className="flex gap-2">
                    <button
                      onClick={() => setPage((p) => Math.max(0, p - 1))}
                      disabled={page === 0}
                      className="px-3 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-50"
                    >
                      Prev
                    </button>
                    <button
                      onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
                      disabled={page >= totalPages - 1}
                      className="px-3 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded disabled:opacity-50"
                    >
                      Next
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>

        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg min-h-[400px]">
          {selectedRef ? (
            <TicketDetail ref={selectedRef} onClose={() => setSelectedRef(null)} />
          ) : (
            <div className="flex items-center justify-center h-full p-6 text-sm text-[var(--text-muted)]">
              <MessageSquare className="w-4 h-4 inline mr-2" /> Select a ticket to view detail and reply.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
