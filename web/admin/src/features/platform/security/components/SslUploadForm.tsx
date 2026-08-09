import { useState } from "react";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import { useUploadSslCertificateMutation } from "../mutations";

// Matches AdminSslUploadCertificate's exact request contract
// (internal/api/handlers/enterprise_admin_ssl.go): name/cert_pem/
// key_pem, with the same client-side format checks the handler itself
// enforces server-side (BEGIN CERTIFICATE / PRIVATE KEY markers) so
// the operator gets immediate feedback instead of a round-trip 400.
// The private key is held only in local component state, cleared
// immediately on success or cancel, never logged, and the response
// (UploadCertificateResponse) contains no key material to redisplay.
export default function SslUploadForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [certPem, setCertPem] = useState("");
  const [keyPem, setKeyPem] = useState("");
  const [confirming, setConfirming] = useState(false);
  const uploadMut = useUploadSslCertificateMutation();

  const nameValid = /^[\x20-\x7E]{1,128}$/.test(name) && !name.includes("/") && !name.includes("\\");
  const certValid = certPem.includes("BEGIN CERTIFICATE");
  const keyValid = keyPem.includes("PRIVATE KEY");
  const canSubmit = nameValid && certValid && keyValid;

  const clearSecrets = () => setKeyPem("");

  return (
    <div className="bg-[var(--bg-elevated)] border border-[var(--border)] rounded-xl p-4 mb-4 space-y-3">
      <h4 className="text-sm font-semibold text-[var(--text-primary)]">Upload certificate</h4>
      <div>
        <label className="block text-xs text-[var(--text-secondary)] mb-1">Name</label>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="mail.example.com"
          className="w-full px-2 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)]"
        />
        {name && !nameValid && <p className="text-[var(--danger)] text-xs mt-1">Name must be ≤128 printable characters with no slashes.</p>}
      </div>
      <div>
        <label className="block text-xs text-[var(--text-secondary)] mb-1">Certificate (PEM)</label>
        <textarea
          value={certPem}
          onChange={(e) => setCertPem(e.target.value)}
          rows={4}
          placeholder="-----BEGIN CERTIFICATE-----"
          className="w-full px-2 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs font-mono text-[var(--text-primary)]"
        />
        {certPem && !certValid && <p className="text-[var(--danger)] text-xs mt-1">Must contain a BEGIN CERTIFICATE block.</p>}
      </div>
      <div>
        <label className="block text-xs text-[var(--text-secondary)] mb-1">Private key (PEM)</label>
        <textarea
          value={keyPem}
          onChange={(e) => setKeyPem(e.target.value)}
          rows={4}
          placeholder="-----BEGIN PRIVATE KEY-----"
          className="w-full px-2 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-xs font-mono text-[var(--text-primary)]"
        />
        {keyPem && !keyValid && <p className="text-[var(--danger)] text-xs mt-1">Must contain a PRIVATE KEY block.</p>}
        <p className="text-[var(--text-muted)] text-xs mt-1">The private key is never stored in this browser beyond this form and is cleared as soon as the upload finishes or is cancelled.</p>
      </div>

      <div className="flex gap-2">
        <button
          disabled={!canSubmit || uploadMut.isPending}
          onClick={() => setConfirming(true)}
          className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
        >
          {uploadMut.isPending ? "Uploading…" : "Upload"}
        </button>
        <button onClick={() => { clearSecrets(); onDone(); }} className="px-3 py-1.5 text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]">Cancel</button>
      </div>
      {uploadMut.isError && <p className="text-[var(--danger)] text-xs">{(uploadMut.error as Error).message}</p>}
      {uploadMut.isSuccess && <p className="text-[var(--success)] text-xs">Uploaded — {uploadMut.data.fingerprint_sha256.slice(0, 16)}…</p>}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Upload certificate"
        description={`This installs a new TLS certificate under the name "${name}". The private key you pasted will be sent once and never displayed again.`}
        requireTypedName={name}
        pending={uploadMut.isPending}
        onConfirm={() => {
          uploadMut.mutate({ name, cert_pem: certPem, key_pem: keyPem }, {
            onSuccess: () => { clearSecrets(); setConfirming(false); onDone(); },
            onError: () => { clearSecrets(); setConfirming(false); },
          });
        }}
      />
    </div>
  );
}
