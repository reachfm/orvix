import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";

export default function ForgotPasswordPage() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState(false);

  const forgot = useMutation({
    mutationFn: () => api.forgotPassword(email),
    onSuccess: () => setSent(true),
  });

  if (sent) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
        <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8 text-center">
          <p className="text-[var(--success)] mb-4">Password reset link sent to {email}</p>
          <p className="text-sm text-[var(--text-secondary)]">Check your inbox for the reset link.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-6">Forgot Password</h2>
        <p className="text-sm text-[var(--text-secondary)] mb-4">Enter your email and we'll send you a reset link.</p>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Email</label>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <button onClick={() => forgot.mutate()}
            disabled={forgot.isPending || !email}
            className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50">
            {forgot.isPending ? "Sending..." : "Send Reset Link"}
          </button>
          {forgot.error && <p className="text-[var(--danger)] text-sm">{forgot.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
