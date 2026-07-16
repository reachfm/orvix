import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

const STORAGE_KEY = "orvix.cookie.choice.v1";

type Choice = "accepted" | "rejected" | "unset";

function readChoice(): Choice {
  if (typeof window === "undefined") {
    return "unset";
  }
  try {
    return (window.localStorage.getItem(STORAGE_KEY) as Choice) || "unset";
  } catch {
    return "unset";
  }
}

function writeChoice(c: Choice) {
  try {
    window.localStorage.setItem(STORAGE_KEY, c);
  } catch {
    // Storage might be blocked (private mode, iframe). Fall back to a
    // session-only cookie so the banner doesn't show again this session.
    document.cookie = `${STORAGE_KEY}=${c}; path=/; SameSite=Lax`;
  }
}

export default function CookieBanner() {
  const [choice, setChoice] = useState<Choice>("unset");
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
    setChoice(readChoice());
  }, []);

  if (!mounted || choice !== "unset") {
    return null;
  }

  return (
    <div
      role="region"
      aria-label="Cookie consent"
      style={{
        position: "fixed",
        insetInline: 0,
        bottom: 0,
        zIndex: 60,
        padding: "var(--sp-3) var(--sp-4)",
        background: "var(--bg-elevated)",
        borderTop: "1px solid var(--border-default)",
        boxShadow: "0 -8px 24px rgba(0,0,0,0.35)",
      }}
    >
      <div
        style={{
          maxWidth: "var(--container-max)",
          margin: "0 auto",
          display: "flex",
          flexWrap: "wrap",
          alignItems: "center",
          gap: "var(--sp-3)",
          justifyContent: "space-between",
        }}
      >
        <p
          style={{
            margin: 0,
            color: "var(--text-secondary)",
            fontSize: "var(--fs-sm)",
            maxWidth: "60ch",
          }}
        >
          We use a small set of strictly necessary and functional cookies to run
          this site. No analytics SDK is included in this release. See our{" "}
          <Link to="/legal/cookies" style={{ color: "var(--accent)" }}>
            cookie policy
          </Link>
          .
        </p>
        <div style={{ display: "flex", gap: "var(--sp-2)" }}>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => {
              writeChoice("rejected");
              setChoice("rejected");
            }}
          >
            Reject non-essential
          </button>
          <button
            type="button"
            className="btn btn-primary btn-sm"
            onClick={() => {
              writeChoice("accepted");
              setChoice("accepted");
            }}
          >
            Accept all
          </button>
        </div>
      </div>
    </div>
  );
}
