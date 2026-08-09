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
  const [qrError, setQrError] = useState(false);
  const [showSetup, setShowSetup] = useState(false);
  const qrCanvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (mfaOtpAuthUrl && qrCanvasRef.current) {
      QRCode.toCanvas(qrCanvasRef.current, mfaOtpAuthUrl, { width: 200, margin: 1 }, (err: Error | null | undefined) => {
        if (err) {
          console.error("QR render failed", err);
          setQrError(true);
        }
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
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Security</h2>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Smartphone className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Active Sessions</h3>
        </div>

        {sessionsLoading ? (
          <div className="flex items-center gap-2 text-[var(--text-secondary)] text-sm py-4">
            <Loader2 className="w-4 h-4 animate-spin" />
            Loading sessions...
          </div>
        ) : sessions.length === 0 ? (
          <p className="text-[var(--text-secondary)] text-sm">No active sessions found.</p>
        ) : (
          <div className="space-y-2">
            {sessions.map((s: any) => (
              <div key={s.id} className="flex items-center justify-between bg-[var(--bg-base)] rounded p-3">
                <div className="flex items-center gap-3 min-w-0">
                  {isMobile(s.user_agent || "") ? (
                    <Smartphone size={16} className="text-[var(--text-secondary)] shrink-0" />
                  ) : (
                    <Monitor size={16} className="text-[var(--text-secondary)] shrink-0" />
                  )}
                  <div className="min-w-0">
                    <span className="text-[var(--text-primary)] text-sm truncate block">{s.user_agent || "Unknown"}</span>
                    {s.ip && <span className="text-xs text-[var(--text-muted)]">{s.ip}</span>}
                  </div>
                  {s.current && <span className="text-xs px-2 py-0.5 rounded bg-[var(--accent)]/10 text-[var(--accent)] shrink-0">Current</span>}
                </div>
                <div className="flex items-center gap-3 shrink-0 ml-3">
                  <span className="text-xs text-[var(--text-muted)]">
                    {s.created_at ? new Date(s.created_at).toLocaleString() : ""}
                  </span>
                  {!s.current && (
                    <button onClick={() => revokeSession.mutate(s.id)}
                      disabled={revokeSession.isPending}
                      className="text-[var(--danger)] hover:bg-[var(--danger)]/10 p-1 rounded disabled:opacity-50">
                      <X size={14} />
                    </button>
                  )}
                </div>
              </div>
            ))}
            {nonCurrentSessions.length === 0 && sessions.length === 1 && (
              <p className="text-[var(--text-secondary)] text-sm pt-2">No other active sessions.</p>
            )}
          </div>
        )}
        {revokeSession.error && (
          <p className="text-[var(--danger)] text-sm mt-2">{(revokeSession.error as any)?.message || "Failed to revoke session"}</p>
        )}
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Lock className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Change Password</h3>
        </div>
        <div className="space-y-3">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Current Password</label>
            <input type="password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">New Password</label>
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)}
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          </div>
          <button onClick={() => changePassword.mutate()}
            disabled={changePassword.isPending || !currentPassword || !newPassword}
            className="flex items-center gap-2 bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
            <Lock className="w-4 h-4" /> {changePassword.isPending ? "Changing..." : "Update Password"}
          </button>
          {changePassword.isSuccess && <p className="text-[var(--success)] text-sm">Password updated.</p>}
          {changePassword.error && <p className="text-[var(--danger)] text-sm">{(changePassword.error as any)?.message || "Failed to update password"}</p>}
        </div>
      </div>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Shield className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Two-Factor Authentication</h3>
        </div>

        {mfaStatus ? (
          <div className="space-y-4">
            <div className="flex items-center gap-2">
              <span className={`px-2 py-1 text-xs rounded-full ${
                mfaStatus.enabled ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--warning)]/10 text-[var(--warning)]"
              }`}>
                {mfaStatus.enabled ? "Enabled" : "Disabled"}
              </span>
              {mfaStatus.label && <span className="text-xs text-[var(--text-secondary)]">({mfaStatus.label})</span>}
            </div>

            {!mfaStatus.enabled && !showSetup && (
              <button onClick={() => mfaBegin.mutate()}
                disabled={mfaBegin.isPending || !currentPassword}
                className="flex items-center gap-2 bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
                <Shield className="w-4 h-4" /> Setup MFA
              </button>
            )}

            {showSetup && (
              <div className="space-y-3 bg-[var(--bg-base)] rounded p-4">
                {qrError ? (
                  <p className="text-sm text-[var(--danger)]">QR code could not be rendered. Use the manual secret below.</p>
                ) : (
                  <>
                    <p className="text-sm text-[var(--text-primary)]">Scan this QR code with your authenticator app:</p>
                    <canvas ref={qrCanvasRef} className="mx-auto bg-white p-1 rounded" width="200" height="200" />
                  </>
                )}
                <details>
                  <summary className="text-xs text-[var(--text-secondary)] cursor-pointer hover:text-[var(--text-primary)]">Enter secret manually</summary>
                  <p className="text-xs font-mono text-[var(--accent)] bg-[var(--bg-surface)] p-2 rounded break-all mt-1">{mfaSecret}</p>
                </details>
                <div>
                  <label className="block text-sm text-[var(--text-secondary)] mb-1">Verification Code</label>
                  <input value={mfaSetupCode} onChange={(e) => setMfaSetupCode(e.target.value)} placeholder="000000"
                    className="w-full px-3 py-2 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm"
                    maxLength={6} />
                </div>
                <button onClick={() => mfaVerify.mutate()}
                  disabled={mfaVerify.isPending || mfaSetupCode.length !== 6}
                  className="flex items-center gap-2 bg-[var(--success)] text-[var(--bg-base)] rounded px-4 py-2 text-sm hover:bg-[var(--success)] disabled:opacity-50 font-medium">
                  <Check className="w-4 h-4" /> Verify & Enable
                </button>
              </div>
            )}

            {mfaStatus.enabled && (
              <div className="space-y-3 bg-[var(--bg-base)] rounded p-4">
                <div className="flex items-center gap-2 text-sm">
                  <AlertTriangle size={14} className="text-[var(--warning)]" />
                  <span className="text-[var(--text-secondary)]">To disable MFA, enter your password and a current code:</span>
                </div>
                <input value={mfaSetupCode} onChange={(e) => setMfaSetupCode(e.target.value)} placeholder="MFA code"
                  className="w-full px-3 py-2 bg-[var(--bg-surface)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm"
                  maxLength={6} />
                <button onClick={() => mfaDisable.mutate()}
                  disabled={mfaDisable.isPending || !currentPassword || mfaSetupCode.length !== 6}
                  className="flex items-center gap-2 bg-[var(--danger)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--danger)] disabled:opacity-50">
                  <X className="w-4 h-4" /> Disable MFA
                </button>
              </div>
            )}

            {mfaBegin.error && <p className="text-[var(--danger)] text-sm">{(mfaBegin.error as any)?.message || "Failed to begin MFA setup"}</p>}
            {mfaVerify.error && <p className="text-[var(--danger)] text-sm">{(mfaVerify.error as any)?.message || "Invalid verification code"}</p>}
            {mfaDisable.error && <p className="text-[var(--danger)] text-sm">{(mfaDisable.error as any)?.message || "Failed to disable MFA"}</p>}
          </div>
        ) : (
          <p className="text-[var(--text-secondary)] text-sm">Loading MFA status...</p>
        )}
      </div>
    </div>
  );
}
