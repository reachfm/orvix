import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, AlertTriangle, Loader2 } from "lucide-react";
import { useCreateMailboxMutation } from "../mutations";
import { usePlatformDomains } from "../../domains/queries";
import { useSetTenantScope } from "../../tenant-context/queries";
import TenantSelectField from "../../tenant-context/components/TenantSelectField";
import { safeErrorInfo } from "../../errors";
import { MAILBOX_ACCESS_MODE_OPTIONS, type MailAccessMode } from "../contract";

/**
 * Mailbox creation dialog.
 *
 * The tenant selector lives IN THIS DIALOG (see CreateDomainDialog for
 * the same pattern) — "Create mailbox" is always available regardless
 * of the page's current scope. The domain selector is filtered to the
 * ACTIVE domains of whichever tenant is currently selected here, and
 * clears if the tenant changes (a domain id from another tenant must
 * never survive a tenant switch). There is no domain_id field on the
 * backend's create-mailbox contract — the domain is carried by the
 * email string itself — so this selector's only job is to constrain
 * the operator to a real domain of the right tenant instead of typing
 * an arbitrary/mistyped one.
 *
 * Security:
 *  - the password field is masked and uses autocomplete="new-password";
 *  - it is held ONLY in this component's local state, never placed in
 *    a URL/query string, localStorage, sessionStorage, the React
 *    Query cache, a toast, or an error message;
 *  - it is cleared immediately on success or dismissal — clearPassword()
 *    runs on every exit path, including the Radix onOpenChange(false)
 *    triggered by Escape or an overlay click;
 *  - the backend never returns the password or its hash, and this
 *    dialog never expects one back.
 *
 * mail_access_mode is REQUIRED — "inherit" is not offered, matching
 * the platform creation route's own contract.
 */
export default function CreateMailboxDialog({
  initialTenantId,
  onClose,
  onCreated,
}: {
  initialTenantId: number | null;
  onClose: () => void;
  onCreated?: (tenantId: number) => void;
}) {
  const [tenantId, setTenantId] = useState<number | null>(initialTenantId);
  const setScope = useSetTenantScope();
  const createMut = useCreateMailboxMutation(tenantId);
  const domainsQ = usePlatformDomains(tenantId, { status: "active", limit: 200, offset: 0 });
  const domains = domainsQ.data?.domains ?? [];

  const [localPart, setLocalPart] = useState("");
  const [domainId, setDomainId] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [quotaMB, setQuotaMB] = useState("");
  const [sendLimit, setSendLimit] = useState("");
  const [forcePasswordChange, setForcePasswordChange] = useState(true);
  const [accessMode, setAccessMode] = useState<MailAccessMode | "">("");

  const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [createdEmail, setCreatedEmail] = useState<string | null>(null);

  const invalidateKey = () => setIdempotencyKey(null);

  const clearPassword = () => setPassword("");

  const close = () => {
    clearPassword();
    onClose();
  };

  const selectedDomain = domains.find((d) => d.id === domainId) ?? null;
  const trimmedLocalPart = localPart.trim();
  const email = selectedDomain ? `${trimmedLocalPart}@${selectedDomain.name}` : "";
  const valid =
    tenantId !== null && domainId !== null && trimmedLocalPart !== "" && password !== "" && accessMode !== "";

  const submit = () => {
    if (!valid || tenantId === null || createMut.isPending) return;
    const key = idempotencyKey ?? crypto.randomUUID();
    setIdempotencyKey(key);
    setError(null);
    createMut.mutate(
      {
        data: {
          email,
          name: name.trim() || undefined,
          password,
          quota_mb: quotaMB.trim() !== "" ? Number(quotaMB) : undefined,
          send_limit_per_hour: sendLimit.trim() !== "" ? Number(sendLimit) : undefined,
          force_password_change: forcePasswordChange,
          mail_access_mode: accessMode as MailAccessMode,
        },
        idempotencyKey: key,
      },
      {
        onSuccess: (res) => {
          setCreatedEmail(res.mailbox.email);
          clearPassword();
          setScope.mutate({ tenantId, tenantName: undefined });
          onCreated?.(tenantId);
        },
        onError: (e) => {
          setError(e);
          clearPassword();
        },
      },
    );
  };

  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) close(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/60 z-40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 z-50 w-full max-w-md max-h-[85vh] overflow-y-auto bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6">
          <div className="flex items-start justify-between mb-3">
            <Dialog.Title className="text-base font-semibold text-[var(--text-primary)]">
              {createdEmail ? "Mailbox created" : "Create mailbox"}
            </Dialog.Title>
            <Dialog.Close aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>

          {createdEmail ? (
            <div className="space-y-4">
              <div className="border border-[var(--success)]/40 rounded-lg p-3 text-sm bg-[var(--success)]/5">
                <p className="font-medium text-[var(--success)]">{createdEmail}</p>
              </div>
              <p className="text-xs text-[var(--text-secondary)]">
                The password you set was used once to create the mailbox and is never returned or stored by this
                console. If the user needs to sign in, use the mailbox's forgot-password / activation flow —
                {forcePasswordChange ? " it must change its password on first sign-in." : ""}
              </p>
              <div className="flex justify-end">
                <button type="button" onClick={close} className="px-3 py-2 text-sm rounded bg-[var(--accent)] text-white">
                  Done
                </button>
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
                onChange={(id) => { setTenantId(id); setDomainId(null); invalidateKey(); }}
                disabled={createMut.isPending}
              />

              <div className="grid grid-cols-[1fr_auto_1.4fr] items-end gap-2">
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Local part *</span>
                  <input
                    value={localPart}
                    onChange={(e) => { setLocalPart(e.target.value); invalidateKey(); }}
                    placeholder="user"
                    required
                    disabled={createMut.isPending}
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] disabled:opacity-60"
                  />
                </label>
                <span className="pb-2 text-[var(--text-secondary)]">@</span>
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Domain *</span>
                  <select
                    value={domainId === null ? "" : String(domainId)}
                    onChange={(e) => { setDomainId(e.target.value === "" ? null : Number(e.target.value)); invalidateKey(); }}
                    required
                    disabled={tenantId === null || domainsQ.isLoading || createMut.isPending}
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] disabled:opacity-60"
                  >
                    <option value="">
                      {tenantId === null ? "Select a tenant first" : domainsQ.isLoading ? "Loading…" : "— Select a domain —"}
                    </option>
                    {domains.map((d) => (
                      <option key={d.id} value={String(d.id)}>{d.name}</option>
                    ))}
                  </select>
                </label>
              </div>
              {tenantId !== null && !domainsQ.isLoading && domains.length === 0 && (
                <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                  <AlertTriangle size={12} /> This tenant has no active domains — create one first.
                </p>
              )}
              {email && (
                <p className="text-xs text-[var(--text-secondary)]">Mailbox address: <span className="font-mono">{email}</span></p>
              )}

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Display name</span>
                <input
                  value={name}
                  onChange={(e) => { setName(e.target.value); invalidateKey(); }}
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
              </label>

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Password *</span>
                <input
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => { setPassword(e.target.value); invalidateKey(); }}
                  required
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
              </label>

              <label className="flex items-center gap-2 text-sm text-[var(--text-primary)]">
                <input
                  type="checkbox"
                  checked={forcePasswordChange}
                  onChange={(e) => { setForcePasswordChange(e.target.checked); invalidateKey(); }}
                />
                Force password change on first sign-in
              </label>

              <fieldset>
                <legend className="text-sm text-[var(--text-secondary)] mb-1.5">Mail access mode *</legend>
                <div className="space-y-2">
                  {MAILBOX_ACCESS_MODE_OPTIONS.map((o) => (
                    <label
                      key={o.value}
                      className="flex items-start gap-2 p-2 border border-[var(--border)] rounded cursor-pointer has-[:checked]:border-[var(--accent)]"
                    >
                      <input
                        type="radio"
                        name="mail_access_mode"
                        value={o.value}
                        checked={accessMode === o.value}
                        onChange={() => { setAccessMode(o.value); invalidateKey(); }}
                        className="mt-0.5"
                      />
                      <span>
                        <span className="block text-sm font-medium text-[var(--text-primary)]">{o.label}</span>
                        <span className="block text-xs text-[var(--text-secondary)]">{o.description}</span>
                      </span>
                    </label>
                  ))}
                </div>
                {accessMode === "" && (
                  <p className="mt-1.5 text-xs text-[var(--text-muted)] flex items-center gap-1">
                    <AlertTriangle size={12} /> A mail access mode is required.
                  </p>
                )}
              </fieldset>

              <div className="grid grid-cols-2 gap-3">
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Quota (MB)</span>
                  <input
                    type="number"
                    min={0}
                    value={quotaMB}
                    onChange={(e) => { setQuotaMB(e.target.value); invalidateKey(); }}
                    placeholder="Default"
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  />
                </label>
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Send limit / hour</span>
                  <input
                    type="number"
                    min={0}
                    value={sendLimit}
                    onChange={(e) => { setSendLimit(e.target.value); invalidateKey(); }}
                    placeholder="Default"
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  />
                </label>
              </div>

              <div className="flex justify-end gap-2">
                <Dialog.Close className="px-3 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
                  Cancel
                </Dialog.Close>
                <button
                  type="button"
                  disabled={!valid || createMut.isPending}
                  onClick={submit}
                  className="inline-flex items-center gap-2 px-3 py-2 text-sm rounded bg-[var(--accent)] text-white disabled:opacity-40"
                >
                  {createMut.isPending && <Loader2 size={14} className="animate-spin" />}
                  Create mailbox
                </button>
              </div>
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
