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
    <div className="min-h-screen flex items-center justify-center bg-[#0C0E12]">
      <div className="w-full max-w-md bg-[#13161C] border border-[#2A2F3E] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-white mb-6">Reset Password</h2>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Reset Token</label>
            <input value={token} onChange={(e) => setToken(e.target.value)}
              className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">New Password</label>
            <input type="password" value={password} onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => reset.mutate()}
            disabled={reset.isPending || !token || !password}
            className="w-full bg-[#4F7CFF] text-white rounded py-2.5 text-sm hover:bg-[#3D6AE8] transition disabled:opacity-50">
            {reset.isPending ? "Resetting..." : "Reset Password"}
          </button>
          {reset.error && <p className="text-red-400 text-sm">{reset.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
