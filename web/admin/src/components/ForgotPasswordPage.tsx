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
      <div className="min-h-screen flex items-center justify-center bg-[#0C0E12]">
        <div className="w-full max-w-md bg-[#13161C] border border-[#2A2F3E] rounded-lg p-8 text-center">
          <p className="text-green-400 mb-4">Password reset link sent to {email}</p>
          <p className="text-sm text-gray-400">Check your inbox for the reset link.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-[#0C0E12]">
      <div className="w-full max-w-md bg-[#13161C] border border-[#2A2F3E] rounded-lg p-8">
        <h2 className="text-xl font-semibold text-white mb-6">Reset Password</h2>
        <p className="text-sm text-gray-400 mb-4">Enter your email and we'll send you a reset link.</p>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Email</label>
            <input type="email" value={email} onChange={(e) => setEmail(e.target.value)}
              className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => forgot.mutate()}
            disabled={forgot.isPending || !email}
            className="w-full bg-[#4F7CFF] text-white rounded py-2.5 text-sm hover:bg-[#3D6AE8] transition disabled:opacity-50">
            {forgot.isPending ? "Sending..." : "Send Reset Link"}
          </button>
          {forgot.error && <p className="text-red-400 text-sm">{forgot.error.message}</p>}
        </div>
      </div>
    </div>
  );
}
