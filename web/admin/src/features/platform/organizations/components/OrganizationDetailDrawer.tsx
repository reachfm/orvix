import { useState } from "react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useOrganizationDetailQuery } from "../queries";
import { useScheduleOrganizationDeletionMutation, useSetOrganizationActiveMutation } from "../mutations";
import OrganizationEditForm from "./OrganizationEditForm";
import { orgLifecycleLabel } from "../contract";

function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

/** Lifecycle badge tone for the canonical status_label values. */
function lifecycleTone(statusLabel: string | undefined): string {
  switch (statusLabel) {
    case "active":
      return "bg-[var(--success)]/10 text-[var(--success)]";
    case "pending_activation":
      return "bg-[var(--warning)]/15 text-[var(--warning)]";
    case "disabled":
      return "bg-[var(--danger)]/10 text-[var(--danger)]";
    default:
      return "bg-[var(--bg-subtle)] text-[var(--text-secondary)]";
  }
}

export default function OrganizationDetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const [confirmToggle, setConfirmToggle] = useState<{ active: boolean } | null>(null);
  // Deletion is a two-step flow: first capture the required reason (a plain
  // inline card, not a modal — ConfirmDialog only supports a typed-name
  // confirmation, not an arbitrary text field), then hand off to
  // ConfirmDialog for the typed-domain confirmation that actually submits.
  const [deleteStep, setDeleteStep] = useState<"idle" | "reason" | "confirm">("idle");
  const [deleteReason, setDeleteReason] = useState("");
  const [editing, setEditing] = useState(false);
  const detailQ = useOrganizationDetailQuery(id);
  const toggleMut = useSetOrganizationActiveMutation(id);
  const deleteMut = useScheduleOrganizationDeletionMutation(id);

  const detail = detailQ.data;
  const statusLabel = detail?.status_label || (detail?.active ? "active" : "disabled");
  const pendingActivation = statusLabel === "pending_activation";

  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/50" onClick={onClose}>
      <div className="w-full max-w-md h-full bg-[var(--bg-surface)] border-l border-[var(--border)] p-6 overflow-y-auto" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-[var(--text-primary)]">Organization detail</h3>
          <button onClick={onClose} aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">×</button>
        </div>

        {detailQ.isLoading ? (
          <p className="text-[var(--text-secondary)] text-sm">Loading…</p>
        ) : detailQ.error ? (
          <p className="text-[var(--danger)] text-sm">Failed to load: {(detailQ.error as Error).message}</p>
        ) : !detailQ.data ? null : (
          <>
            <div className="mb-6">
              <h4 className="text-xl font-semibold text-[var(--text-primary)]">{detailQ.data.name}</h4>
              <p className="text-sm text-[var(--text-secondary)]">{detailQ.data.domain}</p>
              <span className={`inline-block mt-2 px-2 py-0.5 rounded text-xs ${lifecycleTone(statusLabel)}`}>
                {orgLifecycleLabel(statusLabel)}
              </span>
              {pendingActivation && (
                <p className="text-xs text-[var(--text-secondary)] mt-2 border border-[var(--warning)]/30 rounded-lg p-2 bg-[var(--warning)]/5">
                  This organization was created by a Platform Super Admin and is waiting for its invited owner to
                  redeem the one-time token (public invitation-accept page). Until then it cannot be activated and
                  its tenant console is locked.
                </p>
              )}
            </div>

            <dl className="space-y-2 text-sm mb-4">
              <Row label="Slug" value={detailQ.data.slug} />
              <Row label="Plan" value={detailQ.data.plan} />
              <Row label="Domains" value={`${detailQ.data.domain_count} / ${detailQ.data.max_domains || "unlimited"}`} />
              <Row label="Mailboxes" value={`${detailQ.data.mailbox_count} / ${detailQ.data.max_mailboxes || "unlimited"}`} />
              <Row label="Admin users" value={String(detailQ.data.admin_count)} />
              <Row label="Storage used" value={formatBytes(detailQ.data.quota_used_bytes)} />
              <Row label="Created" value={new Date(detailQ.data.created_at).toLocaleDateString()} />
            </dl>

            {editing ? (
              <OrganizationEditForm org={detailQ.data} id={id} onDone={() => setEditing(false)} />
            ) : (
              <button onClick={() => setEditing(true)} className="px-3 py-1.5 text-xs bg-[var(--bg-elevated)] border border-[var(--border)] text-[var(--text-primary)] rounded mb-3">
                Edit organization
              </button>
            )}

            <div className="flex flex-wrap gap-2">
              <button
                onClick={() => setConfirmToggle({ active: !detailQ.data!.active })}
                disabled={toggleMut.isPending || pendingActivation}
                title={pendingActivation ? "Activation happens when the invited owner redeems the invitation token" : undefined}
                className={`px-3 py-2 text-sm rounded disabled:opacity-50 ${detailQ.data.active ? "bg-[var(--danger)] text-black" : "bg-[var(--success)] text-black"}`}
              >
                {toggleMut.isPending ? "Working…" : detailQ.data.active ? "Suspend organization" : pendingActivation ? "Awaiting owner activation" : "Activate organization"}
              </button>
              <button
                onClick={() => {
                  setDeleteReason("");
                  setDeleteStep("reason");
                }}
                disabled={deleteMut.isPending}
                className="px-3 py-2 text-sm rounded disabled:opacity-50 border border-[var(--danger)] text-[var(--danger)] hover:bg-[var(--danger)]/10"
              >
                {deleteMut.isPending ? "Working…" : "Schedule Deletion"}
              </button>
            </div>
            {toggleMut.isSuccess && <p className="text-[var(--success)] text-sm mt-2">Updated.</p>}
            {toggleMut.error && <p className="text-[var(--danger)] text-sm mt-2">{(toggleMut.error as Error).message}</p>}
            {deleteMut.isSuccess && (
              <p className="text-[var(--success)] text-sm mt-2">
                {deleteMut.data?.status === "deletion_already_scheduled"
                  ? "Deletion was already scheduled for this organization."
                  : "Deletion scheduled — a 30-day retention window has started."}
              </p>
            )}
            {deleteMut.error && (
              <p className="text-[var(--danger)] text-sm mt-2">
                {(deleteMut.error as any).body?.blockers
                  ? `Cannot schedule deletion: ${(deleteMut.error as any).body.blockers.join(", ")}`
                  : (deleteMut.error as Error).message}
              </p>
            )}

            {deleteStep === "reason" && (
              <div className="mt-3 p-3 border border-[var(--danger)]/40 rounded-lg bg-[var(--danger)]/5">
                <label className="block text-xs text-[var(--text-secondary)] mb-1">
                  Reason for scheduling deletion (required)
                </label>
                <textarea
                  autoFocus
                  value={deleteReason}
                  onChange={(e) => setDeleteReason(e.target.value)}
                  className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
                  rows={2}
                />
                <div className="flex justify-end gap-2 mt-2">
                  <button
                    onClick={() => setDeleteStep("idle")}
                    className="px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
                  >
                    Cancel
                  </button>
                  <button
                    onClick={() => setDeleteStep("confirm")}
                    disabled={!deleteReason.trim()}
                    className="px-3 py-1.5 text-xs rounded bg-[var(--danger)] text-black disabled:opacity-40"
                  >
                    Continue
                  </button>
                </div>
              </div>
            )}
          </>
        )}

        <ConfirmDialog
          open={!!confirmToggle}
          onOpenChange={(o) => !o && setConfirmToggle(null)}
          title={confirmToggle?.active ? "Activate organization" : "Suspend organization"}
          description={`This will ${confirmToggle?.active ? "restore" : "suspend"} access for tenant "${detail?.name ?? id}". Suspending blocks all mail delivery and login for this organization's users.`}
          requireTypedName={detail?.slug || detail?.name || ""}
          danger={!confirmToggle?.active}
          pending={toggleMut.isPending}
          onConfirm={() => {
            if (!confirmToggle) return;
            toggleMut.mutate(confirmToggle.active, { onSuccess: () => setConfirmToggle(null) });
          }}
        />

        <ConfirmDialog
          open={deleteStep === "confirm"}
          onOpenChange={(o) => !o && setDeleteStep("idle")}
          title="Schedule organization deletion"
          description={`This schedules "${detail?.name ?? id}" for deletion after a 30-day retention window. The organization must have zero active domains and zero active mailboxes. This does not immediately delete billing or audit history.`}
          requireTypedName={detail?.domain || ""}
          confirmLabel="Schedule Deletion"
          danger
          pending={deleteMut.isPending}
          onConfirm={() => {
            if (!detail) return;
            deleteMut.mutate(
              { confirm_domain: detail.domain, reason: deleteReason.trim() },
              { onSuccess: () => setDeleteStep("idle") },
            );
          }}
        />
      </div>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex justify-between gap-4">
      <dt className="text-[var(--text-secondary)]">{label}</dt>
      <dd className="text-[var(--text-primary)] text-right">{value}</dd>
    </div>
  );
}
