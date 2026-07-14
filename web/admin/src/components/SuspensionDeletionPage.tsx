import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AlertTriangle, Trash2, Ban, Undo2 } from "lucide-react";
import { api } from "../api";

export default function SuspensionDeletionPage() {
  const queryClient = useQueryClient();
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleteReason, setDeleteReason] = useState("");
  const [showSuspendConfirm, setShowSuspendConfirm] = useState(false);
  const [suspendReason, setSuspendReason] = useState("");

  const { data: org } = useQuery({ queryKey: ["org"], queryFn: api.getCurrentOrganization });
  const { data: abuse } = useQuery({ queryKey: ["abuse"], queryFn: api.listAbuseSignals });

  const isSuspended = org?.status === "suspended";
  const isDeletionRequested = org?.deletion_state === "deletion_requested";

  const sendLimit = useQuery({ queryKey: ["send-limit"], queryFn: api.checkSendLimit });

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-white">Organization Status</h2>

      {org && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            {isSuspended ? (
              <Ban className="w-5 h-5 text-red-400" />
            ) : isDeletionRequested ? (
              <Trash2 className="w-5 h-5 text-red-400" />
            ) : (
              <AlertTriangle className="w-5 h-5 text-green-400" />
            )}
            <h3 className="text-lg font-medium text-white">
              Status: {isSuspended ? "Suspended" : isDeletionRequested ? "Deletion Requested" : "Active"}
            </h3>
          </div>
          <p className="text-sm text-gray-400">
            {isSuspended && "Your organization has been suspended. Contact support to reactivate."}
            {isDeletionRequested && "Deletion has been requested. This can be cancelled within 30 days."}
            {!isSuspended && !isDeletionRequested && "Your organization is active and operating normally."}
          </p>
        </div>
      )}

      {sendLimit.data && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <AlertTriangle className={`w-5 h-5 ${sendLimit.data.blocked ? "text-red-400" : "text-green-400"}`} />
            <h3 className="text-lg font-medium text-white">Send Limit Status</h3>
          </div>
          <div className="text-sm space-y-1">
            <p><span className="text-gray-400">Sent today: </span><span className="text-white">{sendLimit.data.sent_today || 0}</span></p>
            <p><span className="text-gray-400">Daily limit: </span><span className="text-white">{sendLimit.data.limit || "N/A"}</span></p>
            {sendLimit.data.blocked && <p className="text-red-400 mt-2">Sending is currently blocked due to excessive bounces.</p>}
          </div>
        </div>
      )}

      {!isSuspended && !isDeletionRequested && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <Trash2 className="w-5 h-5 text-red-400" />
            <h3 className="text-lg font-medium text-white">Danger Zone</h3>
          </div>
          <p className="text-sm text-gray-400 mb-4">Request deletion of your organization and all associated data.</p>
          <button onClick={() => setShowDeleteConfirm(true)}
            className="bg-red-500/10 border border-red-500/30 text-red-400 rounded px-4 py-2 text-sm hover:bg-red-500/20">
            Request Organization Deletion
          </button>
          {showDeleteConfirm && (
            <div className="mt-4 p-4 bg-red-500/5 border border-red-500/20 rounded">
              <p className="text-red-400 text-sm mb-3">This will permanently delete all mailboxes, domains, and data after a 30-day grace period.</p>
              <div className="flex gap-2">
                <input value={deleteReason} onChange={(e) => setDeleteReason(e.target.value)} placeholder="Reason (optional)"
                  className="flex-1 px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
                <button className="bg-red-500 text-white rounded px-4 py-2 text-sm hover:bg-red-600">Confirm Deletion</button>
                <button onClick={() => setShowDeleteConfirm(false)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
