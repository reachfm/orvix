import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle } from "lucide-react";
import type { ReactNode } from "react";
import StatusBadge from "../../components/StatusBadge";
import { usePlatformDomain } from "../queries";
import { useSetDomainStatusMutation, useSetMailAccessModeMutation } from "../mutations";
import {
  domainStatusLabel,
  domainStatusTone,
  mailAccessModeDescription,
  mailAccessModeLabel,
} from "../formatters";
import { DOMAIN_STATUSES, MAIL_ACCESS_MODES, type MailAccessMode } from "../contract";
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
 * Domain detail + the two real platform mutations (lifecycle status,
 * mail-access mode). DNS records, DKIM rotation, and TLS/ACME actions
 * are not part of the platform route family — the panel explains the
 * configured DKIM/DMARC state the backend actually reports instead of
 * inventing controls.
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
  const modeMut = useSetMailAccessModeMutation(tenantId);
  const [statusDraft, setStatusDraft] = useState("");
  const [modeDraft, setModeDraft] = useState<MailAccessMode | "">("");
  const [mutationError, setMutationError] = useState<unknown>(null);

  const submitting = statusMut.isPending || modeMut.isPending;

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

              {/* Mail access policy — canonical modes with explanation */}
              <section aria-label="Mail access policy" className="border border-[var(--border)] rounded-lg p-4">
                <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Mail access policy</h3>
                <p className="text-sm text-[var(--text-secondary)] mb-1">
                  Configured: <span className="font-medium text-[var(--text-primary)]">{mailAccessModeLabel(domain.mail_access_mode)}</span>
                </p>
                <p className="text-xs text-[var(--text-secondary)] mb-3">{mailAccessModeDescription(domain.mail_access_mode)}</p>
                <div className="flex flex-wrap items-center gap-2">
                  <select
                    aria-label="New mail access mode"
                    value={modeDraft}
                    onChange={(e) => setModeDraft(e.target.value as MailAccessMode)}
                    className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  >
                    <option value="">— Choose mode —</option>
                    {MAIL_ACCESS_MODES.filter((m) => m !== domain.mail_access_mode).map((m) => (
                      <option key={m} value={m}>{mailAccessModeLabel(m)}</option>
                    ))}
                  </select>
                  <button
                    type="button"
                    disabled={!modeDraft || submitting}
                    onClick={() =>
                      modeMut.mutate({ id: domain.id, mailAccessMode: modeDraft as MailAccessMode }, {
                        onSuccess: () => setModeDraft(""),
                        onError: (e) => setMutationError(e),
                      })
                    }
                    className="px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                  >
                    {modeMut.isPending ? "Saving…" : "Apply mode"}
                  </button>
                </div>
                <p className="text-xs text-[var(--text-muted)] mt-3">
                  The updated value is only claimed after the backend confirms the mutation — enforcement is server-side,
                  not client-side.
                </p>
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
