import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";

export default function ResetPasswordPage() {
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");

  const reset = useMutation({
    mutationFn: () => api.resetPassword(token, password),
    onSuccess: () => window.location.reload(),
  });

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-6">Reset Password</h2>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Reset Token</label>
            <input value={token} onChange={(e) => setToken(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">New Password</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <button onClick={() => reset.mutate()}
            disabled={reset.isPending || !token || !password}
            className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50">
            {reset.isPending ? "Resetting..." : "Reset Password"}
          </button>
          {reset.error && <p className="text-[var(--danger)] text-sm">{reset.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
