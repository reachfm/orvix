import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";

declare global {
  interface Window { __navigate?: (path: string) => void; }
}

export default function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: () => api.login(email, password),
    onSuccess: () => window.location.reload(),
  });

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-6">Sign In</h2>
        <div className="space-y-4">
          <div>
            <label htmlFor="login-email" className="block text-sm text-[var(--text-secondary)] mb-1">Email</label>
            <input id="login-email" type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <div>
            <label htmlFor="login-password" className="block text-sm text-[var(--text-secondary)] mb-1">Password</label>
            <input id="login-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <button id="login-button" onClick={() => login.mutate()}
            disabled={login.isPending || !email || !password}
            className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50">
            {login.isPending ? "Signing in..." : "Sign In"}
          </button>
          {login.error && <p id="login-error" role="alert" className="text-[var(--danger)] text-sm">{login.error.message}</p>}
          <p className="text-sm text-[var(--text-muted)] mt-4">
            Don't have an account? <a href="/admin/signup" className="text-[var(--accent)] hover:underline">Create account</a>
          </p>
        </div>
      </div>
    </div>
  );
}
