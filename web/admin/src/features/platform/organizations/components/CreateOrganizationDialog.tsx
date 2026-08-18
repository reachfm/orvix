import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Copy, Check, Loader2, AlertTriangle, KeyRound } from "lucide-react";
import { useCreatePlatformOrganizationMutation } from "../mutations";
import { safeErrorInfo, errorCodeOf } from "../../errors";
import type { CreatePlatformOrganizationResponse } from "../contract";

/**
 * PSA organization creation dialog (POST /platform/organizations).
 *
 * The org is created pending_activation with a REQUIRED tenant_admin
 * owner invitation. The live response includes the ONE-TIME invite
 * token — shown exactly once with a copy action, never stored, never
 * re-fetched (idempotent replays return the stored body WITHOUT the
 * token). The org becomes active only when the owner redeems the token
 * at the public invitation-accept page.
 *
 * Idempotency: one key per submission attempt; any field edit after a
 * failed attempt clears the key so a changed request never replays an
 * old key.
 */
export default function CreateOrganizationDialog({ onClose }: { onClose: () => void }) {
  const createMut = useCreatePlatformOrganizationMutation();

  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [domain, setDomain] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<CreatePlatformOrganizationResponse | null>(null);
  const [copiedField, setCopiedField] = useState<string | null>(null);

  const trimmedName = name.trim();
  const trimmedOwnerEmail = ownerEmail.trim();
  const formValid = trimmedName.length > 0 && trimmedOwnerEmail.length > 0;

  const invalidateKey = () => setIdempotencyKey(null);

  const copy = (field: string, value: string) => {
    void navigator.clipboard?.writeText(value).catch(() => {});
    setCopiedField(field);
    window.setTimeout(() => setCopiedField((f) => (f === field ? null : f)), 2000);
  };

  const submit = () => {
    if (!formValid || createMut.isPending) return;
    const key = idempotencyKey ?? crypto.randomUUID();
    setIdempotencyKey(key);
    setError(null);
    createMut.mutate(
      {
        body: {
          name: trimmedName,
          slug: slug.trim() || undefined,
          domain: domain.trim() || undefined,
          owner_email: trimmedOwnerEmail,
        },
        idempotencyKey: key,
      },
      {
        onSuccess: (res) => setResult(res),
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
              {result ? "Organization created" : "Create organization"}
            </Dialog.Title>
            <Dialog.Close aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">
              <X size={18} />
            </Dialog.Close>
          </div>

          {result ? (
            <div className="space-y-4">
              <div className="border border-[var(--warning)]/40 rounded-lg p-3 text-sm bg-[var(--warning)]/5">
                <p className="font-medium text-[var(--text-primary)]">{result.organization.name}</p>
                <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                  This organization starts in <strong className="text-[var(--warning)]">pending activation</strong> —
                  it becomes operational only when the owner redeems the invitation token below.
                </p>
              </div>

              <section>
                <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--text-secondary)] mb-1.5 flex items-center gap-1.5">
                  <KeyRound size={12} /> One-time owner invitation token
                </h3>
                <p className="text-xs text-[var(--text-secondary)] mb-2">
                  Invited owner: {result.invitation.email} · Role {result.invitation.role} · Expires{" "}
                  {new Date(result.invitation.expires_at).toLocaleString()}
                </p>
                <div className="flex items-start gap-2">
                  <code className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs break-all font-mono text-[var(--text-primary)]">
                    {result.invite_token}
                  </code>
                  <button
                    type="button"
                    aria-label="Copy invitation token"
                    onClick={() => copy("token", result.invite_token)}
                    className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)] shrink-0"
                  >
                    {copiedField === "token" ? <Check size={14} /> : <Copy size={14} />}
                  </button>
                </div>
                <p className="text-xs text-[var(--danger)] mt-2">{result.warning}</p>
                <p className="text-xs text-[var(--text-muted)] mt-1">
                  Share it privately with the owner. They redeem it at the public invitation-accept page
                  (token + password), which activates the organization.
                </p>
              </section>

              <div className="flex justify-end">
                <button
                  type="button"
                  onClick={onClose}
                  className="px-3 py-2 text-sm rounded border border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                >
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
                  {errorCodeOf(error) === "CONFLICT" && (
                    <p className="text-xs text-[var(--text-secondary)] mt-1">
                      The slug or domain may already be in use by another organization.
                    </p>
                  )}
                </div>
              )}

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Organization name *</span>
                <input
                  value={name}
                  onChange={(e) => { setName(e.target.value); invalidateKey(); }}
                  placeholder="Acme Corp"
                  required
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
              </label>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Slug</span>
                  <input
                    value={slug}
                    onChange={(e) => { setSlug(e.target.value); invalidateKey(); }}
                    placeholder="acme"
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  />
                </label>
                <label className="block text-sm">
                  <span className="text-[var(--text-secondary)]">Domain</span>
                  <input
                    value={domain}
                    onChange={(e) => { setDomain(e.target.value); invalidateKey(); }}
                    placeholder="acme.example"
                    className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  />
                </label>
              </div>

              <label className="block text-sm">
                <span className="text-[var(--text-secondary)]">Owner email *</span>
                <input
                  type="email"
                  value={ownerEmail}
                  onChange={(e) => { setOwnerEmail(e.target.value); invalidateKey(); }}
                  placeholder="owner@acme.example"
                  required
                  className="mt-1 w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                />
                <span className="text-xs text-[var(--text-muted)] mt-1 block">
                  The owner receives a tenant_admin invitation to activate this organization. No password is ever
                  created or stored by this console.
                </span>
              </label>

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
                  Create organization
                </button>
              </div>
              {!trimmedName.length && (
                <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                  <AlertTriangle size={12} /> Organization name is required.
                </p>
              )}
              {trimmedName.length > 0 && !trimmedOwnerEmail.length && (
                <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                  <AlertTriangle size={12} /> Owner email is required.
                </p>
              )}
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
