import { useState } from "react";
import { Trash2 } from "lucide-react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useSslCertificatesQuery, useAcmeStatusQuery, useSslExpiryWarningsQuery } from "../queries";
import { useReloadSslCertificatesMutation, useDeleteSslCertificateMutation } from "../mutations";
import { Loading, ErrorBox, Empty } from "./StateViews";
import SslUploadForm from "./SslUploadForm";
import type { CertInfo } from "../contract";

export default function SslPanel() {
  const [confirmDelete, setConfirmDelete] = useState<CertInfo | null>(null);
  const [uploading, setUploading] = useState(false);
  const certsQ = useSslCertificatesQuery();
  const acmeQ = useAcmeStatusQuery();
  const warningsQ = useSslExpiryWarningsQuery();
  const reloadMut = useReloadSslCertificatesMutation();
  const deleteMut = useDeleteSslCertificateMutation();

  // Real response envelope is {runtime, uploaded, expiry_warnings, ...}
  // — not a bare array. The certificate table shows both sources; only
  // "uploaded" certs are deletable (runtime certs are file-based).
  const certs = [...(certsQ.data?.runtime ?? []), ...(certsQ.data?.uploaded ?? [])];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="text-sm text-[var(--text-secondary)]">
          ACME: <span className="text-[var(--text-primary)]">{acmeQ.isLoading ? "…" : acmeQ.data?.acme_enabled ? "enabled" : "not implemented (manual import only)"}</span>
          {(warningsQ.data?.warnings.length ?? 0) > 0 && <span className="ml-3 text-[var(--warning)]">{warningsQ.data!.warnings.length} expiring soon</span>}
        </div>
        <div className="flex gap-2">
          <button onClick={() => setUploading((v) => !v)} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded">{uploading ? "Cancel upload" : "Upload certificate"}</button>
          <button disabled={reloadMut.isPending} onClick={() => reloadMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Reload certificates</button>
        </div>
      </div>
      {uploading && <SslUploadForm onDone={() => setUploading(false)} />}
      {certsQ.isLoading ? <Loading /> : certsQ.error ? <ErrorBox error={certsQ.error} /> : certs.length === 0 ? <Empty text="No certificates configured" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Name</th><th className="text-left p-3 text-[var(--text-secondary)]">Source</th><th className="text-left p-3 text-[var(--text-secondary)]">Expires</th><th className="text-right p-3 text-[var(--text-secondary)]">Action</th></tr></thead>
          <tbody>{certs.map((c) => (
            <tr key={`${c.source}:${c.id}`} className="border-b border-[var(--border)]">
              <td className="p-3 text-[var(--text-primary)]">{c.name || c.common_name}</td>
              <td className="p-3 text-[var(--text-secondary)]">{c.source}</td>
              <td className="p-3 text-[var(--text-secondary)]">{c.not_after ?? "—"} ({c.days_remaining}d)</td>
              <td className="p-3 text-right">{c.source === "uploaded" && <button onClick={() => setConfirmDelete(c)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"><Trash2 size={14} /></button>}</td>
            </tr>
          ))}</tbody></table>
        </div>
      )}
      <ConfirmDialog
        open={!!confirmDelete}
        onOpenChange={(o) => !o && setConfirmDelete(null)}
        title="Delete certificate"
        description="This permanently removes the uploaded certificate."
        requireTypedName={confirmDelete?.name || ""}
        danger
        pending={deleteMut.isPending}
        onConfirm={() => confirmDelete && deleteMut.mutate(confirmDelete.id, { onSuccess: () => setConfirmDelete(null) })}
      />
    </div>
  );
}
