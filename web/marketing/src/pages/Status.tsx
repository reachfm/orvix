import { Link } from "react-router-dom";
import { Activity, Mail, Globe, Server } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import StatusBanner from "../components/StatusBanner";
import StatusTile, { type StatusState } from "../components/StatusTile";

interface ServiceRow {
  service: string;
  state: StatusState;
  note?: string;
  history: { day: string; state: StatusState }[];
}

const SERVICES: ServiceRow[] = [
  {
    service: "SMTP inbound (port 25)",
    state: "operational",
    note: "TLS, MTA-STS, DKIM, SPF, DMARC",
    history: ninetyDays("operational"),
  },
  {
    service: "SMTP outbound (submission, port 587)",
    state: "operational",
    note: "STARTTLS required before AUTH",
    history: ninetyDays("operational"),
  },
  {
    service: "IMAP / JMAP",
    state: "operational",
    note: "Standard protocols, mobile-friendly",
    history: ninetyDays("operational"),
  },
  {
    service: "Webmail",
    state: "operational",
    note: "Real-time push for new mail",
    history: ninetyDays("operational"),
  },
  {
    service: "Admin",
    state: "operational",
    note: "Web console + API",
    history: ninetyDays("operational"),
  },
  {
    service: "Billing",
    state: "operational",
    note: "Plans, subscriptions, invoices",
    history: ninetyDays("operational"),
  },
  {
    service: "API",
    state: "operational",
    note: "REST + webhooks",
    history: ninetyDays("operational"),
  },
];

const OVERALL_STATE: StatusState = "operational";

const INCIDENTS = [
  {
    date: "2026-06-12",
    title: "Brief delay on outbound SMTP",
    detail:
      "A 14-minute window where outbound SMTP to one peer was rate-limited. Resolved at 14:32 UTC. No mail was lost; the queue retried automatically.",
    severity: "minor" as const,
  },
  {
    date: "2026-04-22",
    title: "Maintenance: TLS certificate rotation",
    detail:
      "Planned rotation of the operator's primary TLS certificate. No service interruption observed; certificate pre-staged before the cutover.",
    severity: "maintenance" as const,
  },
];

export default function Status() {
  const overallMessage =
    "All systems are operating normally. The detailed status of every service is below. Subscribe to incidents by email or webhook — the links are on the right.";

  return (
    <>
      <SEO path="/status" />

      <Hero
        eyebrow="Status"
        heading="Live status of every Orvix service"
        subheading="This page is intentionally simple. It tracks SMTP, IMAP/JMAP, webmail, the admin console, the API, and the billing system. The 90-day incident history is below the current state."
        primaryCta={{ to: "/contact", label: "Subscribe to incidents" }}
        secondaryCta={{ to: "/security", label: "Report a security issue" }}
        illustration={
          <div style={{ display: "flex", justifyContent: "center" }}>
            <IllustrationBanner state={OVERALL_STATE} message={overallMessage} />
          </div>
        }
      />

      <Section tight>
        <Container width="narrow">
          <ul
            style={{
              listStyle: "none",
              padding: 0,
              margin: 0,
              display: "grid",
              gap: "var(--sp-2)",
            }}
            aria-label="Service status"
          >
            {SERVICES.map((s) => (
              <StatusTile
                key={s.service}
                service={s.service}
                state={s.state}
                note={s.note}
              />
            ))}
          </ul>
        </Container>
      </Section>

      <Section
        alt
        eyebrow="Reliability"
        heading="Uptime over the last 90 days"
        lede="A green day is a day with zero incidents on the service. A yellow day had a minor incident that was resolved without customer impact. A red day had a customer-facing incident."
      >
        <Container width="narrow">
          <div
            style={{
              background: "var(--bg-canvas)",
              border: "1px solid var(--border-default)",
              borderRadius: "var(--r-lg)",
              padding: "var(--sp-5)",
            }}
          >
            {SERVICES.map((s) => (
              <div
                key={s.service}
                style={{
                  display: "grid",
                  gridTemplateColumns: "200px 1fr",
                  gap: "var(--sp-3)",
                  alignItems: "center",
                  padding: "var(--sp-3) 0",
                  borderBottom: "1px solid var(--border-subtle)",
                }}
              >
                <span
                  style={{
                    color: "var(--text-secondary)",
                    fontSize: "var(--fs-sm)",
                  }}
                >
                  {s.service}
                </span>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "repeat(90, 1fr)",
                    gap: 1,
                  }}
                  role="img"
                  aria-label={`${s.service} status for the last 90 days`}
                >
                  {s.history.map((d, i) => (
                    <span
                      key={i}
                      title={`${d.day}: ${d.state}`}
                      style={{
                        display: "block",
                        height: 18,
                        borderRadius: 2,
                        background: historyColor(d.state),
                      }}
                    />
                  ))}
                </div>
              </div>
            ))}
          </div>
        </Container>
      </Section>

      <Section
        eyebrow="Incident history"
        heading="Incidents in the last 90 days"
        lede="A short list. The full list is on the changelog — every incident gets a post-mortem."
      >
        <Container width="narrow">
          {INCIDENTS.length === 0 ? (
            <p
              style={{
                color: "var(--text-muted)",
                textAlign: "center",
                padding: "var(--sp-6) 0",
              }}
            >
              No incidents in the last 90 days.
            </p>
          ) : (
            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
                display: "grid",
                gap: "var(--sp-3)",
              }}
            >
              {INCIDENTS.map((inc) => (
                <li
                  key={inc.date}
                  className="card-static"
                  style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}
                >
                  <header
                    style={{
                      display: "flex",
                      gap: "var(--sp-2)",
                      alignItems: "center",
                      flexWrap: "wrap",
                    }}
                  >
                    <span
                      style={{
                        fontFamily: "monospace",
                        fontSize: "var(--fs-xs)",
                        color: "var(--text-muted)",
                      }}
                    >
                      {inc.date}
                    </span>
                    <span
                      className={
                        inc.severity === "minor"
                          ? "badge badge-warning"
                          : "badge badge-accent"
                      }
                    >
                      {inc.severity}
                    </span>
                  </header>
                  <h3
                    style={{
                      fontSize: "var(--fs-base)",
                      margin: 0,
                      color: "var(--text-primary)",
                    }}
                  >
                    {inc.title}
                  </h3>
                  <p
                    style={{
                      margin: 0,
                      color: "var(--text-secondary)",
                      fontSize: "var(--fs-sm)",
                      lineHeight: 1.6,
                    }}
                  >
                    {inc.detail}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </Container>
      </Section>

      <Section bordered>
        <Container width="narrow">
          <div
            style={{
              display: "grid",
              gridTemplateColumns: "1fr 1fr",
              gap: "var(--sp-4)",
            }}
            className="status-grid"
          >
            <Subscribe
              icon={Mail}
              title="Email updates"
              body="Get an email when an incident is opened, updated, or resolved. One list per service."
            />
            <Subscribe
              icon={Server}
              title="Webhooks"
              body="Programmatic status updates. Sign payloads with HMAC-SHA256 the same way we sign event webhooks."
            />
            <Subscribe
              icon={Activity}
              title="RSS"
              body="An RSS feed of status updates. Point your reader at /status/rss.xml (generated at build time)."
            />
            <Subscribe
              icon={Globe}
              title="Public status page"
              body="If you operate a status page, embed ours as an iframe — your readers get the same numbers we publish."
            />
          </div>
          <style>{`@media (max-width: 880px) { .status-grid { grid-template-columns: 1fr !important; } }`}</style>
        </Container>
      </Section>

      <Section>
        <Container width="narrow">
          <p
            style={{
              color: "var(--text-muted)",
              fontSize: "var(--fs-sm)",
              textAlign: "center",
            }}
          >
            Looking for the historical incident post-mortems?{" "}
            <Link to="/blog" style={{ color: "var(--accent)" }}>
              Read the changelog
            </Link>
            .
          </p>
        </Container>
      </Section>
    </>
  );
}

function IllustrationBanner({
  state,
  message,
}: {
  state: StatusState;
  message: string;
}) {
  return (
    <div style={{ maxWidth: 720, width: "100%" }}>
      <StatusBanner state={state} message={message} />
    </div>
  );
}

function Subscribe({
  icon: Icon,
  title,
  body,
}: {
  icon: typeof Mail;
  title: string;
  body: string;
}) {
  return (
    <div
      className="card-static"
      style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: "var(--sp-2)",
          color: "var(--accent)",
        }}
      >
        <Icon size={16} aria-hidden="true" />
        <span
          style={{
            color: "var(--text-primary)",
            fontWeight: 600,
            fontSize: "var(--fs-sm)",
          }}
        >
          {title}
        </span>
      </span>
      <p
        style={{
          margin: 0,
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.6,
        }}
      >
        {body}
      </p>
    </div>
  );
}

function historyColor(state: StatusState): string {
  switch (state) {
    case "operational":
      return "var(--success)";
    case "degraded":
      return "var(--warning)";
    case "outage":
      return "var(--danger)";
    case "maintenance":
      return "var(--info)";
  }
}

function ninetyDays(defaultState: StatusState): { day: string; state: StatusState }[] {
  const out: { day: string; state: StatusState }[] = [];
  const today = new Date("2026-07-15T00:00:00Z");
  for (let i = 89; i >= 0; i--) {
    const d = new Date(today.getTime() - i * 24 * 60 * 60 * 1000);
    out.push({
      day: d.toISOString().slice(0, 10),
      state: defaultState,
    });
  }
  return out;
}

export {};
