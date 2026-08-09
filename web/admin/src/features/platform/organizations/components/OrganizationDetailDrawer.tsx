import { useState } from "react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useOrganizationDetailQuery } from "../queries";
import { useSetOrganizationActiveMutation } from "../mutations";

function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export default function OrganizationDetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const [confirmToggle, setConfirmToggle] = useState<{ active: boolean } | null>(null);
  const detailQ = useOrganizationDetailQuery(id);
  const toggleMut = useSetOrganizationActiveMutation(id);

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
              <span className={`inline-block mt-2 px-2 py-0.5 rounded text-xs ${detailQ.data.active ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--danger)]/10 text-[var(--danger)]"}`}>
                {detailQ.data.status_label || (detailQ.data.active ? "active" : "suspended")}
              </span>
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

            <button
              onClick={() => setConfirmToggle({ active: !detailQ.data!.active })}
              disabled={toggleMut.isPending}
              className={`px-3 py-2 text-sm rounded disabled:opacity-50 ${detailQ.data.active ? "bg-[var(--danger)] text-black" : "bg-[var(--success)] text-black"}`}
            >
              {toggleMut.isPending ? "Working…" : detailQ.data.active ? "Suspend organization" : "Activate organization"}
            </button>
            {toggleMut.isSuccess && <p className="text-[var(--success)] text-sm mt-2">Updated.</p>}
            {toggleMut.error && <p className="text-[var(--danger)] text-sm mt-2">{(toggleMut.error as Error).message}</p>}
          </>
        )}

        <ConfirmDialog
          open={!!confirmToggle}
          onOpenChange={(o) => !o && setConfirmToggle(null)}
          title={confirmToggle?.active ? "Activate organization" : "Suspend organization"}
          description={`This will ${confirmToggle?.active ? "restore" : "suspend"} access for tenant "${detailQ.data?.name ?? id}". Suspending blocks all mail delivery and login for this organization's users.`}
          requireTypedName={detailQ.data?.slug || detailQ.data?.name || ""}
          danger={!confirmToggle?.active}
          pending={toggleMut.isPending}
          onConfirm={() => {
            if (!confirmToggle) return;
            toggleMut.mutate(confirmToggle.active, { onSuccess: () => setConfirmToggle(null) });
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
