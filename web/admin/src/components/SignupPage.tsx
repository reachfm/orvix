import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api";

export default function SignupPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");

  const signup = useMutation({
    mutationFn: () => api.signup({ email, password, name }),
    onSuccess: () => window.location.reload(),
  });

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-6">Create Account</h2>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Name</label>
            <input value={name} onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Email</label>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Password</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <button onClick={() => signup.mutate()}
            disabled={signup.isPending || !email || !password}
            className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50">
            {signup.isPending ? "Creating..." : "Create Account"}
          </button>
          {signup.error && <p className="text-[var(--danger)] text-sm">{signup.error.message}</p>}
          <p className="text-sm text-[var(--text-muted)] mt-4">
            Already have an account? <a href="/admin/login" className="text-[var(--accent)] hover:underline">Sign in</a>
          </p>
        </div>
      </div>
    </div>
  );
}
