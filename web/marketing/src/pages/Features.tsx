import {
  Inbox,
  Search,
  Calendar as CalendarIcon,
  Users,
  Lock,
  KeyRound,
  Globe,
  ShieldCheck,
  Server,
  Code2,
  Layers,
  AtSign,
  Building2,
  Webhook,
  FileText,
  Activity,
  Hash,
  PenLine,
  Clock,
  Filter,
} from "lucide-react";
import SEO from "../components/SEO";
import Section from "../components/Section";
import Hero from "../components/Hero";
import Container from "../components/Container";
import Illustration from "../components/Illustration";
import { FEATURE_ROWS } from "../data/features-matrix";

const GROUPS = [
  {
    name: "Webmail",
    description: "The interface for the people who send and receive.",
    icon: Inbox,
    capabilities: [
      { icon: Inbox, title: "Folders and labels", body: "Standard inbox, sent, drafts, spam, trash, archive — plus labels for organization." },
      { icon: Search, title: "Full-text search", body: "Search every message you have, by sender, subject, body, or attachment filename." },
      { icon: CalendarIcon, title: "Calendar", body: "Day, week, month, and agenda views. Share calendars with your team." },
      { icon: PenLine, title: "Compose", body: "Rich-text compose with attachments, mentions, and undo send." },
      { icon: AtSign, title: "Contacts", body: "Auto-complete from your sent and received mail. Sync with CardDAV." },
      { icon: Clock, title: "Snooze and send later", body: "Schedule a message to send at a specific time, or snooze it to your inbox." },
    ],
  },
  {
    name: "Admin",
    description: "The control panel for the people who run the org.",
    icon: Server,
    capabilities: [
      { icon: Building2, title: "Multi-tenant", body: "Run multiple organizations from one operator account. Each org is fully isolated." },
      { icon: Globe, title: "Domains and DNS", body: "Verify ownership, then publish the exact MX, SPF, DKIM, and DMARC records we give you." },
      { icon: Users, title: "Members and roles", body: "Owner, admin, operator, and member. Per-role permissions documented in the API." },
      { icon: AtSign, title: "Aliases and groups", body: "Forward addresses, catch-all, and distribution lists — all from the admin UI." },
      { icon: Activity, title: "Audit log", body: "Every administrative action recorded with actor, target, timestamp, and IP." },
      { icon: FileText, title: "Usage and quotas", body: "Storage and sends per mailbox, per domain, per organization." },
    ],
  },
  {
    name: "Security",
    description: "The defaults that protect every account.",
    icon: ShieldCheck,
    capabilities: [
      { icon: Lock, title: "Encrypted in transit", body: "TLS 1.2 minimum on every connection. Outbound uses opportunistic TLS by default." },
      { icon: ShieldCheck, title: "DKIM, SPF, DMARC", body: "DKIM signing on every domain by default. SPF and DMARC are validated on every incoming message." },
      { icon: KeyRound, title: "Multi-factor auth", body: "TOTP for every account. Optional enforcement at the org level." },
      { icon: Hash, title: "Encrypted at rest", body: "AES-256-GCM for mailbox storage and backups. Keys rotated on a documented schedule." },
      { icon: Filter, title: "Spam and abuse filtering", body: "Reputation-based scoring plus per-org rules. Quarantine for reviewable messages." },
      { icon: Activity, title: "Login protection", body: "Rate-limited login attempts. Suspicious logins trigger MFA challenge." },
    ],
  },
  {
    name: "API",
    description: "Everything you can do in the admin UI, in JSON.",
    icon: Code2,
    capabilities: [
      { icon: Code2, title: "REST + bearer tokens", body: "JSON over HTTPS. Authenticate with a per-org API key." },
      { icon: Webhook, title: "Webhooks", body: "Get notified when a message arrives, a mailbox is created, or a domain is verified." },
      { icon: FileText, title: "OpenAPI 3.0", body: "A spec you can generate clients from. The docs page links to it." },
      { icon: Layers, title: "Idempotent writes", body: "POST endpoints accept an Idempotency-Key header so retries are safe." },
      { icon: Globe, title: "Open data formats", body: "Export mailboxes as mbox or EML. Import via the bulk-mailbox endpoint." },
      { icon: Server, title: "Rate limits documented", body: "Per-key rate limits documented in the API reference. 429 responses include Retry-After." },
    ],
  },
];

export default function Features() {
  return (
    <>
      <SEO path="/features" />

      <Hero
        eyebrow="Features"
        heading="Everything in the box, in one place"
        subheading="Orvix is one product. This page is the long-form description of what that product does. No marketing fluff — just capabilities grouped by surface."
        primaryCta={{ to: "/pricing", label: "See pricing" }}
        secondaryCta={{ to: "/docs", label: "Read the docs" }}
      />

      {GROUPS.map((group, idx) => (
        <Section
          key={group.name}
          alt={idx % 2 === 1}
          eyebrow={group.name}
          heading={group.description}
        >
          <div
            className="cap-grid"
            style={{
              display: "grid",
              gridTemplateColumns: "1.2fr 1fr",
              gap: "var(--sp-7)",
              alignItems: "center",
            }}
          >
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "1fr 1fr",
                gap: "var(--sp-3)",
              }}
            >
              {group.capabilities.map((cap) => {
                const Cap = cap.icon;
                return (
                  <div
                    key={cap.title}
                    className="card-static"
                    style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}
                  >
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        justifyContent: "center",
                        width: 32,
                        height: 32,
                        borderRadius: "var(--r-md)",
                        background: "var(--accent-glow)",
                        color: "var(--accent)",
                      }}
                      aria-hidden="true"
                    >
                      <Cap size={16} />
                    </span>
                    <h3
                      style={{
                        fontSize: "var(--fs-base)",
                        margin: 0,
                        color: "var(--text-primary)",
                      }}
                    >
                      {cap.title}
                    </h3>
                    <p
                      style={{
                        margin: 0,
                        color: "var(--text-secondary)",
                        fontSize: "var(--fs-sm)",
                        lineHeight: 1.6,
                      }}
                    >
                      {cap.body}
                    </p>
                  </div>
                );
              })}
            </div>
            <Illustration
              variant={
                idx === 0
                  ? "inbox"
                  : idx === 1
                    ? "admin-dashboard"
                    : idx === 2
                      ? "admin-domains"
                      : "api-explorer"
              }
              height={420}
            />
          </div>
          <style>{`@media (max-width: 880px) { .cap-grid { grid-template-columns: 1fr !important; } }`}</style>
        </Section>
      ))}

      <Section
        eyebrow="Capability matrix"
        heading="What each plan includes"
        lede="A long table, broken out by capability group. Use it to plan a migration or to figure out which plan you actually need."
      >
        <Container>
          {Array.from(new Set(FEATURE_ROWS.map((r) => r.group))).map((group) => (
            <div key={group} style={{ marginBottom: "var(--sp-6)" }}>
              <h3
                style={{
                  fontSize: "var(--fs-lg)",
                  marginBottom: "var(--sp-3)",
                  color: "var(--text-primary)",
                }}
              >
                {group}
              </h3>
              <div
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
                    fontSize: "var(--fs-sm)",
                    minWidth: 720,
                  }}
                >
                  <thead>
                    <tr>
                      <th
                        scope="col"
                        style={{
                          textAlign: "left",
                          padding: "var(--sp-3) var(--sp-4)",
                          borderBottom: "1px solid var(--border-default)",
                          color: "var(--text-secondary)",
                          fontWeight: 600,
                          width: "44%",
                        }}
                      >
                        Capability
                      </th>
                      {["Free", "Starter", "Business", "Enterprise"].map(
                        (label) => (
                          <th
                            key={label}
                            scope="col"
                            style={{
                              textAlign: "center",
                              padding: "var(--sp-3) var(--sp-4)",
                              borderBottom: "1px solid var(--border-default)",
                              color: "var(--text-secondary)",
                              fontWeight: 600,
                            }}
                          >
                            {label}
                          </th>
                        ),
                      )}
                    </tr>
                  </thead>
                  <tbody>
                    {FEATURE_ROWS.filter((r) => r.group === group).map((row) => {
                      const included = (p: string) => row.planIds.includes(p as never);
                      return (
                        <tr key={row.capability}>
                          <th
                            scope="row"
                            style={{
                              textAlign: "left",
                              padding: "var(--sp-3) var(--sp-4)",
                              borderBottom: "1px solid var(--border-subtle)",
                              color: "var(--text-primary)",
                              fontWeight: 500,
                            }}
                          >
                            <div>{row.capability}</div>
                            <div
                              style={{
                                fontSize: "var(--fs-xs)",
                                color: "var(--text-faint)",
                                fontWeight: 400,
                                marginTop: 4,
                              }}
                            >
                              {row.body}
                            </div>
                          </th>
                          {["free", "starter", "business", "enterprise"].map(
                            (p) => (
                              <td
                                key={p}
                                style={{
                                  textAlign: "center",
                                  padding: "var(--sp-3) var(--sp-4)",
                                  borderBottom: "1px solid var(--border-subtle)",
                                  color: included(p)
                                    ? "var(--success)"
                                    : "var(--text-faint)",
                                }}
                                aria-label={included(p) ? "Included" : "Not included"}
                              >
                                {included(p) ? "✓" : "—"}
                              </td>
                            ),
                          )}
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>
          ))}
        </Container>
      </Section>
    </>
  );
}
