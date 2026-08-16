import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import * as Tabs from "@radix-ui/react-tabs";
import { X, Loader2, AlertCircle, Copy, Check, AlertTriangle } from "lucide-react";
import StatusBadge from "../../components/StatusBadge";
import { usePlatformDomain, useDomainDNSCache } from "../queries";
import { useSetDomainStatusMutation } from "../mutations";
import { domainStatusLabel, domainStatusTone, formatTimestamp } from "../formatters";
import { DOMAIN_STATUSES } from "../contract";
import { safeErrorInfo } from "../../errors";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-sm text-[var(--text-primary)] mt-0.5 break-words">{children}</dd>
    </div>
  );
}

function useCopy() {
  const [copiedField, setCopiedField] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const copy = (field: string, value: string, label?: string) => {
    void navigator.clipboard?.writeText(value).catch(() => {});
    setCopiedField(field);
    setAnnouncement(`${label ?? field} copied to clipboard`);
    window.setTimeout(() => setCopiedField((f) => (f === field ? null : f)), 2000);
  };
  return { copiedField, copy, announcement };
}

function CopyButton({
  field,
  value,
  copiedField,
  onCopy,
  label,
}: {
  field: string;
  value: string;
  copiedField: string | null;
  onCopy: (field: string, value: string, label?: string) => void;
  label: string;
}) {
  return (
    <button
      type="button"
      aria-label={`Copy ${label}`}
      onClick={() => onCopy(field, value, label)}
      className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] shrink-0"
    >
      {copiedField === field ? <Check size={14} /> : <Copy size={14} />}
    </button>
  );
}

/**
 * Domain detail: Overview / DNS Setup / DKIM / Lifecycle tabs.
 *
 * DNS Setup and DKIM are backed ONLY by the one-time domain-creation
 * response (see queries.ts's dnsCache) — GetPlatformDomain never
 * re-exposes DNS requirements or the public DKIM TXT value, and there
 * is no generate/rotate route for an EXISTING domain
 * (internal/api/router.go's /platform/domains/:tenant_id family has
 * only list/detail/create/status/mail-access-mode/deactivate). Both
 * tabs are honest about this: they show the cache when present (a
 * domain just created in this session) and a clear "not available"
 * state otherwise — never a fabricated or re-derived value.
 *
 * Lifecycle intentionally does NOT wire the deactivate route
 * (POST .../deactivate): it requires expected_version, and
 * GetPlatformDomain's response has no version field to source that
 * value from safely — see the final report for this exact gap.
 *
 * PRODUCT DECISION: mail_access_mode is a MAILBOX-level policy, not a
 * domain-level one, in this frontend — it is set and changed on the
 * mailbox create/detail views only.
 */
export default function DomainDetailDrawer({
  tenantId,
  id,
  onClose,
  initialTab,
}: {
  tenantId: number;
  id: number;
  onClose: () => void;
  initialTab?: "overview" | "dns" | "dkim" | "lifecycle";
}) {
  const { data: domain, isLoading, isError, error, refetch } = usePlatformDomain(tenantId, id);
  const dnsCache = useDomainDNSCache(tenantId, id);
  const statusMut = useSetDomainStatusMutation(tenantId);
  const [statusDraft, setStatusDraft] = useState("");
  const [mutationError, setMutationError] = useState<unknown>(null);
  const { copiedField, copy, announcement } = useCopy();

  const submitting = statusMut.isPending;

  const allRecordsText = dnsCache?.dns_requirements
    ? dnsCache.dns_requirements.map((r) => `${r.name} ${r.type} ${r.priority ? r.priority + " " : ""}${r.value}`).join("\n") +
      (dnsCache.dkim ? `\n${dnsCache.dkim.dns_record_name} TXT ${dnsCache.dkim.public_dns_txt}` : "")
    : "";

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content
          className="fixed right-0 top-0 h-full w-full sm:max-w-2xl bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 flex flex-col"
          aria-describedby={undefined}
        >
          <div aria-live="polite" className="sr-only">{announcement}</div>

          <div className="flex items-start justify-between gap-4 px-6 pt-6 pb-4 border-b border-[var(--border)] shrink-0">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)] break-all">
              {domain ? domain.name : "Domain detail"}
            </Dialog.Title>
            <Dialog.Close aria-label="Close domain detail" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)] shrink-0">
              <X size={20} />
            </Dialog.Close>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError || !domain ? (
            <div className="p-6">
              <div className="border border-[var(--danger)]/30 rounded-xl p-6 flex items-start gap-3" role="alert">
                <AlertCircle size={20} className="text-[var(--danger)] shrink-0" />
                <div className="flex-1">
                  <p className="text-[var(--danger)] text-sm font-medium">Failed to load domain</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
                  <button
                    type="button"
                    onClick={() => refetch()}
                    className="mt-3 px-3 py-1.5 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
                  >
                    Retry
                  </button>
                </div>
              </div>
            </div>
          ) : (
            <Tabs.Root defaultValue={initialTab ?? "overview"} className="flex flex-col flex-1 min-h-0">
              <Tabs.List className="flex gap-1 px-6 pt-3 border-b border-[var(--border)] shrink-0 overflow-x-auto" aria-label="Domain detail sections">
                {[
                  { value: "overview", label: "Overview" },
                  { value: "dns", label: "DNS Setup" },
                  { value: "dkim", label: "DKIM" },
                  { value: "lifecycle", label: "Lifecycle" },
                ].map((t) => (
                  <Tabs.Trigger
                    key={t.value}
                    value={t.value}
                    className="px-3 py-2 text-sm font-medium text-[var(--text-secondary)] border-b-2 border-transparent data-[state=active]:border-[var(--accent)] data-[state=active]:text-[var(--text-primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)] whitespace-nowrap"
                  >
                    {t.label}
                  </Tabs.Trigger>
                ))}
              </Tabs.List>

              <div className="flex-1 overflow-y-auto p-6">
                <Tabs.Content value="overview" className="space-y-6 outline-none">
                  <dl className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                    <Field label="Tenant">#{domain.tenant_id}</Field>
                    <Field label="Status">
                      <StatusBadge tone={domainStatusTone(domain.status)} label={`Status ${domainStatusLabel(domain.status)}`}>
                        {domainStatusLabel(domain.status)}
                      </StatusBadge>
                    </Field>
                    <Field label="Plan">{domain.plan || "—"}</Field>
                    <Field label="Mailboxes">{domain.mailbox_count}</Field>
                    <Field label="Aliases">{domain.alias_count}</Field>
                    <Field label="DKIM">
                      {domain.dkim_enabled
                        ? `Enabled${domain.dkim_selector ? ` · selector ${domain.dkim_selector}` : ""}`
                        : "Not enabled"}
                    </Field>
                    <Field label="DMARC">{domain.dmarc_enabled ? "Enabled" : "Not enabled"}</Field>
                    <Field label="Created">{formatTimestamp(domain.created_at)}</Field>
                    <Field label="Updated">{formatTimestamp(domain.updated_at)}</Field>
                  </dl>
                </Tabs.Content>

                <Tabs.Content value="dns" className="space-y-4 outline-none">
                  {dnsCache?.dns_requirements && dnsCache.dns_requirements.length > 0 ? (
                    <>
                      <div className="flex items-center justify-between gap-2">
                        <p className="text-xs text-[var(--text-secondary)]">
                          Records returned when this domain was created. The platform does not re-expose DNS
                          records for an existing domain, and does not perform DNS verification from the browser —
                          publish these with your DNS provider and verify with standard DNS tools.
                        </p>
                        <button
                          type="button"
                          onClick={() => copy("all-records", allRecordsText, "all DNS records")}
                          className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] shrink-0"
                        >
                          {copiedField === "all-records" ? <Check size={12} /> : <Copy size={12} />}
                          Copy all
                        </button>
                      </div>
                      <ul className="space-y-2">
                        {dnsCache.dns_requirements.map((rec, i) => {
                          const field = `dns-${i}`;
                          return (
                            <li key={field} className="border border-[var(--border)] rounded-lg p-3">
                              <div className="flex flex-wrap items-center gap-2 mb-1.5">
                                {rec.purpose && (
                                  <span className="text-xs font-semibold text-[var(--text-primary)]">{rec.purpose}</span>
                                )}
                                <StatusBadge tone={rec.required ? "warning" : "neutral"}>
                                  {rec.required ? "Required" : "Optional"}
                                </StatusBadge>
                              </div>
                              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
                                <dt className="text-[var(--text-secondary)]">Type</dt>
                                <dd className="text-[var(--text-primary)] font-mono flex items-center gap-2">
                                  {rec.type}
                                  <CopyButton field={`${field}-type`} value={rec.type} copiedField={copiedField} onCopy={copy} label={`${rec.purpose ?? rec.type} record type`} />
                                </dd>
                                <dt className="text-[var(--text-secondary)]">Host</dt>
                                <dd className="text-[var(--text-primary)] font-mono break-all flex items-center gap-2">
                                  <span className="break-all">{rec.name}</span>
                                  <CopyButton field={`${field}-name`} value={rec.name} copiedField={copiedField} onCopy={copy} label={`${rec.purpose ?? rec.type} record host`} />
                                </dd>
                                <dt className="text-[var(--text-secondary)]">Value</dt>
                                <dd className="text-[var(--text-primary)] font-mono break-all flex items-start gap-2">
                                  <span className="break-all whitespace-pre-wrap">{rec.value}</span>
                                  <CopyButton field={`${field}-value`} value={rec.value} copiedField={copiedField} onCopy={copy} label={`${rec.purpose ?? rec.type} record value`} />
                                </dd>
                                {rec.priority !== undefined && (
                                  <>
                                    <dt className="text-[var(--text-secondary)]">Priority</dt>
                                    <dd className="text-[var(--text-primary)]">{rec.priority}</dd>
                                  </>
                                )}
                                {rec.ttl > 0 && (
                                  <>
                                    <dt className="text-[var(--text-secondary)]">TTL</dt>
                                    <dd className="text-[var(--text-primary)]">{rec.ttl}s</dd>
                                  </>
                                )}
                              </dl>
                            </li>
                          );
                        })}
                      </ul>
                      {dnsCache.dns_next_step && (
                        <p className="text-xs text-[var(--text-secondary)]">{dnsCache.dns_next_step}</p>
                      )}
                    </>
                  ) : (
                    <div className="border border-[var(--border)] rounded-lg p-4 flex items-start gap-3">
                      <AlertTriangle size={16} className="text-[var(--text-muted)] shrink-0 mt-0.5" />
                      <div>
                        <p className="text-sm font-medium text-[var(--text-primary)]">DNS records unavailable</p>
                        <p className="text-xs text-[var(--text-secondary)] mt-1">
                          The platform only returns DNS records at the moment a domain is created — there is no
                          route to re-fetch them for an existing domain. DKIM is{" "}
                          {domain.dkim_enabled ? "enabled" : "not enabled"} on this domain
                          {domain.dkim_selector ? ` (selector ${domain.dkim_selector})` : ""}.
                        </p>
                      </div>
                    </div>
                  )}
                </Tabs.Content>

                <Tabs.Content value="dkim" className="space-y-4 outline-none">
                  <div className="flex items-center gap-2">
                    <StatusBadge tone={domain.dkim_enabled ? "success" : "neutral"}>
                      {domain.dkim_enabled ? "Configured" : "Not configured"}
                    </StatusBadge>
                    {domain.dkim_selector && (
                      <span className="text-sm text-[var(--text-secondary)]">Selector: {domain.dkim_selector}</span>
                    )}
                  </div>

                  {dnsCache?.dkim ? (
                    <div className="border border-[var(--border)] rounded-lg p-3">
                      <p className="text-xs text-[var(--text-secondary)] mb-2">
                        Public DNS TXT record returned when this domain was created. This is the ONLY time the
                        platform returns it — it cannot be re-fetched later. The private key is never shown, stored
                        by this console, or requested by any control here.
                      </p>
                      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
                        <dt className="text-[var(--text-secondary)]">Hostname</dt>
                        <dd className="text-[var(--text-primary)] font-mono break-all flex items-center gap-2">
                          <span className="break-all">{dnsCache.dkim.dns_record_name}</span>
                          <CopyButton field="dkim-host" value={dnsCache.dkim.dns_record_name} copiedField={copiedField} onCopy={copy} label="DKIM hostname" />
                        </dd>
                        <dt className="text-[var(--text-secondary)]">TXT value</dt>
                        <dd className="text-[var(--text-primary)] font-mono break-all whitespace-pre-wrap flex items-start gap-2">
                          <span className="break-all whitespace-pre-wrap">{dnsCache.dkim.public_dns_txt}</span>
                          <CopyButton field="dkim-value" value={dnsCache.dkim.public_dns_txt} copiedField={copiedField} onCopy={copy} label="DKIM TXT value" />
                        </dd>
                      </dl>
                    </div>
                  ) : (
                    <div className="border border-[var(--border)] rounded-lg p-4 flex items-start gap-3">
                      <AlertTriangle size={16} className="text-[var(--text-muted)] shrink-0 mt-0.5" />
                      <div>
                        <p className="text-sm font-medium text-[var(--text-primary)]">Public DKIM record unavailable</p>
                        <p className="text-xs text-[var(--text-secondary)] mt-1">
                          The platform returns the public DKIM TXT record only in the domain-creation response, and
                          there is no generate or rotate route for an existing domain on this route family — DKIM
                          can only be set up at the moment a domain is created.
                        </p>
                      </div>
                    </div>
                  )}
                </Tabs.Content>

                <Tabs.Content value="lifecycle" className="space-y-4 outline-none">
                  <section aria-label="Domain status" className="border border-[var(--border)] rounded-lg p-4">
                    <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-1">Lifecycle status</h3>
                    <p className="text-xs text-[var(--text-secondary)] mb-3">
                      <strong className="text-[var(--text-primary)]">Active</strong> accepts mail normally.{" "}
                      <strong className="text-[var(--text-primary)]">Disabled</strong> and{" "}
                      <strong className="text-[var(--text-primary)]">Suspended</strong> stop the domain from
                      accepting or sending mail, distinctly from each other on the backend. The writable set from
                      this console is: {DOMAIN_STATUSES.map(domainStatusLabel).join(", ")}.
                    </p>
                    <div className="flex flex-wrap items-center gap-2">
                      <label className="sr-only" htmlFor="lifecycle-status-select">New domain status</label>
                      <select
                        id="lifecycle-status-select"
                        aria-label="New domain status"
                        value={statusDraft}
                        onChange={(e) => setStatusDraft(e.target.value)}
                        className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                      >
                        <option value="">— Choose status —</option>
                        {DOMAIN_STATUSES.filter((s) => s !== domain.status).map((s) => (
                          <option key={s} value={s}>{domainStatusLabel(s)}</option>
                        ))}
                      </select>
                      <button
                        type="button"
                        disabled={!statusDraft || submitting}
                        onClick={() =>
                          statusMut.mutate({ id: domain.id, status: statusDraft }, {
                            onSuccess: () => setStatusDraft(""),
                            onError: (e) => setMutationError(e),
                          })
                        }
                        className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                      >
                        {statusMut.isPending && <Loader2 size={14} className="inline animate-spin mr-1.5" />}
                        {statusMut.isPending ? "Saving…" : "Apply status"}
                      </button>
                    </div>
                    <p className="text-xs text-[var(--text-muted)] mt-3">
                      Applied only after the backend confirms it — enforcement is server-side, not client-side.
                    </p>
                  </section>

                  {mutationError !== null && (
                    <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                      <p className="text-[var(--danger)] font-medium">{safeErrorInfo(mutationError).title}</p>
                      <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(mutationError).detail}</p>
                    </div>
                  )}
                </Tabs.Content>
              </div>
            </Tabs.Root>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
