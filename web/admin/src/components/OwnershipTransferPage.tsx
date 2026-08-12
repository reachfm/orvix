import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { Send, Check, X } from "lucide-react";
import { api } from "../api";

export default function OwnershipTransferPage() {
  const [email, setEmail] = useState("");
  const [token, setToken] = useState("");

  const requestTransfer = useMutation({
    mutationFn: () => api.requestOwnershipTransfer(email),
    onSuccess: () => setEmail(""),
  });

  const acceptTransfer = useMutation({
    mutationFn: () => api.acceptOwnershipTransfer(token),
    onSuccess: () => setToken(""),
  });

  const cancelTransfer = useMutation({
    mutationFn: () => api.cancelOwnershipTransfer(),
  });

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Ownership Transfer</h2>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Send className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Request Transfer</h3>
        </div>
        <p className="text-sm text-[var(--text-secondary)] mb-4">Transfer organization ownership to another member. They must accept within 48 hours.</p>
        <div className="flex gap-2">
          <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="new-owner@example.com"
            className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <button onClick={() => requestTransfer.mutate()}
            disabled={requestTransfer.isPending || !email}
            className="bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
            {requestTransfer.isPending ? "Requesting..." : "Request"}
          </button>
        </div>
        {requestTransfer.isSuccess && <p className="text-[var(--success)] text-sm mt-2">Transfer requested. The recipient must accept.</p>}
        {requestTransfer.error && <p className="text-[var(--danger)] text-sm mt-2">{requestTransfer.error.message}</p>}
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Check className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Accept Transfer</h3>
        </div>
        <p className="text-sm text-[var(--text-secondary)] mb-4">If you received an ownership transfer token, enter it here to accept.</p>
        <div className="flex gap-2">
          <input value={token} onChange={(e) => setToken(e.target.value)} placeholder="Transfer token"
            className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <button onClick={() => acceptTransfer.mutate()}
            disabled={acceptTransfer.isPending || !token}
            className="bg-[var(--success)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--success)] disabled:opacity-50">
            {acceptTransfer.isPending ? "Accepting..." : "Accept"}
          </button>
        </div>
        {acceptTransfer.isSuccess && <p className="text-[var(--success)] text-sm mt-2">Ownership transferred successfully.</p>}
      </div>

      <button onClick={() => cancelTransfer.mutate()}
        disabled={cancelTransfer.isPending}
        className="flex items-center gap-2 text-[var(--danger)] hover:text-[var(--danger)] text-sm">
        <X className="w-4 h-4" /> {cancelTransfer.isPending ? "Cancelling..." : "Cancel pending transfer"}
      </button>
    </div>
  );
}
