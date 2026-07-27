import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { MessageSquare, BookOpen, HeartPulse, ChevronDown, ChevronRight, ExternalLink, Send, Check, Loader2 } from "lucide-react";
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

export default function SupportPage() {
  const [category, setCategory] = useState("general");
  const [subject, setSubject] = useState("");
  const [message, setMessage] = useState("");
  const [expanded, setExpanded] = useState<number | null>(null);
  const [referenceId, setReferenceId] = useState<string | null>(null);

  const submitRequest = useMutation({
    mutationFn: () => api.submitSupportRequest({ category, subject, message }),
    onSuccess: (data: any) => {
      setReferenceId(data.reference_id || data.id || null);
      setSubject("");
      setMessage("");
      setCategory("general");
    },
  });

  const handleReset = () => {
    setReferenceId(null);
    setSubject("");
    setMessage("");
    setCategory("general");
    submitRequest.reset();
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Support</h2>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <MessageSquare className="w-5 h-5 text-[var(--accent-blue)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">Contact Support</h3>
          </div>

          {referenceId ? (
            <div className="text-center py-6">
              <Check size={32} className="text-[var(--status-success)] mx-auto mb-3" />
              <p className="text-[var(--text-primary)] font-medium">Request Submitted</p>
              <p className="text-sm text-[var(--text-secondary)] mt-1">
                Reference ID: <span className="text-[var(--accent-blue)] font-mono">{referenceId}</span>
              </p>
              <p className="text-sm text-[var(--text-secondary)] mt-1">We'll get back to you soon.</p>
              <button onClick={handleReset}
                className="mt-4 text-sm text-[var(--accent-blue)] hover:underline">Send another request</button>
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
                <textarea required rows={4} value={message} onChange={(e) => setMessage(e.target.value)}
                  className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm resize-none" />
              </div>
              <button type="submit"
                disabled={submitRequest.isPending}
                className="flex items-center gap-2 bg-[var(--accent-blue)] text-[var(--text-primary)] rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
                {submitRequest.isPending ? (
                  <><Loader2 className="w-4 h-4 animate-spin" /> Submitting...</>
                ) : (
                  <><Send className="w-4 h-4" /> Send Request</>
                )}
              </button>
              {submitRequest.error && (
                <p className="text-[var(--status-danger)] text-sm">{(submitRequest.error as any)?.message || "Failed to submit request"}</p>
              )}
            </form>
          )}
        </div>

        <div className="space-y-6">
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <BookOpen className="w-5 h-5 text-[var(--accent-blue)]" />
              <h3 className="text-lg font-medium text-[var(--text-primary)]">Documentation</h3>
            </div>
            <div className="space-y-2">
              {docs.map((doc) => (
                <a key={doc.label} href={doc.url} target="_blank" rel="noopener noreferrer"
                  className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded hover:bg-[var(--bg-shell)] transition-colors group">
                  <span className="text-sm text-[var(--text-primary)]">{doc.label}</span>
                  <ExternalLink size={14} className="text-[var(--text-muted)] group-hover:text-[var(--accent-blue)] transition-colors" />
                </a>
              ))}
            </div>
          </div>

          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <HeartPulse className="w-5 h-5 text-[var(--accent-blue)]" />
              <h3 className="text-lg font-medium text-[var(--text-primary)]">System Status</h3>
            </div>
            <p className="text-sm text-[var(--text-secondary)]">System status information is not available.</p>
            <p className="text-xs text-[var(--text-muted)] mt-1">
              If you experience issues, please submit a support request using the form.
            </p>
          </div>
        </div>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
        <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">Frequently Asked Questions</h3>
        <div className="space-y-2">
          {faqItems.map((item, idx) => (
            <div key={idx} className="bg-[var(--bg-base)] rounded-lg overflow-hidden">
              <button onClick={() => setExpanded(expanded === idx ? null : idx)}
                className="w-full flex items-center justify-between p-4 text-left hover:bg-[var(--bg-shell)] transition-colors">
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
