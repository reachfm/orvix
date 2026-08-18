import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { KeyRound, CheckCircle2, AlertCircle, Loader2 } from "lucide-react";
import { api, ApiError } from "../api";

/**
 * Public invitation-accept page (POST /auth/invitations/accept).
 *
 * This is the ONLY activation path for a PSA-created organization: the
 * org starts pending_activation and becomes operational when the
 * invited owner redeems the one-time token here (token + password; the
 * email comes from the invitation row server-side, never from this
 * form). On success the user is redirected to the login page.
 *
 * Error mapping follows the pinned contract:
 *   - 404 NOT_FOUND               -> invitation not found (no existence disclosure)
 *   - 409 INVALID_STATE_TRANSITION -> revoked / expired / already used
 *   - 409 CONFLICT                -> an account already exists for the invited email
 */
function invitationErrorMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : "Invitation acceptance failed.";
  switch (err.code) {
    case "NOT_FOUND":
      return "This invitation could not be found. Check the link or ask the sender for a fresh invitation.";
    case "INVALID_STATE_TRANSITION":
      return "This invitation is no longer usable — it was revoked, expired, or already accepted.";
    case "CONFLICT":
      return "An account already exists for the invited email. Sign in with that account instead.";
    case "VALIDATION_FAILED":
      return err.message || "Check the token and password and try again.";
    case "UNAVAILABLE":
      return "The invitation service is temporarily unavailable. Try again shortly.";
    default:
      return err.message || "Invitation acceptance failed.";
  }
}

export default function InvitationAcceptPage() {
  const [token, setToken] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [copiedFromUrl, setCopiedFromUrl] = useState(false);

  // Pre-fill the token from ?token= when present (the inviter shares
  // the accept URL with the token embedded).
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const t = (params.get("token") || "").trim();
    if (t) {
      setToken(t);
      setCopiedFromUrl(true);
    }
  }, []);

  const accept = useMutation({
    mutationFn: () => api.acceptInvitation({ token: token.trim(), password, name: name.trim() || undefined }),
    onSuccess: () => {
      // Redirect to login after a short confirmation so the user sees
      // the acceptance result before the console reloads.
      window.setTimeout(() => {
        window.location.assign("/admin/login");
      }, 2500);
    },
  });

  const valid = token.trim().length > 0 && password.length >= 8;

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)] px-4">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        <div className="flex items-center gap-2 mb-2">
          <KeyRound className="w-5 h-5 text-[var(--accent)]" />
          <h2 className="text-xl font-semibold text-[var(--text-primary)]">Accept invitation</h2>
        </div>
        <p className="text-sm text-[var(--text-secondary)] mb-6">
          You were invited to an Orvix organization. Enter the one-time invitation token and choose a password to
          activate your membership.
        </p>

        {accept.isSuccess ? (
          <div className="border border-[var(--success)]/40 rounded-lg p-4 bg-[var(--success)]/5 text-center" role="status">
            <CheckCircle2 className="w-8 h-8 text-[var(--success)] mx-auto mb-2" />
            <p className="text-sm font-medium text-[var(--text-primary)]">Invitation accepted</p>
            <p className="text-xs text-[var(--text-secondary)] mt-1">
              Your organization {accept.data.organization_active ? "is now active" : "is pending activation"}.
              Redirecting to sign in…
            </p>
            <a href="/admin/login" className="inline-block mt-3 text-sm text-[var(--accent)] hover:underline">
              Go to sign in
            </a>
          </div>
        ) : (
          <div className="space-y-4">
            {accept.error && (
              <p role="alert" className="text-[var(--danger)] text-sm border border-[var(--danger)]/30 rounded-lg p-3">
                {invitationErrorMessage(accept.error)}
              </p>
            )}

            <div>
              <label htmlFor="invite-token" className="block text-sm text-[var(--text-secondary)] mb-1">
                Invitation token
              </label>
              <input
                id="invite-token"
                type="text"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="Paste the one-time token from your invitation"
                autoComplete="off"
                className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm font-mono"
              />
              {copiedFromUrl && token && (
                <p className="text-xs text-[var(--text-muted)] mt-1">Token pre-filled from the invitation link.</p>
              )}
            </div>

            <div>
              <label htmlFor="invite-password" className="block text-sm text-[var(--text-secondary)] mb-1">
                Password
              </label>
              <input
                id="invite-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="At least 8 characters"
                autoComplete="new-password"
                className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm"
              />
            </div>

            <div>
              <label htmlFor="invite-name" className="block text-sm text-[var(--text-secondary)] mb-1">
                Name <span className="text-[var(--text-muted)]">(optional)</span>
              </label>
              <input
                id="invite-name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Your display name"
                autoComplete="name"
                className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm"
              />
            </div>

            <button
              id="invite-accept-button"
              onClick={() => accept.mutate()}
              disabled={accept.isPending || !valid}
              className="w-full inline-flex items-center justify-center gap-2 bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50"
            >
              {accept.isPending && <Loader2 size={14} className="animate-spin" />}
              {accept.isPending ? "Accepting…" : "Accept invitation"}
            </button>

            {!valid && (
              <p className="text-xs text-[var(--text-muted)] flex items-center gap-1">
                <AlertCircle size={12} />
                {token.trim().length === 0 ? "The invitation token is required." : "Password must be at least 8 characters."}
              </p>
            )}

            <p className="text-sm text-[var(--text-muted)]">
              Already have an account?{" "}
              <a href="/admin/login" className="text-[var(--accent)] hover:underline">Sign in</a>
            </p>
          </div>
        )}
      </div>
    </div>
  );
}
