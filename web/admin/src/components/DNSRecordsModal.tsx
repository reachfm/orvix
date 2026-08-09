import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import {
  X, Copy, Check, Download, RefreshCw, Loader2, AlertTriangle, ShieldAlert,
} from "lucide-react";
import { api, domainErrorMessage, ApiError } from "../api";
import type { DomainDNSHealth, DNSHealthCheck, DKIMResult } from "../types/dns";
import { dnsQueryKey, buildRecordRows, buildDNSRecordsFile } from "../lib/dnsRecords";

/**
 * DNSRecordsModal renders the canonical EnterpriseDNSHealth payload returned by
 * BOTH `GET /enterprise/domains/:id/dns` and `POST .../dns/verify` — the two
 * endpoints share one response shape, so nothing here special-cases which one
 * answered.
 *
 * Three invariants this component must never violate:
 *
 *  1. `complete`, `health_score` and `dns_health` are read from the API object
 *     on every render. This component NEVER derives its own pass/percentage
 *     from the individual record statuses. The backend's recomputeEnterpriseHealth
 *     is the single source of truth; duplicating that logic here is exactly the
 *     defect that produced "100% Pass" over rows that all said "Not checked".
 *  2. When `complete` is false the header renders an explicit INCOMPLETE state
 *     regardless of how high `health_score` is.
 *  3. A 429 from the verify POST is NOT an error state. Per the backend
 *     contract, the 429 body IS the last successful snapshot, so it is written
 *     into the cache and rendered normally with a cooldown notice.
 */

interface DNSRecordsModalProps {
  domain: { id: number; name: string; dkim_selector?: string };
  onClose: () => void;
}

function errorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object" && "code" in (err as any)) {
    return domainErrorMessage((err as any).code, (err as any).message || fallback);
  }
  return (err as Error)?.message || fallback;
}

function isCooldownError(err: unknown): err is ApiError {
  return !!err && typeof err === "object" && (err as any).status === 429;
}

function StatusPill({ status }: { status: string }) {
  const s = (status || "unknown").toLowerCase();
  const map: Record<string, [string, string]> = {
    pass: ["var(--success)", "Pass"],
    warning: ["var(--warning)", "Warning"],
    fail: ["var(--danger)", "Fail"],
    pending: ["var(--accent)", "Pending propagation"],
    unknown: ["var(--text-secondary)", "Not checked"],
    not_checked: ["var(--text-secondary)", "Not checked"],
    // Not required of this deployment. Rendered neutrally — never as a
    // failure — and excluded from the score by the backend.
    optional: ["var(--text-secondary)", "Optional — not configured"],
    not_applicable: ["var(--text-secondary)", "Not applicable"],
    // The required value could not be determined, so this record is
    // indeterminate and must never read as a pass.
    configuration_required: ["var(--danger)", "Configuration required"],
  };
  const [color, label] = map[s] || ["var(--text-secondary)", status];
  return (
    <span
      className="inline-flex items-center gap-1.5 whitespace-nowrap text-xs"
      data-testid={`status-${s}`}
    >
      <span className="w-1.5 h-1.5 rounded-full shrink-0" style={{ backgroundColor: color }} />
      <span style={{ color }}>{label}</span>
    </span>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      /* clipboard unavailable — nothing to surface, the value is selectable */
    }
  }, [value]);
  if (!value) return null;
  return (
    <button
      type="button"
      onClick={onCopy}
      aria-label={`Copy ${label}`}
      className="shrink-0 p-1 rounded text-[var(--accent)] hover:text-[var(--accent-soft)] hover:bg-[var(--bg-elevated)]"
    >
      {copied ? <Check size={12} className="text-[var(--success)]" /> : <Copy size={12} />}
    </button>
  );
}

/** Live cooldown countdown. Pure clock — issues no network requests while ticking. */
function useCooldown(health: DomainDNSHealth | undefined): number {
  const [remaining, setRemaining] = useState(0);
  const deadlineRef = useRef<number>(0);

  useEffect(() => {
    if (!health) return;
    let deadline = 0;
    if (health.cooldown_until) {
      const parsed = new Date(health.cooldown_until).getTime();
      if (!Number.isNaN(parsed)) deadline = parsed;
    }
    if (!deadline && health.retry_after_seconds && health.retry_after_seconds > 0) {
      deadline = Date.now() + health.retry_after_seconds * 1000;
    }
    deadlineRef.current = deadline;
    setRemaining(deadline ? Math.max(0, Math.ceil((deadline - Date.now()) / 1000)) : 0);
  }, [health, health?.cooldown_until, health?.retry_after_seconds]);

  useEffect(() => {
    if (remaining <= 0) return;
    const t = setInterval(() => {
      const left = Math.max(0, Math.ceil((deadlineRef.current - Date.now()) / 1000));
      setRemaining(left);
      if (left <= 0) clearInterval(t);
    }, 1000);
    return () => clearInterval(t);
  }, [remaining > 0]);

  return remaining;
}

function formatCountdown(secs: number): string {
  const m = Math.floor(secs / 60);
  const s = secs % 60;
  return m > 0 ? `${m}m ${String(s).padStart(2, "0")}s` : `${s}s`;
}

export default function DNSRecordsModal({ domain, onClose }: DNSRecordsModalProps) {
  const queryClient = useQueryClient();
  const modalRef = useRef<HTMLDivElement>(null);
  const closedRef = useRef(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  const [confirmRotate, setConfirmRotate] = useState(false);
  const [dkimPending, setDkimPending] = useState<DKIMResult | null>(null);
  const queryKey = dnsQueryKey(domain.id);

  /**
   * `placeholderData: keep` retains the previously rendered payload while a
   * background refetch is in flight, so an in-flight refetch never blanks the
   * already-rendered record table.
   */
  const dnsQuery = useQuery<DomainDNSHealth>({
    queryKey,
    queryFn: () => api.getEnterpriseDomainDNS(domain.id),
    placeholderData: (prev) => prev,
    staleTime: 30_000,
    gcTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });

  const verifyMutation = useMutation({
    mutationFn: () => api.verifyEnterpriseDomainDNS(domain.id),
    /**
     * The verify response is a COMPLETE EnterpriseDNSHealth object, so it
     * wholesale REPLACES the cached GET payload. A shallow merge would leave
     * stale keys behind (e.g. an old mtasts_policy, or a cooldown_until that
     * the fresh check just cleared) — the cache must reflect exactly one
     * server-authored object.
     */
    onSuccess: (fresh) => {
      queryClient.setQueryData(queryKey, fresh);
    },
    onError: (err) => {
      // 429 = cooldown. The body IS the last good snapshot; render it.
      if (isCooldownError(err) && err.body && typeof err.body === "object") {
        queryClient.setQueryData(queryKey, err.body);
      }
    },
  });

  const generateDKIM = useMutation({
    mutationFn: () => api.generateDomainDKIM(domain.id, domain.dkim_selector || "mail"),
    onSuccess: (res: any) => {
      if (res?.dkim) setDkimPending(res.dkim as DKIMResult);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const rotateDKIM = useMutation({
    mutationFn: () => api.rotateDomainDKIM(domain.id, domain.dkim_selector || "mail"),
    onSuccess: (res: any) => {
      setConfirmRotate(false);
      if (res?.dkim) setDkimPending(res.dkim as DKIMResult);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const health = dnsQuery.data;
  const cooldownRemaining = useCooldown(health);

  /**
   * Single close path. Escape, the X button and the overlay all route through
   * this guarded callback so a close can never fire twice (which previously
   * could pop two entries of parent state).
   */
  const close = useCallback(() => {
    if (closedRef.current) return;
    closedRef.current = true;
    onCloseRef.current();
  }, []);

  // Escape closes; Tab is trapped within the dialog.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        close();
        return;
      }
      if (e.key !== "Tab" || !modalRef.current) return;
      const focusable = Array.from(
        modalRef.current.querySelectorAll<HTMLElement>(
          'button:not([disabled]), [href], input:not([disabled]), select, textarea, [tabindex]:not([tabindex="-1"])'
        )
      ).filter((el) => el.offsetParent !== null || el === document.activeElement);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [close]);

  // Focus moves into the dialog on open and returns to the DNS button on close.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const target =
      modalRef.current?.querySelector<HTMLElement>("[data-autofocus]") ||
      modalRef.current?.querySelector<HTMLElement>("button");
    target?.focus();
    return () => {
      previouslyFocused?.focus?.();
    };
  }, []);

  const rows = useMemo(() => buildRecordRows(health, domain.name, dkimPending), [health, domain.name, dkimPending]);

  const handleDownload = useCallback(() => {
    const contents = buildDNSRecordsFile(rows, domain.name, health);
    const blob = new Blob([contents], { type: "text/plain;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${domain.name}-dns-records.txt`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [rows, domain.name, health]);

  const dkim = health?.dkim as DNSHealthCheck | null | undefined;
  const dkimConfigured = !!dkim?.configured;
  const verifying = verifyMutation.isPending;
  const cooling = cooldownRemaining > 0;
  const checkDisabled = verifying || cooling;

  // Never derive these — read straight off the server object every render.
  const complete = health?.complete === true;
  const score = health?.health_score;
  const healthLabel = health?.dns_health;

  const showBlockingError = dnsQuery.isError && !health;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:p-8">
      <div className="fixed inset-0 bg-black/70" onClick={close} aria-hidden="true" />
      <div
        ref={modalRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dns-records-title"
        className="relative z-10 w-full max-w-5xl my-auto bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl shadow-2xl flex flex-col max-h-[90vh]"
      >
        {/* ── Header ── */}
        <div className="shrink-0 px-5 py-4 border-b border-[var(--border)]">
          <div className="flex items-start justify-between gap-4">
            <div className="min-w-0">
              <h2 id="dns-records-title" className="text-base font-semibold text-[var(--text-primary)]">
                DNS Records · <span className="font-mono text-[var(--accent-soft)]">{domain.name}</span>
              </h2>
              {health?.last_checked_at && (
                <p className="text-[var(--text-muted)] text-xs mt-0.5">
                  Last checked {new Date(health.last_checked_at).toLocaleString()}
                </p>
              )}
            </div>
            <button
              type="button"
              onClick={close}
              aria-label="Close DNS records dialog"
              className="shrink-0 p-1.5 rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-elevated)]"
            >
              <X size={18} />
            </button>
          </div>

          <div className="flex flex-wrap items-center gap-3 mt-3">
            {health && (
              <div className="flex items-center gap-2" data-testid="health-summary">
                <span className="text-2xl font-semibold text-[var(--text-primary)]">
                  {typeof score === "number" ? `${score}%` : "—"}
                </span>
                {complete ? (
                  <StatusPill status={healthLabel || "unknown"} />
                ) : (
                  /* complete:false ⇒ never a pass/100% affirmation, whatever the score says */
                  <span
                    data-testid="incomplete-indicator"
                    className="inline-flex items-center gap-1.5 px-2 py-1 rounded-full bg-[var(--warning)]/10 text-[var(--warning)] text-xs"
                  >
                    <AlertTriangle size={11} />
                    Incomplete — not all records were checked
                  </span>
                )}
              </div>
            )}

            <div className="flex items-center gap-2 ml-auto">
              <button
                type="button"
                data-autofocus
                onClick={() => { if (!checkDisabled) verifyMutation.mutate(); }}
                disabled={checkDisabled}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[var(--accent)] text-white text-xs hover:bg-[var(--accent-hover)] disabled:opacity-50 disabled:cursor-not-allowed"
              >
                {verifying ? <Loader2 size={13} className="animate-spin" /> : <RefreshCw size={13} />}
                {verifying
                  ? "Checking…"
                  : cooling
                  ? `Retry in ${formatCountdown(cooldownRemaining)}`
                  : "Check DNS now"}
              </button>
              <button
                type="button"
                onClick={handleDownload}
                disabled={rows.length === 0}
                className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg border border-[var(--border)] text-[var(--text-primary)] text-xs hover:bg-[var(--bg-elevated)] disabled:opacity-50"
              >
                <Download size={13} /> Download DNS Records
              </button>
            </div>
          </div>

          {cooling && (
            <p className="text-[var(--warning)] text-xs mt-2" role="status" data-testid="cooldown-notice">
              Verification is rate limited. Showing the last completed check —
              you can re-check in {formatCountdown(cooldownRemaining)}.
            </p>
          )}
          {verifyMutation.isError && !isCooldownError(verifyMutation.error) && (
            <p className="text-[var(--danger)] text-xs mt-2" role="alert">
              {errorMessage(verifyMutation.error, "Verification failed.")}
            </p>
          )}
        </div>

        {/* ── Body ── */}
        <div className="flex-1 overflow-y-auto">
          {showBlockingError ? (
            <div className="p-10 text-center">
              <ShieldAlert size={22} className="text-[var(--danger)] mx-auto mb-2" />
              <p className="text-[var(--danger)] text-sm">
                {errorMessage(dnsQuery.error, "Failed to load DNS data.")}
              </p>
            </div>
          ) : !health ? (
            <div className="p-10 text-center text-[var(--text-secondary)] text-sm">
              <Loader2 size={20} className="animate-spin mx-auto mb-2 text-[var(--accent)]" />
              Loading DNS records…
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs border-collapse" data-testid="dns-records-table">
                <thead>
                  <tr className="border-b border-[var(--border)] bg-[var(--bg-base)]">
                    <th className="text-left font-medium text-[var(--text-secondary)] px-3 py-2">Name</th>
                    <th className="text-left font-medium text-[var(--text-secondary)] px-3 py-2">Type</th>
                    <th className="text-left font-medium text-[var(--text-secondary)] px-3 py-2">Required Data</th>
                    <th className="text-left font-medium text-[var(--text-secondary)] px-3 py-2">Current DNS</th>
                    <th className="text-left font-medium text-[var(--text-secondary)] px-3 py-2">Status</th>
                    <th className="text-right font-medium text-[var(--text-secondary)] px-3 py-2">Action</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r) => (
                    <tr
                      key={r.key}
                      data-testid={`dns-row-${r.key}`}
                      className="border-b border-[var(--bg-subtle)] align-top hover:bg-[var(--bg-surface)]"
                    >
                      <td className="px-3 py-2.5 font-mono text-[var(--text-primary)] break-all">{r.name}</td>
                      <td className="px-3 py-2.5 text-[var(--text-secondary)] whitespace-nowrap">
                        {r.type}
                        {r.priority != null && (
                          <span className="ml-1 text-[var(--text-muted)]">prio {r.priority}</span>
                        )}
                      </td>
                      <td
                        className="px-3 py-2.5 font-mono text-[var(--text-secondary)] break-all max-w-xs"
                        data-testid={`required-${r.key}`}
                      >
                        {r.required || (
                          <span className="text-[var(--text-muted)] not-italic">
                            {r.optional ? "Not required" : "—"}
                          </span>
                        )}
                      </td>
                      <td
                        className="px-3 py-2.5 font-mono text-[var(--text-secondary)] break-all max-w-xs"
                        data-testid={`observed-${r.key}`}
                      >
                        {r.observed || <span className="text-[var(--text-muted)]">Not present</span>}
                      </td>
                      <td className="px-3 py-2.5">
                        <StatusPill status={r.status} />
                        {/* `reason` is rendered verbatim as the backend authored it. */}
                        {r.reason && (
                          <p className="text-[var(--text-secondary)] mt-1 max-w-[16rem]" data-testid={`reason-${r.key}`}>
                            {r.reason}
                          </p>
                        )}
                        {/*
                          Repair guidance, authored by the backend against this
                          domain's real values. Shown on every row so an
                          operator never has to guess what to publish.
                        */}
                        {r.guidance && (
                          <p
                            className="text-[var(--text-muted)] mt-1 max-w-[16rem]"
                            data-testid={`guidance-${r.key}`}
                          >
                            {r.guidance}
                          </p>
                        )}
                      </td>
                      <td className="px-3 py-2.5 text-right">
                        <CopyButton value={r.required} label={`${r.type} record for ${r.name}`} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>

              {/* ── DKIM management ── */}
              <div className="px-4 py-4 border-t border-[var(--border)]">
                <div className="flex items-center justify-between gap-3 flex-wrap">
                  <div>
                    <p className="text-[var(--text-primary)] text-sm font-medium">DKIM signing key</p>
                    <p className="text-[var(--text-secondary)] text-xs mt-0.5">
                      {dkimConfigured
                        ? `Configured with selector "${dkim?.selector || domain.dkim_selector || "mail"}".`
                        : "No DKIM key is configured for this domain yet."}
                    </p>
                  </div>
                  {/* Generate is offered only when unconfigured; Rotate only when configured. */}
                  {dkimConfigured ? (
                    <button
                      type="button"
                      onClick={() => setConfirmRotate(true)}
                      disabled={rotateDKIM.isPending}
                      className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg border border-[var(--border)] text-[var(--warning)] text-xs hover:bg-[var(--bg-elevated)] disabled:opacity-50"
                    >
                      <RefreshCw size={13} className={rotateDKIM.isPending ? "animate-spin" : ""} />
                      Rotate DKIM key
                    </button>
                  ) : (
                    <button
                      type="button"
                      onClick={() => { if (!generateDKIM.isPending) generateDKIM.mutate(); }}
                      disabled={generateDKIM.isPending}
                      className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[var(--accent)] text-white text-xs hover:bg-[var(--accent-hover)] disabled:opacity-50"
                    >
                      {generateDKIM.isPending && <Loader2 size={13} className="animate-spin" />}
                      Generate DKIM key
                    </button>
                  )}
                </div>

                {(generateDKIM.isError || rotateDKIM.isError) && (
                  <p className="text-[var(--danger)] text-xs mt-2" role="alert">
                    {errorMessage(generateDKIM.error || rotateDKIM.error, "DKIM operation failed.")}
                  </p>
                )}

                {/*
                  After generate/rotate we show the new PUBLIC TXT record and an
                  explicit pending-propagation state. We never claim pass here:
                  only a subsequent live check can do that.
                */}
                {dkimPending && (
                  <div
                    className="mt-3 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-3 space-y-2"
                    data-testid="dkim-pending"
                  >
                    <StatusPill status="pending" />
                    <p className="text-[var(--warning)] text-xs">
                      Publish the TXT record below. DKIM will keep reporting a failure until
                      DNS propagation completes — this can take up to 48 hours.
                    </p>
                    <div className="flex items-start gap-2">
                      <span className="text-[var(--text-muted)] text-xs shrink-0 w-14">Name</span>
                      <span className="font-mono text-xs text-[var(--text-primary)] break-all select-all flex-1">
                        {dkimPending.dns_record_name}
                      </span>
                      <CopyButton value={dkimPending.dns_record_name} label="DKIM record name" />
                    </div>
                    <div className="flex items-start gap-2">
                      <span className="text-[var(--text-muted)] text-xs shrink-0 w-14">Value</span>
                      <span className="font-mono text-xs text-[var(--text-primary)] break-all select-all flex-1">
                        {dkimPending.public_dns_txt}
                      </span>
                      <CopyButton value={dkimPending.public_dns_txt} label="DKIM public TXT record" />
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>

      {/* ── Rotation confirmation ── */}
      {confirmRotate && (
        <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/70 p-4">
          <div
            role="dialog"
            aria-modal="true"
            aria-labelledby="dkim-rotate-title"
            className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-5 w-96 max-w-full"
          >
            <h3 id="dkim-rotate-title" className="text-sm font-semibold text-[var(--text-primary)] mb-2">
              Rotate DKIM key for {domain.name}?
            </h3>
            <p className="text-xs text-[var(--warning)] mb-4">
              A new key pair is generated immediately, but the published DNS record still
              carries the old key. Until you publish the new TXT record and DNS propagation
              completes (up to 48 hours), DKIM signatures on outbound mail may fail
              validation at receiving servers.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmRotate(false)}
                disabled={rotateDKIM.isPending}
                className="px-3 py-1.5 text-xs rounded-lg border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] disabled:opacity-50"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => { if (!rotateDKIM.isPending) rotateDKIM.mutate(); }}
                disabled={rotateDKIM.isPending}
                className="px-3 py-1.5 text-xs rounded-lg bg-[var(--warning)] text-[var(--bg-base)] font-medium hover:bg-[var(--warning)] disabled:opacity-50"
              >
                {rotateDKIM.isPending ? "Rotating…" : "Rotate key"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
