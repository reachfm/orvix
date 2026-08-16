import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle } from "lucide-react";
import type { ReactNode } from "react";
import StatusBadge from "../../components/StatusBadge";
import { usePlatformDomain } from "../queries";
import { useSetDomainStatusMutation } from "../mutations";
import { domainStatusLabel, domainStatusTone } from "../formatters";
import { DOMAIN_STATUSES } from "../contract";
import { safeErrorInfo } from "../../errors";

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-sm text-[var(--text-primary)] mt-0.5">{children}</dd>
    </div>
  );
}

/**
 * Domain detail + the real platform lifecycle-status mutation. DNS
 * records, DKIM rotation, and TLS/ACME actions are not part of the
 * platform route family — the panel explains the configured
 * DKIM/DMARC state the backend actually reports instead of inventing
 * controls.
 *
 * PRODUCT DECISION: mail_access_mode is a MAILBOX-level policy, not a
 * domain-level one, in this frontend — it is set and changed on the
 * mailbox create/detail views only. The domain still has a legacy
 * mail_access_mode field on the backend for compatibility, but this
 * drawer deliberately does not read or mutate it.
 */
export default function DomainDetailDrawer({
  tenantId,
  id,
  onClose,
}: {
  tenantId: number;
  id: number;
  onClose: () => void;
}) {
  const { data: domain, isLoading, isError, error } = usePlatformDomain(tenantId, id);
  const statusMut = useSetDomainStatusMutation(tenantId);
  const [statusDraft, setStatusDraft] = useState("");
  const [mutationError, setMutationError] = useState<unknown>(null);

  const submitting = statusMut.isPending;

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed right-0 top-0 h-full w-full max-w-xl bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 overflow-y-auto p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)]">
              {domain ? domain.name : "Domain detail"}
            </Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={20} />
            </Dialog.Close>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError || !domain ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)]" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">Failed to load domain</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                  {safeErrorInfo(error).detail}
                </p>
              </div>
            </div>
          ) : (
            <div className="space-y-6">
              <dl className="grid grid-cols-2 gap-4">
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
                    ? `enabled${domain.dkim_selector ? ` · selector ${domain.dkim_selector}` : ""}`
                    : "not enabled"}
                </Field>
                <Field label="DMARC">{domain.dmarc_enabled ? "enabled" : "not enabled"}</Field>
                <Field label="Created">{new Date(domain.created_at).toLocaleString()}</Field>
                <Field label="Updated">{new Date(domain.updated_at).toLocaleString()}</Field>
              </dl>

              {/* Lifecycle status mutation — allowed transitions only */}
              <section aria-label="Domain status" className="border border-[var(--border)] rounded-lg p-4">
                <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Lifecycle status</h3>
                <p className="text-xs text-[var(--text-secondary)] mb-3">
                  The backend accepts only the writable status set:{" "}
                  {DOMAIN_STATUSES.join(", ")}. Requests to other values are rejected.
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  <select
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
                    {statusMut.isPending ? "Saving…" : "Apply status"}
                  </button>
                </div>
              </section>

              {mutationError !== null && (
                <div className="border border-[var(--danger)]/30 rounded-lg p-3 text-sm" role="alert">
                  <p className="text-[var(--danger)] font-medium">{safeErrorInfo(mutationError).title}</p>
                  <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(mutationError).detail}</p>
                </div>
              )}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
