import { Check, X } from "lucide-react";
import { PLANS, ALL_FEATURE_KEYS } from "../lib/plans";

const FEATURE_LABELS: Record<string, string> = {
  custom_domain: "Custom domain",
  dkim: "DKIM signing",
  mta_sts: "MTA-STS policy",
  api: "REST API",
  team: "Team seats",
  groups: "Distribution groups",
  catch_all: "Catch-all addresses",
  mail_forwarding: "Mail forwarding",
  backup: "Encrypted backups",
  audit_log: "Audit log",
  mfa: "Multi-factor auth (TOTP)",
  sso: "SAML / OIDC SSO",
  sla: "99.99% uptime SLA",
  priority_support: "Priority support",
};

export default function ComparisonTable() {
  return (
    <div
      tabIndex={0}
      aria-label="Plan comparison table"
      style={{
        overflowX: "auto",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-lg)",
      }}
    >
      <table
        style={{
          width: "100%",
          borderCollapse: "collapse",
          minWidth: 720,
          fontSize: "var(--fs-sm)",
        }}
      >
        <caption
          style={{
            position: "absolute",
            width: 1,
            height: 1,
            padding: 0,
            margin: -1,
            overflow: "hidden",
            clip: "rect(0,0,0,0)",
            whiteSpace: "nowrap",
            border: 0,
          }}
        >
          Plan comparison
        </caption>
        <thead>
          <tr>
            <th
              scope="col"
              style={{
                textAlign: "left",
                padding: "var(--sp-4)",
                borderBottom: "1px solid var(--border-default)",
                fontWeight: 600,
                color: "var(--text-secondary)",
                background: "var(--bg-canvas)",
                position: "sticky",
                left: 0,
                zIndex: 1,
              }}
            >
              Feature
            </th>
            {PLANS.map((p) => (
              <th
                key={p.id}
                scope="col"
                style={{
                  textAlign: "center",
                  padding: "var(--sp-4)",
                  borderBottom: "1px solid var(--border-default)",
                  fontWeight: 600,
                  color: p.isFeatured ? "var(--accent)" : "var(--text-primary)",
                  background: "var(--bg-canvas)",
                }}
              >
                {p.name}
                {p.isFeatured && (
                  <span
                    className="badge badge-accent"
                    style={{ marginLeft: "var(--sp-2)" }}
                  >
                    Popular
                  </span>
                )}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {ALL_FEATURE_KEYS.map((key) => (
            <tr key={key}>
              <th
                scope="row"
                style={{
                  textAlign: "left",
                  padding: "var(--sp-3) var(--sp-4)",
                  borderBottom: "1px solid var(--border-subtle)",
                  fontWeight: 500,
                  color: "var(--text-primary)",
                  background: "var(--bg-canvas)",
                  position: "sticky",
                  left: 0,
                }}
              >
                {FEATURE_LABELS[key] ?? key}
              </th>
              {PLANS.map((p) => {
                const has = p.features.some((f) => f.key === key);
                return (
                  <td
                    key={p.id}
                    style={{
                      textAlign: "center",
                      padding: "var(--sp-3) var(--sp-4)",
                      borderBottom: "1px solid var(--border-subtle)",
                      color: has ? "var(--success)" : "var(--text-faint)",
                    }}
                    aria-label={has ? "Included" : "Not included"}
                  >
                    {has ? (
                      <Check
                        size={18}
                        aria-hidden="true"
                        style={{ display: "inline-block" }}
                      />
                    ) : (
                      <X
                        size={18}
                        aria-hidden="true"
                        style={{ display: "inline-block" }}
                      />
                    )}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
