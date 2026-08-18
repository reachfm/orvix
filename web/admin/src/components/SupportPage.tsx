import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  MessageSquare,
  BookOpen,
  HeartPulse,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  Send,
  Check,
  Loader2,
  X,
} from "lucide-react";
import { api } from "../api";

const categories = [
  { value: "general", label: "General" },
  { value: "billing", label: "Billing" },
  { value: "technical", label: "Technical" },
  { value: "security", label: "Security" },
];

const docs = [
  { label: "Getting Started Guide", url: "https://github.com/reachfm/orvix/tree/main/docs/getting-started" },
  { label: "Domain Configuration", url: "https://github.com/reachfm/orvix/tree/main/docs/domains" },
  { label: "Mailbox Management", url: "https://github.com/reachfm/orvix/tree/main/docs/mailboxes" },
  { label: "DNS Setup Guide", url: "https://github.com/reachfm/orvix/tree/main/docs/dns" },
  { label: "API Reference", url: "https://github.com/reachfm/orvix/tree/main/docs/api" },
  { label: "Security Best Practices", url: "https://github.com/reachfm/orvix/tree/main/docs/security" },
];

const faqItems = [
  { q: "How do I add a new domain?", a: "Navigate to the Domains page and use the Add Domain button. Follow the DNS verification wizard to configure MX, SPF, DKIM, and DMARC records." },
  { q: "How do I create a new mailbox?", a: "Go to Mailboxes and click Create. Enter the email address, password, and assign a quota. The mailbox is ready immediately after creation." },
  { q: "What plans are available?", a: "Visit the Billing page to see all available plans. Each plan includes different limits for mailboxes, domains, storage, and send volume." },
  { q: "How do I reset my password?", a: "Go to Account Settings or Security page, enter your current password and a new password, then click Update Password." },
  { q: "Is two-factor authentication supported?", a: "Yes. Visit the Security page to enable MFA using any standard TOTP authenticator app like Google Authenticator or Authy." },
  { q: "How do I view my billing invoices?", a: "Go to the Invoices page to see your current plan, usage summary, and billing history. Invoice PDF download is available from the invoice details view." },
];

const STATUS_TONE: Record<string, { bg: string; fg: string }> = {
  open: { bg: "bg-[var(--accent)]/10", fg: "text-[var(--accent)]" },
  in_progress: { bg: "bg-[var(--warning)]/10", fg: "text-[var(--warning)]" },
  waiting_for_customer: { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-secondary)]" },
  customer_replied: { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-secondary)]" },
  resolved: { bg: "bg-[var(--success)]/10", fg: "text-[var(--success)]" },
  closed: { bg: "bg-[var(--bg-subtle)]", fg: "text-[var(--text-muted)]" },
};

function StatusBadge({ status }: { status: string }) {
  const tone = STATUS_TONE[status] ?? STATUS_TONE.closed;
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${tone.bg} ${tone.fg}`}>
      {status.replace(/_/g, " ")}
    </span>
  );
}

export default function SupportPage() {
  const qc = useQueryClient();
  const [category, setCategory] = useState("general");
  const [subject, setSubject] = useState("");
  const [description, setDescription] = useState("");
  const [expanded, setExpanded] = useState<number | null>(null);
  const [justSubmittedRef, setJustSubmittedRef] = useState<string | null>(null);
  const [selectedRef, setSelectedRef] = useState<string | null>(null);
  const [replyBody, setReplyBody] = useState("");

  const ticketsQuery = useQuery({
    queryKey: ["own-support-tickets"],
    queryFn: () => api.listOwnSupportRequests({ limit: 50 }),
  });
  const detailQuery = useQuery({
    queryKey: ["own-support-ticket", selectedRef],
    queryFn: () => api.getOwnSupportRequest(selectedRef as string),
    enabled: !!selectedRef,
  });
  const reply = useMutation({
    mutationFn: ({ ref, body }: { ref: string; body: string }) =>
      api.replyOnOwnSupportRequest(ref, body),
    onSuccess: (_d, vars) => {
      setReplyBody("");
      void qc.invalidateQueries({ queryKey: ["own-support-ticket", vars.ref] });
      void qc.invalidateQueries({ queryKey: ["own-support-tickets"] });
    },
  });

  const submitRequest = useMutation({
    mutationFn: () =>
      api.submitSupportRequest({ category, subject, description, priority: "normal" }),
    onSuccess: (data: any) => {
      const ref = data?.reference_id || data?.id || null;
      setJustSubmittedRef(ref);
      setSubject("");
      setDescription("");
      setCategory("general");
      void qc.invalidateQueries({ queryKey: ["own-support-tickets"] });
    },
  });

  const handleReset = () => {
    setJustSubmittedRef(null);
    setSubject("");
    setDescription("");
    setCategory("general");
    submitRequest.reset();
  };

  const tickets: any[] = ticketsQuery.data?.entries ?? [];
  const messages: any[] = detailQuery.data?.messages ?? [];

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Support</h2>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <MessageSquare className="w-5 h-5 text-[var(--accent)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">Contact Support</h3>
          </div>

          {justSubmittedRef ? (
            <div className="text-center py-6">
              <Check size={32} className="text-[var(--success)] mx-auto mb-3" />
              <p className="text-[var(--text-primary)] font-medium">Request Submitted</p>
              <p className="text-sm text-[var(--text-secondary)] mt-1">
                Reference ID: <span className="text-[var(--accent)] font-mono">{justSubmittedRef}</span>
              </p>
              <p className="text-sm text-[var(--text-secondary)] mt-1">We'll get back to you soon.</p>
              <button onClick={handleReset}
                className="mt-4 text-sm text-[var(--accent)] hover:underline">Send another request</button>
            </div>
          ) : (
            <form
              onSubmit={(e) => { e.preventDefault(); submitRequest.mutate(); }}
              className="space-y-3"
            >
              <div>
                <label className="block text-sm text-[var(--text-secondary)] mb-1">Category</label>
                <select value={category} onChange={(e) => setCategory(e.target.value)}
                  className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm">
                  {categories.map((c) => (
                    <option key={c.value} value={c.value}>{c.label}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm text-[var(--text-secondary)] mb-1">Subject</label>
                <input required value={subject} onChange={(e) => setSubject(e.target.value)}
                  className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
              </div>
              <div>
                <label className="block text-sm text-[var(--text-secondary)] mb-1">Message</label>
                <textarea required rows={4} value={description} onChange={(e) => setDescription(e.target.value)}
                  className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm resize-none" />
              </div>
              <button type="submit"
                disabled={submitRequest.isPending}
                className="flex items-center gap-2 bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
                {submitRequest.isPending ? (
                  <><Loader2 className="w-4 h-4 animate-spin" /> Submitting...</>
                ) : (
                  <><Send className="w-4 h-4" /> Send Request</>
                )}
              </button>
              {submitRequest.error && (
                <p className="text-[var(--danger)] text-sm">{(submitRequest.error as any)?.message || "Failed to submit request"}</p>
              )}
            </form>
          )}
        </div>

        <div className="space-y-6">
          <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <BookOpen className="w-5 h-5 text-[var(--accent)]" />
              <h3 className="text-lg font-medium text-[var(--text-primary)]">Documentation</h3>
            </div>
            <div className="space-y-2">
              {docs.map((doc) => (
                <a key={doc.label} href={doc.url} target="_blank" rel="noopener noreferrer"
                  className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded hover:bg-[var(--bg-elevated)] transition-colors group">
                  <span className="text-sm text-[var(--text-primary)]">{doc.label}</span>
                  <ExternalLink size={14} className="text-[var(--text-muted)] group-hover:text-[var(--accent)] transition-colors" />
                </a>
              ))}
            </div>
          </div>

          <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <HeartPulse className="w-5 h-5 text-[var(--accent)]" />
              <h3 className="text-lg font-medium text-[var(--text-primary)]">System Status</h3>
            </div>
            <p className="text-sm text-[var(--text-secondary)]">System status information is not available.</p>
            <p className="text-xs text-[var(--text-muted)] mt-1">
              If you experience issues, please submit a support request using the form.
            </p>
          </div>
        </div>
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Your Support Tickets</h3>
        {ticketsQuery.isLoading ? (
          <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
            <Loader2 className="w-4 h-4 animate-spin" /> Loading your tickets…
          </div>
        ) : tickets.length === 0 ? (
          <p className="text-sm text-[var(--text-muted)]">
            You haven't opened any support tickets yet.
          </p>
        ) : (
          <div className="space-y-2">
            {tickets.map((t: any) => (
              <button
                key={t.id}
                onClick={() => { setSelectedRef(t.reference_id); setReplyBody(""); }}
                className={`w-full text-left p-3 rounded border transition-colors ${
                  selectedRef === t.reference_id
                    ? "border-[var(--accent)] bg-[var(--accent)]/5"
                    : "border-[var(--border)] bg-[var(--bg-base)] hover:bg-[var(--bg-elevated)]"
                }`}
              >
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-xs text-[var(--text-muted)] font-mono">{t.reference_id}</span>
                  <StatusBadge status={t.status} />
                </div>
                <div className="text-sm font-medium text-[var(--text-primary)] mt-1">{t.subject}</div>
                <div className="text-xs text-[var(--text-secondary)] mt-1">
                  {t.category} · created {new Date(t.created_at).toLocaleString()}
                </div>
              </button>
            ))}
          </div>
        )}

        {selectedRef && (
          <div className="mt-4 border-t border-[var(--border)] pt-4 space-y-3">
            <div className="flex items-center justify-between">
              <h4 className="text-sm font-semibold text-[var(--text-primary)]">
                Conversation
              </h4>
              <button onClick={() => setSelectedRef(null)} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]">
                <X size={14} />
              </button>
            </div>
            {detailQuery.isLoading ? (
              <div className="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
                <Loader2 className="w-4 h-4 animate-spin" /> Loading…
              </div>
            ) : (
              <>
                <div className="bg-[var(--bg-base)] border border-[var(--border)] rounded p-3">
                  <div className="text-xs text-[var(--text-muted)] mb-1">
                    You · {new Date(detailQuery.data?.ticket?.created_at ?? "").toLocaleString()}
                  </div>
                  <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap">
                    {detailQuery.data?.ticket?.description ?? ""}
                  </p>
                </div>
                {messages.map((m: any) => (
                  <div
                    key={m.id}
                    className={`border rounded p-3 ${
                      m.author_kind === "platform"
                        ? "bg-[var(--accent)]/5 border-[var(--accent)]/30"
                        : "bg-[var(--bg-base)] border-[var(--border)]"
                    }`}
                  >
                    <div className="text-xs text-[var(--text-muted)] mb-1">
                      <strong className="text-[var(--text-primary)]">
                        {m.author_kind === "platform" ? "Orvix Support" : "You"}
                      </strong>
                      {" · "}
                      {new Date(m.created_at).toLocaleString()}
                    </div>
                    <p className="text-sm text-[var(--text-primary)] whitespace-pre-wrap">{m.body}</p>
                  </div>
                ))}
                {(detailQuery.data?.ticket?.status !== "closed" &&
                  detailQuery.data?.ticket?.status !== "resolved") && (
                  <div className="pt-2 border-t border-[var(--border)]">
                    <textarea
                      value={replyBody}
                      onChange={(e) => setReplyBody(e.target.value)}
                      placeholder="Type a follow-up…"
                      rows={3}
                      className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] resize-none"
                    />
                    <div className="flex items-center justify-end mt-2">
                      <button
                        onClick={() =>
                          reply.mutate({ ref: selectedRef, body: replyBody.trim() })
                        }
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
              </>
            )}
          </div>
        )}
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Frequently Asked Questions</h3>
        <div className="space-y-2">
          {faqItems.map((item, idx) => (
            <div key={idx} className="bg-[var(--bg-base)] rounded-lg overflow-hidden">
              <button onClick={() => setExpanded(expanded === idx ? null : idx)}
                className="w-full flex items-center justify-between p-4 text-left hover:bg-[var(--bg-elevated)] transition-colors">
                <span className="text-sm text-[var(--text-primary)]">{item.q}</span>
                {expanded === idx ? <ChevronDown size={16} className="text-[var(--text-secondary)]" /> : <ChevronRight size={16} className="text-[var(--text-secondary)]" />}
              </button>
              {expanded === idx && (
                <div className="px-4 pb-4">
                  <p className="text-sm text-[var(--text-secondary)]">{item.a}</p>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
