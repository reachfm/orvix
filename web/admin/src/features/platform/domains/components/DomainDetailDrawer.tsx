import { useEffect, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import * as Tabs from "@radix-ui/react-tabs";
import { X, Loader2, AlertCircle, Copy, Check, AlertTriangle, ShieldAlert, CheckCircle2, XCircle, RefreshCw } from "lucide-react";
import StatusBadge from "../../components/StatusBadge";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { usePlatformDomain, useDomainDNSCache, usePlatformDomainDNS } from "../queries";
import {
  useSetDomainStatusMutation,
  useGenerateDKIMMutation,
  useRotateDKIMMutation,
  useDeactivateDomainMutation,
  useVerifyDomainDNSMutation,
} from "../mutations";
import { domainStatusLabel, domainStatusTone, formatTimestamp } from "../formatters";
import {
  DOMAIN_STATUSES,
  deactivateDomainConfirmation,
  type PlatformDomainDNSResult,
  type PlatformDNSVerifyRecord,
  type PlatformDNSVerifyStatus,
} from "../contract";
import { safeErrorInfo } from "../../errors";

/**
 * Maps a verify status to operator-facing text/icon/tone. Color is
 * never the only signal — every state carries text and an icon, and
 * StatusBadge renders an accessible status role.
 */
function verifyStatusPresentation(status: PlatformDNSVerifyStatus | undefined, pending: boolean): {
  label: string;
  tone: "success" | "danger" | "warning" | "neutral";
  icon: React.ReactNode;
} {
  if (pending) return { label: "Checking…", tone: "neutral", icon: <Loader2 size={12} className="animate-spin" /> };
  switch (status) {
    case "verified":
      return { label: "Matched", tone: "success", icon: <CheckCircle2 size={12} /> };
    case "missing":
      return { label: "Missing", tone: "danger", icon: <XCircle size={12} /> };
    case "mismatch":
    case "conflict":
    case "multiple_spf":
      return { label: "Mismatch", tone: "danger", icon: <XCircle size={12} /> };
    case "error":
    case "not_found":
      return { label: "Check failed", tone: "warning", icon: <AlertTriangle size={12} /> };
    case "unsupported":
      return { label: "Not checked", tone: "neutral", icon: <AlertTriangle size={12} /> };
    case "not_checked":
    case undefined:
    default:
      return { label: "Not checked", tone: "neutral", icon: <AlertTriangle size={12} /> };
  }
}

function verifyRecordKey(name: string, type: string, purpose?: string): string {
  return `${purpose ?? ""}|${type}|${name}`;
}

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
 * DNS Setup and DKIM are backed by the LIVE existing-domain snapshot
 * (GET /platform/domains/:tenant_id/:id/dns — usePlatformDomainDNS).
 * The one-time domain-creation response cache (queries.ts's dnsCache)
 * is used ONLY as an immediate-paint placeholder right after "View
 * domain" from a just-created domain, while the live fetch is still in
 * flight — the live snapshot always supersedes it once loaded, so a
 * stale creation cache can never override newer server state.
 *
 * DKIM generate/rotate call the real version-guarded backend routes
 * (POST .../dkim/generate, POST .../dkim/rotate) and refresh the live
 * snapshot on success. The backend never returns a private key on any
 * route, and this UI never renders, stores, or requests one.
 *
 * Lifecycle keeps the existing Active/Disabled/Suspended status
 * control, plus a separate danger-zone Deactivate action wired to the
 * canonical audited POST .../deactivate route, using the domain's real
 * current `version` as expected_version and a typed confirmation
 * phrase. This is a soft-delete (status=disabled, deactivated_at set)
 * — never a hard delete.
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
  const dnsQuery = usePlatformDomainDNS(tenantId, id);
  const statusMut = useSetDomainStatusMutation(tenantId);
  const generateDKIMMut = useGenerateDKIMMutation(tenantId);
  const rotateDKIMMut = useRotateDKIMMutation(tenantId);
  const deactivateMut = useDeactivateDomainMutation(tenantId);
  const verifyMut = useVerifyDomainDNSMutation(tenantId);
  const [autoVerifiedFor, setAutoVerifiedFor] = useState<number | null>(null);
  const [activeTab, setActiveTab] = useState<"overview" | "dns" | "dkim" | "lifecycle">(initialTab ?? "overview");
  const [statusDraft, setStatusDraft] = useState("");
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [dkimError, setDkimError] = useState<unknown>(null);
  const [rotateConfirmOpen, setRotateConfirmOpen] = useState(false);
  const [deactivateConfirmOpen, setDeactivateConfirmOpen] = useState(false);
  const [deactivateReason, setDeactivateReason] = useState("");
  const [deactivateError, setDeactivateError] = useState<unknown>(null);
  const { copiedField, copy, announcement } = useCopy();

  const submitting = statusMut.isPending;

  // Immediate-paint placeholder from the creation-response cache,
  // shaped to match the live PlatformDomainDNSResult contract. Only
  // used until the live query resolves.
  const cacheAsDNSResult: PlatformDomainDNSResult | null =
    dnsCache && domain
      ? {
          tenant_id: domain.tenant_id,
          domain_id: domain.id,
          domain: domain.name,
          version: domain.version,
          status: domain.status,
          dkim_configured: !!dnsCache.dkim,
          dkim_selector: dnsCache.dkim?.selector,
          dkim_dns_record_name: dnsCache.dkim?.dns_record_name,
          dkim_public_dns_txt: dnsCache.dkim?.public_dns_txt,
          dns_requirements: dnsCache.dns_requirements,
          dns_next_step: dnsCache.dns_next_step,
        }
      : null;

  // Live snapshot is authoritative; the cache placeholder is used only
  // while it hasn't loaded yet.
  const dns = dnsQuery.data ?? cacheAsDNSResult;

  // Trigger live DNS verification exactly once per opened domain, and
  // ONLY once the operator has actually visited the DNS Setup tab —
  // never merely because the drawer opened. This keeps verification
  // requests from firing on every domain-drawer interaction across the
  // whole app (Overview/DKIM/Lifecycle users never trigger a DNS
  // lookup), matching "when DNS Setup is opened" from spec and
  // avoiding needless network load. "Re-check DNS" reuses the same
  // mutate call and is unaffected by this gate (it's a direct button
  // click, not the auto-trigger).
  useEffect(() => {
    if (
      activeTab === "dns" &&
      domain &&
      dns?.dns_requirements &&
      dns.dns_requirements.length > 0 &&
      autoVerifiedFor !== domain.id
    ) {
      setAutoVerifiedFor(domain.id);
      verifyMut.mutate({ id: domain.id });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, domain?.id, dns?.dns_requirements, autoVerifiedFor]);

  const verifyByKey = new Map<string, PlatformDNSVerifyRecord>();
  verifyMut.data?.records?.forEach((r) => verifyByKey.set(verifyRecordKey(r.name, r.type, r.purpose), r));

  const allRecordsText = dns?.dns_requirements
    ? dns.dns_requirements.map((r) => `${r.name} ${r.type} ${r.priority ? r.priority + " " : ""}${r.value}`).join("\n") +
      (dns.dkim_configured && dns.dkim_dns_record_name && dns.dkim_public_dns_txt
        ? `\n${dns.dkim_dns_record_name} TXT ${dns.dkim_public_dns_txt}`
        : "")
    : "";

  function submitVerifyDNS() {
    if (!domain) return;
    verifyMut.mutate({ id: domain.id });
  }

  function submitGenerateDKIM() {
    if (!domain) return;
    setDkimError(null);
    generateDKIMMut.mutate(
      { id: domain.id, body: { expected_version: domain.version }, idempotencyKey: crypto.randomUUID() },
      { onError: (e) => setDkimError(e) },
    );
  }

  function submitRotateDKIM() {
    if (!domain) return;
    setDkimError(null);
    rotateDKIMMut.mutate(
      {
        id: domain.id,
        body: { confirm_rotation: "rotate-dkim-key", expected_version: domain.version },
        idempotencyKey: crypto.randomUUID(),
      },
      {
        onSuccess: () => setRotateConfirmOpen(false),
        onError: (e) => setDkimError(e),
      },
    );
  }

  function submitDeactivate() {
    if (!domain) return;
    setDeactivateError(null);
    deactivateMut.mutate(
      {
        id: domain.id,
        body: {
          confirm: deactivateDomainConfirmation(domain.id),
          reason: deactivateReason.trim(),
          expected_version: domain.version,
        },
        idempotencyKey: crypto.randomUUID(),
      },
      {
        onSuccess: () => {
          setDeactivateConfirmOpen(false);
          onClose();
        },
        onError: (e) => setDeactivateError(e),
      },
    );
  }

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
            <Tabs.Root
              value={activeTab}
              onValueChange={(v) => setActiveTab(v as "overview" | "dns" | "dkim" | "lifecycle")}
              className="flex flex-col flex-1 min-h-0"
            >
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
                  {dnsQuery.isLoading && !dns ? (
                    <div className="flex items-center justify-center h-24">
                      <Loader2 size={20} className="text-[var(--accent)] animate-spin" />
                    </div>
                  ) : dnsQuery.isError && !dns ? (
                    <div className="border border-[var(--danger)]/30 rounded-lg p-4" role="alert">
                      <p className="text-sm font-medium text-[var(--danger)]">Failed to load DNS records</p>
                      <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(dnsQuery.error).detail}</p>
                      <button
                        type="button"
                        onClick={() => dnsQuery.refetch()}
                        className="mt-3 px-3 py-1.5 text-sm rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
                      >
                        Retry
                      </button>
                    </div>
                  ) : dns?.dns_requirements && dns.dns_requirements.length > 0 ? (
                    <>
                      {/* Overall DNS health summary — compact, not a dashboard redesign. */}
                      <div
                        role="status"
                        className={`border rounded-lg p-3 flex items-start justify-between gap-3 ${
                          verifyMut.isPending && !verifyMut.data
                            ? "border-[var(--border)]"
                            : verifyMut.data?.all_verified
                              ? "border-[var(--success)]/30"
                              : verifyMut.isError
                                ? "border-[var(--warning)]/30"
                                : verifyMut.data
                                  ? "border-[var(--danger)]/30"
                                  : "border-[var(--border)]"
                        }`}
                      >
                        <div>
                          {verifyMut.isPending && !verifyMut.data ? (
                            <p className="text-sm font-medium text-[var(--text-primary)] flex items-center gap-1.5">
                              <Loader2 size={14} className="animate-spin" /> Checking DNS records…
                            </p>
                          ) : verifyMut.data ? (
                            <>
                              <p
                                className={`text-sm font-medium flex items-center gap-1.5 ${
                                  verifyMut.data.all_verified ? "text-[var(--success)]" : "text-[var(--danger)]"
                                }`}
                              >
                                {verifyMut.data.all_verified ? <CheckCircle2 size={14} /> : <XCircle size={14} />}
                                {verifyMut.data.all_verified ? "All DNS records match" : "DNS configuration needs attention"}
                              </p>
                              <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                                {verifyMut.data.matched_count} of {verifyMut.data.total_count} matched
                                {verifyMut.data.issue_count > 0 ? ` · ${verifyMut.data.issue_count} issue${verifyMut.data.issue_count === 1 ? "" : "s"}` : ""}
                                {" · Checked "}
                                {formatTimestamp(verifyMut.data.checked_at)}
                              </p>
                            </>
                          ) : verifyMut.isError ? (
                            <>
                              <p className="text-sm font-medium text-[var(--warning)] flex items-center gap-1.5">
                                <AlertTriangle size={14} /> DNS verification incomplete
                              </p>
                              <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                                Some records could not be checked: {safeErrorInfo(verifyMut.error).detail}
                              </p>
                            </>
                          ) : (
                            <p className="text-sm font-medium text-[var(--text-secondary)]">Not checked</p>
                          )}
                        </div>
                        <button
                          type="button"
                          disabled={verifyMut.isPending}
                          onClick={submitVerifyDNS}
                          className="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)] shrink-0 disabled:opacity-40"
                        >
                          <RefreshCw size={12} className={verifyMut.isPending ? "animate-spin" : ""} />
                          Re-check DNS
                        </button>
                      </div>

                      <div className="flex items-center justify-between gap-2">
                        <p className="text-xs text-[var(--text-secondary)]">
                          Live DNS records for this domain, checked against real public DNS. Publish any record
                          flagged below with your DNS provider.
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
                        {dns.dns_requirements.map((rec, i) => {
                          const field = `dns-${i}`;
                          const verifyRec = verifyByKey.get(verifyRecordKey(rec.name, rec.type, rec.purpose));
                          const presentation = verifyStatusPresentation(verifyRec?.status, verifyMut.isPending);
                          const showExpectedActual = verifyRec && (verifyRec.status === "mismatch" || verifyRec.status === "conflict" || verifyRec.status === "multiple_spf");
                          const showMissingActual = verifyRec && (verifyRec.status === "missing" || verifyRec.status === "not_found");
                          const showCheckFailedReason = verifyRec && (verifyRec.status === "error");
                          return (
                            <li key={field} className="border border-[var(--border)] rounded-lg p-3">
                              <div className="flex flex-wrap items-center gap-2 mb-1.5">
                                {rec.purpose && (
                                  <span className="text-xs font-semibold text-[var(--text-primary)]">{rec.purpose}</span>
                                )}
                                <StatusBadge tone={rec.required ? "warning" : "neutral"}>
                                  {rec.required ? "Required" : "Optional"}
                                </StatusBadge>
                                <StatusBadge tone={presentation.tone} label={`Verification ${presentation.label}`}>
                                  <span className="inline-flex items-center gap-1">
                                    {presentation.icon}
                                    {presentation.label}
                                  </span>
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
                                <dt className="text-[var(--text-secondary)]">{showExpectedActual || showMissingActual ? "Expected" : "Value"}</dt>
                                <dd className="text-[var(--text-primary)] font-mono break-all flex items-start gap-2">
                                  <span className="break-all whitespace-pre-wrap">{rec.value}</span>
                                  <CopyButton field={`${field}-value`} value={rec.value} copiedField={copiedField} onCopy={copy} label={`${rec.purpose ?? rec.type} record value`} />
                                </dd>
                                {showExpectedActual && (
                                  <>
                                    <dt className="text-[var(--danger)]">Actual</dt>
                                    <dd className="text-[var(--danger)] font-mono break-all whitespace-pre-wrap">
                                      {verifyRec!.observed || "—"}
                                    </dd>
                                  </>
                                )}
                                {showMissingActual && (
                                  <>
                                    <dt className="text-[var(--danger)]">Actual</dt>
                                    <dd className="text-[var(--danger)]">Not found</dd>
                                  </>
                                )}
                                {showCheckFailedReason && (
                                  <>
                                    <dt className="text-[var(--warning)]">Reason</dt>
                                    <dd className="text-[var(--warning)]">{verifyRec!.reason || "DNS lookup failed"}</dd>
                                  </>
                                )}
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
                      {dns.dns_next_step && (
                        <p className="text-xs text-[var(--text-secondary)]">{dns.dns_next_step}</p>
                      )}
                    </>
                  ) : (
                    <div className="border border-[var(--border)] rounded-lg p-4 flex items-start gap-3">
                      <AlertTriangle size={16} className="text-[var(--text-muted)] shrink-0 mt-0.5" />
                      <div>
                        <p className="text-sm font-medium text-[var(--text-primary)]">No DNS requirement records returned</p>
                        <p className="text-xs text-[var(--text-secondary)] mt-1">
                          The live DNS snapshot for this domain returned no requirement records. DKIM is{" "}
                          {domain.dkim_enabled ? "enabled" : "not enabled"} on this domain
                          {domain.dkim_selector ? ` (selector ${domain.dkim_selector})` : ""}.
                        </p>
                      </div>
                    </div>
                  )}
                </Tabs.Content>

                <Tabs.Content value="dkim" className="space-y-4 outline-none">
                  <div className="flex items-center gap-2">
                    <StatusBadge tone={dns?.dkim_configured ? "success" : "neutral"}>
                      {dns?.dkim_configured ? "Configured" : "Not configured"}
                    </StatusBadge>
                    {dns?.dkim_selector && (
                      <span className="text-sm text-[var(--text-secondary)]">Selector: {dns.dkim_selector}</span>
                    )}
                  </div>

                  {dnsQuery.isLoading && !dns ? (
                    <div className="flex items-center justify-center h-24">
                      <Loader2 size={20} className="text-[var(--accent)] animate-spin" />
                    </div>
                  ) : dns?.dkim_configured && dns.dkim_dns_record_name && dns.dkim_public_dns_txt ? (
                    <>
                      <div className="border border-[var(--border)] rounded-lg p-3">
                        <p className="text-xs text-[var(--text-secondary)] mb-2">
                          Current public DKIM DNS TXT record. The private key is never shown, stored by this
                          console, or requested by any control here.
                        </p>
                        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
                          <dt className="text-[var(--text-secondary)]">Hostname</dt>
                          <dd className="text-[var(--text-primary)] font-mono break-all flex items-center gap-2">
                            <span className="break-all">{dns.dkim_dns_record_name}</span>
                            <CopyButton field="dkim-host" value={dns.dkim_dns_record_name} copiedField={copiedField} onCopy={copy} label="DKIM hostname" />
                          </dd>
                          <dt className="text-[var(--text-secondary)]">TXT value</dt>
                          <dd className="text-[var(--text-primary)] font-mono break-all whitespace-pre-wrap flex items-start gap-2">
                            <span className="break-all whitespace-pre-wrap">{dns.dkim_public_dns_txt}</span>
                            <CopyButton field="dkim-value" value={dns.dkim_public_dns_txt} copiedField={copiedField} onCopy={copy} label="DKIM TXT value" />
                          </dd>
                        </dl>
                      </div>
                      <button
                        type="button"
                        onClick={() => setRotateConfirmOpen(true)}
                        className="px-3 py-1.5 text-sm rounded border border-[var(--danger)]/40 text-[var(--danger)] hover:bg-[var(--danger)]/10"
                      >
                        Rotate DKIM
                      </button>
                      <p className="text-xs text-[var(--text-muted)]">
                        Rotation replaces the current key pair; the previously published TXT record stops matching
                        as soon as it completes. Publish the new value before old mail relying on the prior key
                        expires from caches.
                      </p>
                    </>
                  ) : (
                    <div className="space-y-3">
                      <div className="border border-[var(--border)] rounded-lg p-4 flex items-start gap-3">
                        <AlertTriangle size={16} className="text-[var(--text-muted)] shrink-0 mt-0.5" />
                        <div>
                          <p className="text-sm font-medium text-[var(--text-primary)]">DKIM is not configured</p>
                          <p className="text-xs text-[var(--text-secondary)] mt-1">
                            Generate a DKIM key pair for this domain. The public DNS TXT record will appear here as
                            soon as it succeeds — no page refresh required. The private key is never shown, stored,
                            or requested by this console.
                          </p>
                        </div>
                      </div>
                      <button
                        type="button"
                        disabled={generateDKIMMut.isPending || !domain}
                        onClick={submitGenerateDKIM}
                        className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                      >
                        {generateDKIMMut.isPending && <Loader2 size={14} className="inline animate-spin mr-1.5" />}
                        {generateDKIMMut.isPending ? "Generating…" : "Generate DKIM"}
                      </button>
                    </div>
                  )}

                  {dkimError !== null && (
                    <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                      <p className="text-[var(--danger)] font-medium">{safeErrorInfo(dkimError).title}</p>
                      <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(dkimError).detail}</p>
                      {safeErrorInfo(dkimError).code === "CONFLICT" && (
                        <button
                          type="button"
                          onClick={() => { setDkimError(null); refetch(); dnsQuery.refetch(); }}
                          className="mt-2 px-3 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
                        >
                          Reload current state
                        </button>
                      )}
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

                  <section aria-label="Danger zone" className="border border-[var(--danger)]/40 rounded-lg p-4">
                    <h3 className="text-sm font-semibold text-[var(--danger)] mb-1 flex items-center gap-1.5">
                      <ShieldAlert size={16} />
                      Danger zone
                    </h3>
                    <p className="text-xs text-[var(--text-secondary)] mb-3">
                      Deactivating this domain stops it from accepting or sending mail. This is a soft-delete: it
                      sets the domain to disabled and records a deactivation timestamp, but never hard-deletes the
                      domain, its DKIM configuration, or its history. It is distinct from the Active/Disabled/
                      Suspended status control above.
                    </p>
                    <label className="block text-xs text-[var(--text-secondary)] mb-2" htmlFor="deactivate-reason">
                      Reason
                      <textarea
                        id="deactivate-reason"
                        value={deactivateReason}
                        onChange={(e) => setDeactivateReason(e.target.value)}
                        rows={2}
                        placeholder="Why is this domain being deactivated?"
                        className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                      />
                    </label>
                    <button
                      type="button"
                      disabled={!deactivateReason.trim()}
                      onClick={() => setDeactivateConfirmOpen(true)}
                      className="px-3 py-1.5 text-sm rounded border border-[var(--danger)]/40 text-[var(--danger)] hover:bg-[var(--danger)]/10 disabled:opacity-40"
                    >
                      Deactivate domain
                    </button>
                  </section>

                  {deactivateError !== null && (
                    <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                      <p className="text-[var(--danger)] font-medium">{safeErrorInfo(deactivateError).title}</p>
                      <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(deactivateError).detail}</p>
                      {safeErrorInfo(deactivateError).code === "CONFLICT" && (
                        <button
                          type="button"
                          onClick={() => { setDeactivateError(null); refetch(); dnsQuery.refetch(); }}
                          className="mt-2 px-3 py-1.5 text-xs rounded border border-[var(--border)] text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
                        >
                          Reload current state
                        </button>
                      )}
                    </div>
                  )}
                </Tabs.Content>
              </div>
            </Tabs.Root>
          )}

          {domain && (
            <ConfirmDialog
              open={rotateConfirmOpen}
              onOpenChange={setRotateConfirmOpen}
              title="Rotate DKIM key"
              description={`Rotating replaces the current DKIM key pair for ${domain.name}. The previously published TXT record stops matching as soon as this completes — publish the new value promptly.`}
              confirmLabel="Rotate DKIM"
              danger
              pending={rotateDKIMMut.isPending}
              onConfirm={submitRotateDKIM}
            />
          )}

          {domain && (
            <ConfirmDialog
              open={deactivateConfirmOpen}
              onOpenChange={(o) => { setDeactivateConfirmOpen(o); if (!o) setDeactivateReason(""); }}
              title="Deactivate domain"
              description={`Mail service for ${domain.name} will be deactivated. This is a soft-delete: status becomes disabled and a deactivation timestamp is recorded, but nothing is hard-deleted. Type the confirmation phrase to proceed.`}
              requireTypedName={deactivateDomainConfirmation(domain.id)}
              confirmLabel="Deactivate domain"
              danger
              pending={deactivateMut.isPending}
              onConfirm={submitDeactivate}
            />
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
