import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { X, ShieldAlert, Paperclip, Loader2, AlertCircle } from "lucide-react";
import { listSupportViewFolders, listSupportViewMessages, getSupportViewMessage, supportViewAttachmentUrl } from "../api";
import { useEndMailboxSupportViewMutation } from "../mutations";
import type { StartMailboxSupportViewResponse } from "../contract";
import { safeErrorInfo } from "../../errors";

function minutesRemaining(expiresAt: string): number {
  const ms = new Date(expiresAt).getTime() - Date.now();
  return Math.max(0, Math.ceil(ms / 60000));
}

/**
 * Read-only support mailbox viewer. The operator remains authenticated
 * as themselves (their own platform admin session/cookie is never
 * touched) — this component only ever calls the audited
 * /support-view/:session_id/* read routes. There is deliberately NO
 * compose/reply/forward/delete/move/mark-read control anywhere in this
 * component — the backend exposes no write route in this family, and
 * the UI must never fabricate one, not even a disabled-looking button.
 */
export default function SupportMailboxViewer({
  tenantId,
  mailboxId,
  session,
  onClose,
}: {
  tenantId: number;
  mailboxId: number;
  session: StartMailboxSupportViewResponse;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const endMut = useEndMailboxSupportViewMutation(tenantId);
  const [selectedFolderId, setSelectedFolderId] = useState<number | null>(null);
  const [selectedMessageId, setSelectedMessageId] = useState<number | null>(null);
  const [remaining, setRemaining] = useState(() => minutesRemaining(session.expires_at));
  const [ended, setEnded] = useState(false);

  useEffect(() => {
    const t = setInterval(() => setRemaining(minutesRemaining(session.expires_at)), 30_000);
    return () => clearInterval(t);
  }, [session.expires_at]);

  const foldersQuery = useQuery({
    queryKey: ["support-view", "folders", tenantId, mailboxId, session.session_id],
    queryFn: () => listSupportViewFolders(tenantId, mailboxId, session.session_id),
    enabled: !ended,
    retry: false,
  });

  const messagesQuery = useQuery({
    queryKey: ["support-view", "messages", tenantId, mailboxId, session.session_id, selectedFolderId],
    queryFn: () => listSupportViewMessages(tenantId, mailboxId, session.session_id, { folderId: selectedFolderId ?? undefined }),
    enabled: !ended,
    retry: false,
  });

  const messageDetailQuery = useQuery({
    queryKey: ["support-view", "message", tenantId, mailboxId, session.session_id, selectedMessageId],
    queryFn: () => getSupportViewMessage(tenantId, mailboxId, session.session_id, selectedMessageId as number),
    enabled: !ended && selectedMessageId !== null,
    retry: false,
  });

  const expired = remaining <= 0;

  function endSession() {
    endMut.mutate(
      { id: mailboxId, sessionId: session.session_id },
      {
        onSettled: () => {
          setEnded(true);
          qc.removeQueries({ queryKey: ["support-view"] });
        },
      },
    );
  }

  const folders = foldersQuery.data?.folders ?? [];
  const messages = messagesQuery.data?.messages ?? [];
  const detail = messageDetailQuery.data;

  const bannerText = useMemo(() => {
    if (ended) return "Support access ended.";
    if (expired) return "Support access expired.";
    return `Expires in ${remaining} min`;
  }, [ended, expired, remaining]);

  return (
    <div className="fixed inset-0 z-50 bg-[var(--bg-base)] flex flex-col" role="dialog" aria-modal="true" aria-label="Support mailbox viewer">
      {/* Persistent, non-dismissible warning banner. */}
      <div className="flex items-center justify-between gap-3 px-4 py-2.5 bg-[var(--warning)]/15 border-b border-[var(--warning)]/40 text-sm">
        <div className="flex items-center gap-2 text-[var(--text-primary)]">
          <ShieldAlert size={16} className="text-[var(--warning)] shrink-0" />
          <span className="font-medium">Support access</span>
          <span className="text-[var(--text-secondary)]">· Viewing: {session.email}</span>
          <span className="text-[var(--text-secondary)]">· Read-only</span>
          <span className="text-[var(--text-secondary)]" data-testid="support-view-expiry">{bannerText}</span>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={endSession}
            disabled={endMut.isPending || ended}
            className="px-3 py-1.5 text-sm rounded bg-[var(--danger)] text-white disabled:opacity-40"
          >
            {endMut.isPending ? "Ending…" : "End access"}
          </button>
          <button
            type="button"
            aria-label="Close support viewer"
            onClick={onClose}
            className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          >
            <X size={18} />
          </button>
        </div>
      </div>

      {ended || expired ? (
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center">
            <AlertCircle size={28} className="text-[var(--text-secondary)] mx-auto mb-2" />
            <p className="text-sm text-[var(--text-primary)]">
              {ended ? "This support session has ended." : "This support session has expired."}
            </p>
            <button type="button" onClick={onClose} className="mt-3 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white">
              Close
            </button>
          </div>
        </div>
      ) : (
        <div className="flex-1 flex overflow-hidden">
          {/* Folder sidebar */}
          <aside className="w-56 border-r border-[var(--border)] overflow-y-auto p-2" aria-label="Folders">
            {foldersQuery.isLoading ? (
              <Loader2 size={16} className="animate-spin text-[var(--accent)] m-2" />
            ) : (
              <ul className="space-y-1">
                <li>
                  <button
                    type="button"
                    onClick={() => setSelectedFolderId(null)}
                    className={`w-full text-left px-2 py-1.5 text-sm rounded ${selectedFolderId === null ? "bg-[var(--accent)]/10 text-[var(--accent)]" : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"}`}
                  >
                    All folders
                  </button>
                </li>
                {folders.map((f) => (
                  <li key={f.id}>
                    <button
                      type="button"
                      onClick={() => setSelectedFolderId(f.id)}
                      className={`w-full text-left px-2 py-1.5 text-sm rounded flex justify-between ${selectedFolderId === f.id ? "bg-[var(--accent)]/10 text-[var(--accent)]" : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"}`}
                    >
                      <span>{f.name}</span>
                      {f.unread_count > 0 && <span className="text-xs">{f.unread_count}</span>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </aside>

          {/* Message list */}
          <section className="w-80 border-r border-[var(--border)] overflow-y-auto" aria-label="Messages">
            {messagesQuery.isLoading ? (
              <div className="p-4"><Loader2 size={16} className="animate-spin text-[var(--accent)]" /></div>
            ) : messages.length === 0 ? (
              <p className="p-4 text-sm text-[var(--text-secondary)]">No messages.</p>
            ) : (
              <ul>
                {messages.map((m) => (
                  <li key={m.id}>
                    <button
                      type="button"
                      onClick={() => setSelectedMessageId(m.id)}
                      className={`w-full text-left px-3 py-2.5 border-b border-[var(--border)] text-sm ${selectedMessageId === m.id ? "bg-[var(--accent)]/10" : "hover:bg-[var(--bg-elevated)]"}`}
                    >
                      <div className="font-medium text-[var(--text-primary)] truncate">{m.subject || "(no subject)"}</div>
                      <div className="text-xs text-[var(--text-secondary)] truncate">{m.from_address}</div>
                      <div className="text-xs text-[var(--text-muted)]">{new Date(m.received_date).toLocaleString()}</div>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>

          {/* Message detail */}
          <section className="flex-1 overflow-y-auto p-4" aria-label="Message detail">
            {selectedMessageId === null ? (
              <p className="text-sm text-[var(--text-secondary)]">Select a message to view it.</p>
            ) : messageDetailQuery.isLoading ? (
              <Loader2 size={16} className="animate-spin text-[var(--accent)]" />
            ) : messageDetailQuery.isError ? (
              <p className="text-sm text-[var(--danger)]">{safeErrorInfo(messageDetailQuery.error).detail}</p>
            ) : detail ? (
              <div>
                <h2 className="text-base font-semibold text-[var(--text-primary)]">{detail.message.subject || "(no subject)"}</h2>
                <p className="text-xs text-[var(--text-secondary)] mt-1">From: {detail.message.from_address}</p>
                <p className="text-xs text-[var(--text-secondary)]">To: {detail.message.to_addresses}</p>
                <p className="text-xs text-[var(--text-muted)]">{new Date(detail.message.received_date).toLocaleString()}</p>
                {detail.attachments.length > 0 && (
                  <div className="mt-3 space-y-1">
                    {detail.attachments.map((a) => (
                      <a
                        key={a.id}
                        href={supportViewAttachmentUrl(tenantId, mailboxId, session.session_id, detail.message.id, a.id)}
                        className="flex items-center gap-2 text-sm text-[var(--accent)] hover:underline"
                      >
                        <Paperclip size={14} /> {a.filename} ({Math.round(a.size_bytes / 1024)} KB)
                      </a>
                    ))}
                  </div>
                )}
                <pre className="mt-4 whitespace-pre-wrap break-words text-xs bg-[var(--bg-elevated)] border border-[var(--border)] rounded p-3">
                  {detail.raw_rfc822}
                </pre>
              </div>
            ) : null}
          </section>
        </div>
      )}
    </div>
  );
}
