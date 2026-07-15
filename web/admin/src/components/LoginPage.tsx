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
    <div className="min-h-screen flex items-center justify-center bg-[#0C0E12]">
      <div className="w-full max-w-md bg-[#13161C] border border-[#2A2F3E] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-white mb-6">Sign In</h2>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Email</label>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">Password</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => login.mutate()}
            disabled={login.isPending || !email || !password}
            className="w-full bg-[#4F7CFF] text-white rounded py-2.5 text-sm hover:bg-[#3D6AE8] transition disabled:opacity-50">
            {login.isPending ? "Signing in..." : "Sign In"}
          </button>
          {login.error && <p className="text-red-400 text-sm">{login.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
