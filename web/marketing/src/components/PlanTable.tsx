import { Link } from "react-router-dom";
import { Check } from "lucide-react";
import { PLANS, formatPrice, formatStorage } from "../lib/plans";
import { PORTAL_SIGNUP } from "../lib/links";

export default function PlanTable() {
  return (
    <div
      style={{
        display: "grid",
        gap: "var(--sp-4)",
        gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))",
      }}
    >
      {PLANS.map((plan) => {
        const featured = plan.isFeatured;
        return (
          <article
            key={plan.id}
            className={featured ? "card card-featured" : "card"}
            aria-label={`${plan.name} plan`}
            style={{
              display: "flex",
              flexDirection: "column",
              gap: "var(--sp-4)",
              position: "relative",
            }}
          >
            {featured && (
              <span
                className="badge badge-accent"
                style={{
                  position: "absolute",
                  top: "calc(var(--sp-4) * -1)",
                  left: "var(--sp-4)",
                }}
              >
                Most popular
              </span>
            )}

            <header>
              <h3
                style={{
                  fontSize: "var(--fs-xl)",
                  margin: 0,
                  color: "var(--text-primary)",
                }}
              >
                {plan.name}
              </h3>
              <p
                style={{
                  marginTop: "var(--sp-1)",
                  fontSize: "var(--fs-sm)",
                  color: "var(--text-muted)",
                }}
              >
                {plan.id === "free"
                  ? "For trying Orvix out."
                  : plan.id === "starter"
                    ? "For small teams with a single domain."
                    : plan.id === "business"
                      ? "For growing teams that need more control."
                      : "For organizations that need SSO, SLA, and scale."}
              </p>
            </header>

            <div>
              <div
                style={{
                  display: "flex",
                  alignItems: "baseline",
                  gap: "var(--sp-1)",
                }}
              >
                <span
                  style={{
                    fontSize: "var(--fs-3xl)",
                    fontWeight: 700,
                    color: "var(--text-primary)",
                    letterSpacing: "-0.02em",
                  }}
                >
                  {formatPrice(plan.usdMonthly, "monthly")}
                </span>
              </div>
              <p
                style={{
                  marginTop: "var(--sp-1)",
                  fontSize: "var(--fs-xs)",
                  color: "var(--text-faint)",
                }}
              >
                {plan.usdMonthly === 0
                  ? "No credit card required."
                  : `Billed monthly. Annual billing is ${formatPrice(plan.usdYearly, "yearly")} (a 16% discount, per the spec).`}
              </p>
            </div>

            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
                display: "flex",
                flexDirection: "column",
                gap: "var(--sp-2)",
                fontSize: "var(--fs-sm)",
                color: "var(--text-secondary)",
              }}
            >
              <Limit label="Domains" value={String(plan.domains)} />
              <Limit label="Mailboxes" value={String(plan.mailboxes)} />
              <Limit label="Storage" value={formatStorage(plan.storageBytes)} />
              <Limit
                label="Sends per day"
                value={
                  plan.sendsPerDay >= 1000
                    ? `${(plan.sendsPerDay / 1000).toFixed(0).replace(/\.0$/, "")}k`
                    : String(plan.sendsPerDay)
                }
              />
            </ul>

            <div
              style={{
                borderTop: "1px solid var(--border-subtle)",
                paddingTop: "var(--sp-4)",
              }}
            >
              <p
                style={{
                  fontSize: "var(--fs-xs)",
                  textTransform: "uppercase",
                  letterSpacing: "0.1em",
                  color: "var(--text-faint)",
                  marginBottom: "var(--sp-2)",
                  fontWeight: 700,
                }}
              >
                Includes
              </p>
              <ul
                style={{
                  listStyle: "none",
                  padding: 0,
                  margin: 0,
                  display: "grid",
                  gap: "var(--sp-2)",
                }}
              >
                {plan.features.map((f) => (
                  <li
                    key={f.key}
                    style={{
                      display: "flex",
                      alignItems: "flex-start",
                      gap: "var(--sp-2)",
                      fontSize: "var(--fs-sm)",
                      color: "var(--text-secondary)",
                    }}
                  >
                    <Check
                      size={16}
                      style={{
                        flexShrink: 0,
                        marginTop: 2,
                        color: "var(--accent)",
                      }}
                      aria-hidden="true"
                    />
                    <span>{f.label}</span>
                  </li>
                ))}
              </ul>
            </div>

            <div style={{ marginTop: "auto" }}>
              {plan.id === "free" ? (
                <a href={PORTAL_SIGNUP} className="btn btn-secondary" style={{ width: "100%" }} aria-label="Start a free Orvix account">
                  Start free
                </a>
              ) : (
                <Link to="/contact" className={featured ? "btn btn-primary" : "btn btn-secondary"} style={{ width: "100%" }} aria-label={`Contact sales about Orvix ${plan.name}`}>
                  Contact sales
                </Link>
              )}
            </div>
          </article>
        );
      })}
    </div>
  );
}

function Limit({ label, value }: { label: string; value: string }) {
  return (
    <li
      style={{
        display: "flex",
        justifyContent: "space-between",
        alignItems: "center",
        gap: "var(--sp-3)",
      }}
    >
      <span style={{ color: "var(--text-muted)" }}>{label}</span>
      <strong style={{ color: "var(--text-primary)", fontWeight: 600 }}>
        {value}
      </strong>
    </li>
  );
}
