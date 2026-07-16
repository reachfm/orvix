import { useState } from "react";
import { PLANS, formatPrice } from "../lib/plans";

/**
 * Monthly / yearly toggle. The spec defines annual prices as
 * 10x monthly (a 16% discount). Both columns are real numbers
 * from the spec, so the toggle is honest — there is no fake
 * "Save 50%!" headline.
 */
export default function PricingToggle() {
  const [cycle, setCycle] = useState<"monthly" | "yearly">("monthly");

  return (
    <div>
      <div
        role="radiogroup"
        aria-label="Billing cycle"
        style={{
          display: "inline-flex",
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-default)",
          borderRadius: 999,
          padding: 4,
          margin: "0 auto var(--sp-6)",
        }}
      >
        <button
          type="button"
          role="radio"
          aria-checked={cycle === "monthly"}
          onClick={() => setCycle("monthly")}
          style={{
            border: "none",
            background: cycle === "monthly" ? "var(--accent)" : "transparent",
            color: cycle === "monthly" ? "#0C0E12" : "var(--text-secondary)",
            fontWeight: 600,
            fontSize: "var(--fs-sm)",
            padding: "var(--sp-2) var(--sp-4)",
            borderRadius: 999,
            cursor: "pointer",
            transition: "background var(--dur-2) var(--ease-out)",
          }}
        >
          Monthly
        </button>
        <button
          type="button"
          role="radio"
          aria-checked={cycle === "yearly"}
          onClick={() => setCycle("yearly")}
          style={{
            border: "none",
            background: cycle === "yearly" ? "var(--accent)" : "transparent",
            color: cycle === "yearly" ? "#0C0E12" : "var(--text-secondary)",
            fontWeight: 600,
            fontSize: "var(--fs-sm)",
            padding: "var(--sp-2) var(--sp-4)",
            borderRadius: 999,
            cursor: "pointer",
            transition: "background var(--dur-2) var(--ease-out)",
          }}
        >
          Yearly
        </button>
      </div>

      <div
        style={{
          display: "grid",
          gap: "var(--sp-3)",
          gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))",
        }}
      >
        {PLANS.map((p) => (
          <div
            key={p.id}
            className="card-static"
            style={{
              textAlign: "center",
              padding: "var(--sp-5)",
            }}
          >
            <p
              style={{
                fontSize: "var(--fs-sm)",
                color: "var(--text-muted)",
                marginBottom: "var(--sp-1)",
                fontWeight: 600,
              }}
            >
              {p.name}
            </p>
            <p
              style={{
                fontSize: "var(--fs-2xl)",
                fontWeight: 700,
                color: "var(--text-primary)",
                margin: 0,
                letterSpacing: "-0.01em",
              }}
            >
              {formatPrice(
                cycle === "yearly" ? p.usdYearly : p.usdMonthly,
                cycle,
              )}
            </p>
            {cycle === "yearly" && p.usdMonthly > 0 && (
              <p
                style={{
                  fontSize: "var(--fs-xs)",
                  color: "var(--text-faint)",
                  marginTop: "var(--sp-1)",
                }}
              >
                Annual billing — 10× monthly per the spec.
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
