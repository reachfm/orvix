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
      <h2 className="text-xl font-semibold text-white">Ownership Transfer</h2>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Send className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Request Transfer</h3>
        </div>
        <p className="text-sm text-gray-400 mb-4">Transfer organization ownership to another member. They must accept within 48 hours.</p>
        <div className="flex gap-2">
          <input value={email} onChange={(e) => setEmail(e.target.value)} placeholder="new-owner@example.com"
            className="flex-1 px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          <button onClick={() => requestTransfer.mutate()}
            disabled={requestTransfer.isPending || !email}
            className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
            {requestTransfer.isPending ? "Requesting..." : "Request"}
          </button>
        </div>
        {requestTransfer.isSuccess && <p className="text-green-400 text-sm mt-2">Transfer requested. The recipient must accept.</p>}
        {requestTransfer.error && <p className="text-red-400 text-sm mt-2">{requestTransfer.error.message}</p>}
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Check className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Accept Transfer</h3>
        </div>
        <p className="text-sm text-gray-400 mb-4">If you received an ownership transfer token, enter it here to accept.</p>
        <div className="flex gap-2">
          <input value={token} onChange={(e) => setToken(e.target.value)} placeholder="Transfer token"
            className="flex-1 px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          <button onClick={() => acceptTransfer.mutate()}
            disabled={acceptTransfer.isPending || !token}
            className="bg-[#34D399] text-white rounded px-4 py-2 text-sm hover:bg-[#2DB884] disabled:opacity-50">
            {acceptTransfer.isPending ? "Accepting..." : "Accept"}
          </button>
        </div>
        {acceptTransfer.isSuccess && <p className="text-green-400 text-sm mt-2">Ownership transferred successfully.</p>}
      </div>

      <button onClick={() => cancelTransfer.mutate()}
        disabled={cancelTransfer.isPending}
        className="flex items-center gap-2 text-red-400 hover:text-red-300 text-sm">
        <X className="w-4 h-4" /> {cancelTransfer.isPending ? "Cancelling..." : "Cancel pending transfer"}
      </button>
    </div>
  );
}
