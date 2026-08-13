import * as Dialog from "@radix-ui/react-dialog";
import { X, Loader2, AlertCircle, Pencil } from "lucide-react";
import type { ReactNode } from "react";
import StatusBadge from "../../components/StatusBadge";
import { usePlatformRelay } from "../queries";
import { lastTestResultLabel } from "../contract";
import { safeErrorInfo } from "../../errors";

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-sm text-[var(--text-primary)] mt-0.5">{children}</dd>
    </div>
  );
}

/**
 * Relay detail — redacted by the backend (has_credential boolean only,
 * never the secret). Circuit state and the last safe test result are
 * displayed; raw network/SMTP/TLS internals are never rendered.
 */
export default function RelayDetailDrawer({
  id,
  onClose,
  onEditRequested,
}: {
  id: number;
  onClose: () => void;
  onEditRequested: () => void;
}) {
  const { data: relay, isLoading, isError, error } = usePlatformRelay(id);

  return (
    <Dialog.Root open onOpenChange={(o) => !o && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed right-0 top-0 h-full w-full max-w-xl bg-[var(--bg-surface)] border-l border-[var(--border)] z-50 overflow-y-auto p-6">
          <div className="flex items-start justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold text-[var(--text-primary)]">
              {relay ? relay.name : "Relay detail"}
            </Dialog.Title>
            <Dialog.Close className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={20} />
            </Dialog.Close>
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-40">
              <Loader2 size={24} className="text-[var(--accent)] animate-spin" />
            </div>
          ) : isError || !relay ? (
            <div className="border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)]" />
              <div>
                <p className="text-[var(--danger)] text-sm font-medium">Failed to load relay</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">{safeErrorInfo(error).detail}</p>
              </div>
            </div>
          ) : (
            <div className="space-y-6">
              <dl className="grid grid-cols-2 gap-4">
                <Field label="Endpoint">{relay.host}:{relay.port}</Field>
                <Field label="Scope">{relay.scope}{relay.tenant_id ? ` · tenant ${relay.tenant_id}` : ""}{relay.domain_id ? ` · domain ${relay.domain_id}` : ""}</Field>
                <Field label="Connection security">{relay.conn_security}</Field>
                <Field label="TLS validation">{relay.tls_validation}</Field>
                <Field label="Priority / weight">{relay.priority} / {relay.weight}</Field>
                <Field label="Authentication">{relay.has_credential ? "configured" : "not configured"}</Field>
                <Field label="State">
                  <StatusBadge tone={relay.active ? "success" : "neutral"} label={relay.active ? "Enabled" : "Disabled"}>
                    {relay.active ? "enabled" : "disabled"}
                  </StatusBadge>
                </Field>
                <Field label="Circuit">
                  <StatusBadge
                    tone={relay.circuit_state === "open" ? "danger" : relay.circuit_state === "half_open" ? "warning" : "success"}
                    label={`Circuit ${relay.circuit_state}`}
                  >
                    {relay.circuit_state} ({relay.circuit_failures} failure{relay.circuit_failures === 1 ? "" : "s"})
                  </StatusBadge>
                </Field>
                <Field label="Last test">
                  {relay.last_test_at ? (
                    <>
                      {lastTestResultLabel(relay.last_test_result)}
                      <span className="block text-xs text-[var(--text-muted)]">{new Date(relay.last_test_at).toLocaleString()}</span>
                    </>
                  ) : (
                    "never"
                  )}
                </Field>
                <Field label="Rate limit">{relay.rate_limit_per_min ? `${relay.rate_limit_per_min}/min` : "unlimited"}</Field>
                <Field label="Version">#{relay.version}</Field>
                <Field label="Created">{new Date(relay.created_at).toLocaleString()}</Field>
              </dl>

              <p className="text-xs text-[var(--text-muted)]">
                Credentials are encrypted at rest and are never exposed by any read. The safe last-test result vocabulary
                (ok, connect_failed, tls_failed, auth_failed, failed) is the only test evidence surfaced.
              </p>

              <button
                type="button"
                onClick={onEditRequested}
                className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              >
                <Pencil size={16} /> Edit relay
              </button>
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
