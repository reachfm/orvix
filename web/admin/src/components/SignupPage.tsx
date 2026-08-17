import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { api } from "../api";

type Stage = "register" | "check-email" | "verify" | "success";

const RESEND_COOLDOWN_SECONDS = 60;

export default function SignupPage() {
  const [stage, setStage] = useState<Stage>("register");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [codeState, setCodeState] = useState<"idle" | "invalid" | "expired">("idle");
  const [cooldown, setCooldown] = useState(0);
  const cooldownTimer = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    return () => {
      if (cooldownTimer.current) clearInterval(cooldownTimer.current);
    };
  }, []);

  const startCooldown = () => {
    setCooldown(RESEND_COOLDOWN_SECONDS);
    if (cooldownTimer.current) clearInterval(cooldownTimer.current);
    cooldownTimer.current = setInterval(() => {
      setCooldown((s) => {
        if (s <= 1) {
          if (cooldownTimer.current) clearInterval(cooldownTimer.current);
          return 0;
        }
        return s - 1;
      });
    }, 1000);
  };

  const start = useMutation({
    mutationFn: () => api.signupStart({ email, password, name }),
    onSuccess: () => {
      setStage("check-email");
      startCooldown();
    },
  });

  const resend = useMutation({
    mutationFn: () => api.signupResend(email),
    onSuccess: () => startCooldown(),
  });

  const verify = useMutation({
    mutationFn: () => api.signupVerify(email, code),
    onSuccess: () => {
      setStage("success");
      setCodeState("idle");
      // Navigate to the canonical Organization landing route, NOT a
      // reload of /admin/signup. window.location.replace() swaps the
      // history entry, so the browser URL becomes the real admin/customer
      // portal route, a refresh stays on a valid authenticated
      // destination, and the Back button cannot reopen the OTP
      // completion step. The signup session cookies were already set by
      // /auth/signup/verify, so /admin resolves /me and renders the
      // Organization portal directly.
      window.location.replace("/admin");
    },
    onError: (err: any) => {
      const msg = String(err?.message || "").toLowerCase();
      if (msg.includes("expired")) {
        setCodeState("expired");
      } else {
        setCodeState("invalid");
      }
    },
  });

  const inputClass =
    "w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm";
  const labelClass = "block text-sm text-[var(--text-secondary)] mb-1";

  return (
    <div className="min-h-screen flex items-center justify-center bg-[var(--bg-base)]">
      <div className="w-full max-w-md bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-8">
        {stage === "register" && (
          <>
            <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-6">Create Account</h2>
            <div className="space-y-4">
              <div>
                <label className={labelClass}>Name</label>
                <input value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
              </div>
              <div>
                <label className={labelClass}>Email</label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  className={inputClass}
                />
              </div>
              <div>
                <label className={labelClass}>Password</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  className={inputClass}
                />
              </div>
              <button
                onClick={() => start.mutate()}
                disabled={start.isPending || !email || !password}
                className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50"
              >
                {start.isPending ? "Sending code..." : "Create Account"}
              </button>
              {start.error && (
                <p className="text-[var(--danger)] text-sm">{(start.error as any).message}</p>
              )}
              <p className="text-sm text-[var(--text-muted)] mt-4">
                Already have an account?{" "}
                <a href="/admin/login" className="text-[var(--accent)] hover:underline">
                  Sign in
                </a>
              </p>
            </div>
          </>
        )}

        {stage === "check-email" && (
          <div className="space-y-4">
            <h2 className="text-xl font-semibold text-[var(--text-primary)]">Check your email</h2>
            <p className="text-sm text-[var(--text-secondary)]">
              We sent a 6-digit verification code to <span className="text-[var(--text-primary)]">{email}</span>.
              Enter it below to finish creating your account.
            </p>
            <button
              onClick={() => setStage("verify")}
              className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition"
            >
              Enter code
            </button>
          </div>
        )}

        {stage === "verify" && (
          <div className="space-y-4">
            <h2 className="text-xl font-semibold text-[var(--text-primary)]">Enter verification code</h2>
            <p className="text-sm text-[var(--text-secondary)]">
              Sent to <span className="text-[var(--text-primary)]">{email}</span>. The code expires in 10 minutes.
            </p>
            <div>
              <label className={labelClass}>6-digit code</label>
              <input
                inputMode="numeric"
                maxLength={6}
                value={code}
                onChange={(e) => {
                  setCode(e.target.value.replace(/\D/g, "").slice(0, 6));
                  setCodeState("idle");
                }}
                className={`${inputClass} tracking-[0.4em] text-center text-lg ${
                  codeState !== "idle" ? "border-[var(--danger)]" : ""
                }`}
                placeholder="000000"
              />
              {codeState === "invalid" && (
                <p className="text-[var(--danger)] text-sm mt-1">Incorrect code. Please try again.</p>
              )}
              {codeState === "expired" && (
                <p className="text-[var(--danger)] text-sm mt-1">
                  This code has expired. Request a new one below.
                </p>
              )}
            </div>
            <button
              onClick={() => verify.mutate()}
              disabled={verify.isPending || code.length !== 6}
              className="w-full bg-[var(--accent)] text-white rounded py-2.5 text-sm hover:bg-[var(--accent-hover)] transition disabled:opacity-50"
            >
              {verify.isPending ? "Verifying..." : "Verify and continue"}
            </button>
            <button
              onClick={() => resend.mutate()}
              disabled={cooldown > 0 || resend.isPending}
              className="w-full border border-[var(--border)] text-[var(--text-secondary)] rounded py-2 text-sm hover:bg-[var(--bg-elevated)] transition disabled:opacity-50"
            >
              {cooldown > 0 ? `Resend code (${cooldown}s)` : resend.isPending ? "Sending..." : "Resend code"}
            </button>
            {resend.isError && (
              <p className="text-[var(--danger)] text-sm">{(resend.error as any).message}</p>
            )}
          </div>
        )}

        {stage === "success" && (
          <div className="space-y-4 text-center">
            <h2 className="text-xl font-semibold text-[var(--text-primary)]">Account verified</h2>
            <p className="text-sm text-[var(--text-secondary)]">Redirecting you to your organization...</p>
          </div>
        )}
      </div>
    </div>
  );
}
