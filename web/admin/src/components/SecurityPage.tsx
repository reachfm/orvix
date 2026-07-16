import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Shield, Monitor, Smartphone, X, Key, Lock, Check, AlertTriangle, Loader2 } from "lucide-react";
import { api } from "../api";
import QRCode from "qrcode";

function isMobile(userAgent: string): boolean {
  return /mobile|android|iphone|ipad/i.test(userAgent);
}

export default function SecurityPage() {
  const queryClient = useQueryClient();
  const { data: sessionsData, isLoading: sessionsLoading } = useQuery({ queryKey: ["sessions"], queryFn: api.listSessions });
  const { data: mfaStatus } = useQuery({ queryKey: ["mfaStatus"], queryFn: api.getMFAStatus });

  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [mfaSetupCode, setMfaSetupCode] = useState("");
  const [mfaSecret, setMfaSecret] = useState("");
  const [mfaOtpAuthUrl, setMfaOtpAuthUrl] = useState("");
  const [showSetup, setShowSetup] = useState(false);
  const qrCanvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (mfaOtpAuthUrl && qrCanvasRef.current) {
      QRCode.toCanvas(qrCanvasRef.current, mfaOtpAuthUrl, { width: 200, margin: 1 }, (err) => {
        if (err) console.error("QR render failed", err);
      });
    }
  }, [mfaOtpAuthUrl]);

  const sessions: any[] = (sessionsData as any)?.sessions || [];
  const nonCurrentSessions = sessions.filter((s: any) => !s.current);

  const changePassword = useMutation({
    mutationFn: () => api.changePassword({ current_password: currentPassword, new_password: newPassword }),
    onSuccess: () => { setCurrentPassword(""); setNewPassword(""); },
  });

  const revokeSession = useMutation({
    mutationFn: (id: string) => api.revokeSession(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["sessions"] }),
  });

  const mfaBegin = useMutation({
    mutationFn: () => api.setupMFABegin({ current_password: currentPassword }),
    onSuccess: (data: any) => {
      setMfaSecret(data.secret);
      setMfaOtpAuthUrl(data.otpauth_url);
      setShowSetup(true);
    },
  });

  const mfaVerify = useMutation({
    mutationFn: () => api.setupMFAVerify(mfaSetupCode),
    onSuccess: () => {
      setShowSetup(false);
      setMfaSetupCode("");
      setMfaSecret("");
      queryClient.invalidateQueries({ queryKey: ["mfaStatus"] });
    },
  });

  const mfaDisable = useMutation({
    mutationFn: () => api.disableMFA({ current_password: currentPassword, code: mfaSetupCode }),
    onSuccess: () => {
      setMfaSetupCode("");
      queryClient.invalidateQueries({ queryKey: ["mfaStatus"] });
    },
  });

  return (
    <div className="space-y-6 max-w-2xl">
      <h2 className="text-xl font-semibold text-white">Security</h2>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Smartphone className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Active Sessions</h3>
        </div>

        {sessionsLoading ? (
          <div className="flex items-center gap-2 text-[#8B92A8] text-sm py-4">
            <Loader2 className="w-4 h-4 animate-spin" />
            Loading sessions...
          </div>
        ) : sessions.length === 0 ? (
          <p className="text-[#8B92A8] text-sm">No active sessions found.</p>
        ) : (
          <div className="space-y-2">
            {sessions.map((s: any) => (
              <div key={s.id} className="flex items-center justify-between bg-[#0C0E12] rounded p-3">
                <div className="flex items-center gap-3 min-w-0">
                  {isMobile(s.user_agent || "") ? (
                    <Smartphone size={16} className="text-[#8B92A8] shrink-0" />
                  ) : (
                    <Monitor size={16} className="text-[#8B92A8] shrink-0" />
                  )}
                  <div className="min-w-0">
                    <span className="text-white text-sm truncate block">{s.user_agent || "Unknown"}</span>
                    {s.ip && <span className="text-xs text-[#555D73]">{s.ip}</span>}
                  </div>
                  {s.current && <span className="text-xs px-2 py-0.5 rounded bg-[#4F7CFF]/10 text-[#4F7CFF] shrink-0">Current</span>}
                </div>
                <div className="flex items-center gap-3 shrink-0 ml-3">
                  <span className="text-xs text-[#555D73]">
                    {s.created_at ? new Date(s.created_at).toLocaleString() : ""}
                  </span>
                  {!s.current && (
                    <button onClick={() => revokeSession.mutate(s.id)}
                      disabled={revokeSession.isPending}
                      className="text-[#F87171] hover:bg-[#F87171]/10 p-1 rounded disabled:opacity-50">
                      <X size={14} />
                    </button>
                  )}
                </div>
              </div>
            ))}
            {nonCurrentSessions.length === 0 && sessions.length === 1 && (
              <p className="text-[#8B92A8] text-sm pt-2">No other active sessions.</p>
            )}
          </div>
        )}
        {revokeSession.error && (
          <p className="text-[#F87171] text-sm mt-2">{(revokeSession.error as any)?.message || "Failed to revoke session"}</p>
        )}
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Lock className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Change Password</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-gray-400 mb-1">Current Password</label>
            <input type="password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-1">New Password</label>
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
          </div>
          <button onClick={() => changePassword.mutate()}
            disabled={changePassword.isPending || !currentPassword || !newPassword}
            className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
            <Lock className="w-4 h-4" /> {changePassword.isPending ? "Changing..." : "Update Password"}
          </button>
          {changePassword.isSuccess && <p className="text-[#34D399] text-sm">Password updated.</p>}
          {changePassword.error && <p className="text-[#F87171] text-sm">{(changePassword.error as any)?.message || "Failed to update password"}</p>}
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Shield className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Two-Factor Authentication</h3>
        </div>

        {mfaStatus ? (
          <div className="space-y-4">
            <div className="flex items-center gap-2">
              <span className={`px-2 py-1 text-xs rounded-full ${
                mfaStatus.enabled ? "bg-[#34D399]/10 text-[#34D399]" : "bg-[#FBBF24]/10 text-[#FBBF24]"
              }`}>
                {mfaStatus.enabled ? "Enabled" : "Disabled"}
              </span>
              {mfaStatus.label && <span className="text-xs text-[#8B92A8]">({mfaStatus.label})</span>}
            </div>

            {!mfaStatus.enabled && !showSetup && (
              <button onClick={() => mfaBegin.mutate()}
                disabled={mfaBegin.isPending || !currentPassword}
                className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8] disabled:opacity-50">
                <Shield className="w-4 h-4" /> Setup MFA
              </button>
            )}

            {showSetup && (
              <div className="space-y-3 bg-[#0C0E12] rounded p-4">
                <p className="text-sm text-[#E8EAF0]">Scan this QR code with your authenticator app:</p>
                <canvas ref={qrCanvasRef} className="mx-auto bg-white p-1 rounded" width="200" height="200" />
                <details>
                  <summary className="text-xs text-[#8B92A8] cursor-pointer hover:text-[#E8EAF0]">Enter secret manually</summary>
                  <p className="text-xs font-mono text-[#4F7CFF] bg-[#13161C] p-2 rounded break-all mt-1">{mfaSecret}</p>
                </details>
                <div>
                  <label className="block text-sm text-gray-400 mb-1">Verification Code</label>
                  <input value={mfaSetupCode} onChange={(e) => setMfaSetupCode(e.target.value)} placeholder="000000"
                    className="w-full px-3 py-2 bg-[#13161C] border border-[#2A2F3E] rounded text-white text-sm"
                    maxLength={6} />
                </div>
                <button onClick={() => mfaVerify.mutate()}
                  disabled={mfaVerify.isPending || mfaSetupCode.length !== 6}
                  className="flex items-center gap-2 bg-[#34D399] text-[#0C0E12] rounded px-4 py-2 text-sm hover:bg-[#2CC48A] disabled:opacity-50 font-medium">
                  <Check className="w-4 h-4" /> Verify & Enable
                </button>
              </div>
            )}

            {mfaStatus.enabled && (
              <div className="space-y-3 bg-[#0C0E12] rounded p-4">
                <div className="flex items-center gap-2 text-sm">
                  <AlertTriangle size={14} className="text-[#FBBF24]" />
                  <span className="text-[#8B92A8]">To disable MFA, enter your password and a current code:</span>
                </div>
                <input value={mfaSetupCode} onChange={(e) => setMfaSetupCode(e.target.value)} placeholder="MFA code"
                  className="w-full px-3 py-2 bg-[#13161C] border border-[#2A2F3E] rounded text-white text-sm"
                  maxLength={6} />
                <button onClick={() => mfaDisable.mutate()}
                  disabled={mfaDisable.isPending || !currentPassword || mfaSetupCode.length !== 6}
                  className="flex items-center gap-2 bg-[#F87171] text-white rounded px-4 py-2 text-sm hover:bg-[#E05555] disabled:opacity-50">
                  <X className="w-4 h-4" /> Disable MFA
                </button>
              </div>
            )}

            {mfaBegin.error && <p className="text-[#F87171] text-sm">{(mfaBegin.error as any)?.message || "Failed to begin MFA setup"}</p>}
            {mfaVerify.error && <p className="text-[#F87171] text-sm">{(mfaVerify.error as any)?.message || "Invalid verification code"}</p>}
            {mfaDisable.error && <p className="text-[#F87171] text-sm">{(mfaDisable.error as any)?.message || "Failed to disable MFA"}</p>}
          </div>
        ) : (
          <p className="text-[#8B92A8] text-sm">Loading MFA status...</p>
        )}
      </div>
    </div>
  );
}
