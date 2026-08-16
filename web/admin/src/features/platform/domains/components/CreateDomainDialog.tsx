import { useMemo, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { useQueryClient } from "@tanstack/react-query";
import { X, Copy, Check, AlertTriangle, Loader2, ChevronDown, ChevronRight } from "lucide-react";
import { useCreateDomainMutation } from "../mutations";
import { domainKeys } from "../queries";
import { useSetTenantScope } from "../../tenant-context/queries";
import TenantSelectField from "../../tenant-context/components/TenantSelectField";
import { safeErrorInfo } from "../../errors";
import type { PlatformCreateDomainResult, PlatformDomainLimits } from "../contract";

/**
 * Domain creation dialog. Deliberately has NO mail-access-mode
 * selector — mail-access policy belongs to the mailbox, never the
 * domain, on this route (see contract.ts).
 *
 * The tenant selector lives IN THIS DIALOG, not on the parent page:
 * "Create domain" is always available regardless of whether a page
 * scope is currently applied. initialTenantId pre-fills the selector
 * (current page scope, or the operator's last-used tenant) but the
 * operator can change it here, and every submit sends the currently
 * selected tenant id explicitly — nothing is inferred.
 *
 * Idempotency: one key is generated when the dialog first mounts and
 * is reused across repeated submissions of the SAME form state
 * (network retry, double-click). Any field edit after a failed
 * attempt clears the key so the next submit gets a fresh one — the
 * request has genuinely changed, so replaying the old key would be
 * wrong. Changing the tenant also clears the key for the same reason.
 */
export default function CreateDomainDialog({
  initialTenantId,
  onClose,
  onCreated,
}: {
  initialTenantId: number | null;
  onClose: () => void;
  onCreated?: (domainId: number, tenantId: number) => void;
}) {
  const [tenantId, setTenantId] = useState<number | null>(initialTenantId);
  const setScope = useSetTenantScope();
  const qc = useQueryClient();
  const createMut = useCreateDomainMutation(tenantId);

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [status, setStatus] = useState("active");
  const [showLimits, setShowLimits] = useState(false);
  const [maxMailboxes, setMaxMailboxes] = useState("");
  const [maxAliases, setMaxAliases] = useState("");
  const [defaultQuotaMB, setDefaultQuotaMB] = useState("");
  const [maxQuotaMB, setMaxQuotaMB] = useState("");
  const [dkimGenerate, setDkimGenerate] = useState(false);
  const [dkimSelector, setDkimSelector] = useState("");

  const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<PlatformCreateDomainResult | null>(null);
  const [copiedField, setCopiedField] = useState<string | null>(null);

  // Any field change after a failed attempt invalidates the prior key —
  // the next submission is a materially different request.
  const invalidateKey = () => setIdempotencyKey(null);

  const trimmedName = name.trim();
  const nameValid = trimmedName.length > 0;
  const tenantValid = tenantId !== null;
  const formValid = nameValid && tenantValid;

  const limits: PlatformDomainLimits | undefined = useMemo(() => {
    const l: PlatformDomainLimits = {};
    if (maxMailboxes.trim() !== "") l.max_mailboxes = Number(maxMailboxes);
    if (maxAliases.trim() !== "") l.max_aliases = Number(maxAliases);
    if (defaultQuotaMB.trim() !== "") l.default_mailbox_quota_mb = Number(defaultQuotaMB);
    if (maxQuotaMB.trim() !== "") l.max_mailbox_quota_mb = Number(maxQuotaMB);
    return Object.keys(l).length > 0 ? l : undefined;
  }, [maxMailboxes, maxAliases, defaultQuotaMB, maxQuotaMB]);

  const copy = (field: string, value: string) => {
    void navigator.clipboard?.writeText(value).catch(() => {});
    setCopiedField(field);
    window.setTimeout(() => setCopiedField((f) => (f === field ? null : f)), 2000);
  };

  const submit = () => {
    if (!formValid || tenantId === null || createMut.isPending) return;
    const key = idempotencyKey ?? crypto.randomUUID();
    setIdempotencyKey(key);
    setError(null);
    createMut.mutate(
      {
        data: {
          name: trimmedName,
          description: description.trim() || undefined,
          status: status || undefined,
          limits,
          dkim: dkimGenerate ? { generate: true, selector: dkimSelector.trim() || undefined } : undefined,
        },
        idempotencyKey: key,
      },
      {
        onSuccess: (res) => {
          setResult(res);
          // The dialog may have created a domain for a DIFFERENT
          // tenant than the page's current scope — bind the page
          // scope to the tenant actually used so the list view (and
          // the newly-created domain within it) is visible without
          // a second manual step. The success panel itself (DKIM/DNS
          // records + copy buttons) stays on screen so the operator
          // can copy what they need; "View domain" below opens the
          // detail view explicitly when they're ready.
          setScope.mutate({ tenantId, tenantName: undefined });
          // Seed the DNS/DKIM one-time cache so the detail view's DNS
          // Setup/DKIM tabs can show the real records the backend just
          // returned — this data is NEVER available again from any GET
          // route, so it must be carried forward now or not at all.
          if (tenantId !== null) {
            qc.setQueryData(domainKeys.dnsCache(tenantId, res.domain.id), {
              dkim: res.dkim,
              dns_requirements: res.dns_requirements,
              dns_next_step: res.dns_next_step,
            });
          }
        },
        onError: (e) => setError(e),
      },
    );
  };

  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-lg max-h-[85vh] overflow-y-auto bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <div className="flex items-start justify-between mb-3">
            <Dialog.Title className="text-base font-semibold text-[var(--text-primary)]">
              {result ? "Domain created" : "Create domain"}
            </Dialog.Title>
            <Dialog.Close aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>

          {result ? (
            <div className="space-y-4">
              <div className="border border-[var(--success)]/40 rounded-lg p-3 text-sm bg-[var(--success)]/5">
                <p className="font-medium text-[var(--success)]">{result.domain.name}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                  Tenant {result.domain.tenant_id} · Status {result.domain.status} · Plan {result.domain.plan}
                  {result.idempotent && " · Replayed from a prior identical submission"}
                </p>
              </div>

              <section>
                <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-secondary)] mb-1.5">
                  Effective limits
                </h3>
                <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                  <dt className="text-[var(--text-secondary)]">Max mailboxes</dt>
                  <dd className="text-[var(--text-primary)]">
                    {result.effective_limits.max_mailboxes_unlimited ? "Unlimited" : result.effective_limits.max_mailboxes}
                  </dd>
                  <dt className="text-[var(--text-secondary)]">Max aliases</dt>
                  <dd className="text-[var(--text-primary)]">
                    {result.effective_limits.max_aliases_unlimited ? "Unlimited" : result.effective_limits.max_aliases}
                  </dd>
                  <dt className="text-[var(--text-secondary)]">Default mailbox quota</dt>
                  <dd className="text-[var(--text-primary)]">{result.effective_limits.default_mailbox_quota_mb} MB</dd>
                  <dt className="text-[var(--text-secondary)]">Max mailbox quota</dt>
                  <dd className="text-[var(--text-primary)]">
                    {result.effective_limits.max_mailbox_quota_mb_unlimited ? "Unlimited" : `${result.effective_limits.max_mailbox_quota_mb} MB`}
                  </dd>
                </dl>
              </section>

              {result.plan && (
                <section>
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-secondary)] mb-1.5">
                    Plan usage
                  </h3>
                  <p className="text-sm text-[var(--text-primary)]">
                    {result.plan.domains_used} / {result.plan.max_domains_unlimited ? "∞" : result.plan.max_domains} domains used
                    {result.plan.remaining_domains !== null && ` (${result.plan.remaining_domains} remaining)`}
                  </p>
                </section>
              )}

              {result.dkim && (
                <section>
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-secondary)] mb-1.5">
                    DKIM (public record only)
                  </h3>
                  <p className="text-xs text-[var(--text-secondary)] mb-1">Selector: {result.dkim.selector}</p>
                  <div className="flex items-start gap-2">
                    <code className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs break-all">
                      {result.dkim.dns_record_name} TXT {result.dkim.public_dns_txt}
                    </code>
                    <button
                      type="button"
                      aria-label="Copy DKIM DNS record"
                      onClick={() => copy("dkim", `${result.dkim!.dns_record_name} TXT ${result.dkim!.public_dns_txt}`)}
                      className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                    >
                      {copiedField === "dkim" ? <Check size={14} /> : <Copy size={14} />}
                    </button>
                  </div>
                </section>
              )}

              {result.dns_requirements && result.dns_requirements.length > 0 && (
                <section>
                  <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-secondary)] mb-1.5">
                    DNS records to publish
                  </h3>
                  <ul className="space-y-1.5">
                    {result.dns_requirements.map((rec, i) => {
                      const line = `${rec.name} ${rec.type} ${rec.value}`;
                      const field = `dns-${i}`;
                      return (
                        <li key={field} className="flex items-start gap-2">
                          <code className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs break-all">
                            {line}
                            {rec.required && <span className="ml-2 text-[var(--warning)]">required</span>}
                          </code>
                          <button
                            type="button"
                            aria-label={`Copy DNS record ${rec.name}`}
                            onClick={() => copy(field, line)}
                            className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                          >
                            {copiedField === field ? <Check size={14} /> : <Copy size={14} />}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                </section>
              )}

              <p className="text-xs text-[var(--text-secondary)]">{result.dns_next_step}</p>
              {!result.public_dns_changed && (
                <p className="text-xs text-[var(--text-muted)]">
                  Publishing these records is your responsibility — no public DNS was changed automatically.
                </p>
              )}

              <div className="flex justify-end gap-2">
                <button
                  type="button"
                  onClick={onClose}
                  className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                >
                  Done
                </button>
                {onCreated && tenantId !== null && (
                  <button
                    type="button"
                    onClick={() => { onCreated(result.domain.id, tenantId); onClose(); }}
                    className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white"
                  >
                    View domain
                  </button>
                )}
              </div>
            </div>
          ) : (
            <div className="space-y-4">
              {error !== null && (
                <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                  <p className="text-[var(--danger)] font-medium">{safeErrorInfo(error).title}</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
                </div>
              )}

              <TenantSelectField
                value={tenantId}
                onChange={(id) => { setTenantId(id); invalidateKey(); }}
                disabled={createMut.isPending}
              />

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Domain name *</span>
                <input
                  value={name}
                  onChange={(e) => { setName(e.target.value); invalidateKey(); }}
                  placeholder="example.com"
                  required
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
              </label>

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Description</span>
                <input
                  value={description}
                  onChange={(e) => { setDescription(e.target.value); invalidateKey(); }}
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
              </label>

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Status</span>
                <select
                  value={status}
                  onChange={(e) => { setStatus(e.target.value); invalidateKey(); }}
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                >
                  <option value="active">Active</option>
                  <option value="disabled">Disabled</option>
                  <option value="suspended">Suspended</option>
                </select>
              </label>

              <div>
                <button
                  type="button"
                  onClick={() => setShowLimits((v) => !v)}
                  aria-expanded={showLimits}
                  className="flex items-center gap-1 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                >
                  {showLimits ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                  Advanced: limits &amp; DKIM
                </button>
                {showLimits && (
                  <div className="mt-3 space-y-3 border-l-2 border-[var(--border)] pl-3">
                    <div className="grid grid-cols-2 gap-3">
                      <label className="block text-sm">
                        <span className="text-[var(--text-secondary)]">Max mailboxes</span>
                        <input
                          type="number"
                          min={0}
                          value={maxMailboxes}
                          onChange={(e) => { setMaxMailboxes(e.target.value); invalidateKey(); }}
                          placeholder="Plan default"
                          className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        />
                      </label>
                      <label className="block text-sm">
                        <span className="text-[var(--text-secondary)]">Max aliases</span>
                        <input
                          type="number"
                          min={0}
                          value={maxAliases}
                          onChange={(e) => { setMaxAliases(e.target.value); invalidateKey(); }}
                          placeholder="Plan default"
                          className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        />
                      </label>
                      <label className="block text-sm">
                        <span className="text-[var(--text-secondary)]">Default mailbox quota (MB)</span>
                        <input
                          type="number"
                          min={0}
                          value={defaultQuotaMB}
                          onChange={(e) => { setDefaultQuotaMB(e.target.value); invalidateKey(); }}
                          placeholder="Plan default"
                          className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        />
                      </label>
                      <label className="block text-sm">
                        <span className="text-[var(--text-secondary)]">Max mailbox quota (MB)</span>
                        <input
                          type="number"
                          min={0}
                          value={maxQuotaMB}
                          onChange={(e) => { setMaxQuotaMB(e.target.value); invalidateKey(); }}
                          placeholder="Plan default"
                          className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        />
                      </label>
                    </div>

                    <label className="flex items-center gap-2 text-sm text-[var(--text-primary)]">
                      <input
                        type="checkbox"
                        checked={dkimGenerate}
                        onChange={(e) => { setDkimGenerate(e.target.checked); invalidateKey(); }}
                      />
                      Generate DKIM for this domain
                    </label>
                    {dkimGenerate && (
                      <label className="block text-sm">
                        <span className="text-[var(--text-secondary)]">DKIM selector</span>
                        <input
                          value={dkimSelector}
                          onChange={(e) => { setDkimSelector(e.target.value); invalidateKey(); }}
                          placeholder="default"
                          className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                        />
                      </label>
                    )}
                  </div>
                )}
              </div>

              <div className="flex justify-end gap-2">
                <Dialog.Close className="px-3 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
                  Cancel
                </Dialog.Close>
                <button
                  type="button"
                  disabled={!formValid || createMut.isPending}
                  onClick={submit}
                  className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                >
                  {createMut.isPending && <Loader2 size={14} className="animate-spin" />}
                  Create domain
                </button>
              </div>
              {!tenantValid && (
                <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                  <AlertTriangle size={12} /> Select an organization/tenant.
                </p>
              )}
              {tenantValid && !nameValid && (
                <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                  <AlertTriangle size={12} /> Domain name is required.
                </p>
              )}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
