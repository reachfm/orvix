import { Link } from "react-router-dom";
import {
  Lock,
  ShieldCheck,
  KeyRound,
  Hash,
  Server,
  AlertTriangle,
  Mail,
  Eye,
} from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import ComplianceTable from "../components/ComplianceTable";
import Illustration from "../components/Illustration";

const COMPLIANCE_ROWS = [
  {
    topic: "Encryption in transit",
    detail:
      "TLS 1.2 minimum on every connection. Inbound SMTP uses opportunistic TLS by default; outbound SMTP uses opportunistic TLS by default and is configurable to strict mode. IMAPS and SMTPS require TLS.",
  },
  {
    topic: "Encryption at rest",
    detail:
      "AES-256-GCM for mailbox storage and backups. Keys are managed by the operator and rotated on a documented schedule. The data and privacy page documents the rotation cadence.",
  },
  {
    topic: "Authentication (incoming)",
    detail:
      "SPF, DKIM, and DMARC are validated on every incoming message. Messages that fail all three are quarantined by default. Operators can override the policy per domain.",
  },
  {
    topic: "Authentication (outgoing)",
    detail:
      "Every message Orvix sends is DKIM-signed. SPF is published as a DNS record for the operator's sending IPs. DMARC is published with a quarantine policy.",
  },
  {
    topic: "MTA-STS and TLS reporting",
    detail:
      "MTA-STS is available on Starter, Business, and Enterprise. TLSRPT is published for every domain that has MTA-STS enabled, so the operator can see which senders delivered over TLS.",
  },
  {
    topic: "Account security",
    detail:
      "TOTP-based MFA on every account. Optional enforcement at the organization level. Login attempts are rate-limited, suspicious logins trigger an MFA challenge, and every successful login is recorded in the audit log.",
  },
  {
    topic: "Data residency",
    detail:
      "Mail content and metadata are stored in the region the operator selected at signup. Operators on Enterprise can deploy a dedicated instance in a region of their choice.",
  },
  {
    topic: "Backups",
    detail:
      "Encrypted, off-host backups of every mailbox with a configurable retention. Backups are AES-256-GCM sealed with a per-operator key. The data and privacy page documents the default retention.",
  },
  {
    topic: "Subprocessors",
    detail:
      "We list every subprocessor on the data and privacy page, including the purpose and the data shared. Customers are notified at least 30 days before a new subprocessor is added.",
  },
  {
    topic: "Disclosure",
    detail:
      "Coordinated disclosure: email security@orvix.com. We acknowledge within one business day and aim to triage within five. The security page lists our PGP key and disclosure scope.",
  },
];

const DISCLOSURE_STEPS = [
  {
    title: "Report",
    body: "Email security@orvix.com with a description, reproduction steps, and the impact you observed.",
  },
  {
    title: "Acknowledge",
    body: "We acknowledge within one business day. We may ask for a PGP-encrypted reply if the report is sensitive.",
  },
  {
    title: "Triage",
    body: "We triage within five business days and assign a severity. We share the severity and our planned timeline with you.",
  },
  {
    title: "Fix",
    body: "We fix, we test, and we deploy. Critical fixes ship within 24 hours of confirmation.",
  },
  {
    title: "Disclose",
    body: "After the fix is in production, we publish a security advisory on the changelog with credit to the reporter (unless they ask to remain anonymous).",
  },
];

export default function Security() {
  return (
    <>
      <SEO path="/security" />

      <Hero
        eyebrow="Security"
        heading="Email is critical infrastructure. We treat it that way."
        subheading="TLS 1.2 minimum, DKIM on every domain, MTA-STS on paid plans, encrypted backups, MFA, and an audit log that records every administrative change."
        primaryCta={{ to: "/contact", label: "Talk to our security team" }}
        secondaryCta={{ to: "/legal/data-and-privacy", label: "Read the data and privacy page" }}
        illustration={<Illustration variant="admin-domains" height={360} />}
      />

      <Section
        alt
        eyebrow="How Orvix secures your mail"
        heading="The defaults, in plain English"
        lede="These are the controls that apply to every Orvix account, on every plan. They are not optional add-ons."
      >
        <div
          className="three-col"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, 1fr)",
            gap: "var(--sp-4)",
          }}
        >
          <Card icon={Lock} title="Encrypted in transit">
            TLS 1.2 minimum. Outbound mail is delivered over TLS when the
            remote server advertises STARTTLS.
          </Card>
          <Card icon={ShieldCheck} title="DKIM on every domain">
            Every domain on Orvix is DKIM-signed out of the box. The DNS page
            gives you the exact record to publish.
          </Card>
          <Card icon={KeyRound} title="MFA available everywhere">
            TOTP-based multi-factor authentication is available on every
            account. Orgs on Business and above can enforce it.
          </Card>
          <Card icon={Hash} title="Encrypted at rest">
            Mailbox storage and backups are AES-256-GCM. Keys are managed by
            the operator and rotated on a documented schedule.
          </Card>
          <Card icon={Server} title="MTA-STS on paid plans">
            Starter, Business, and Enterprise publish an MTA-STS policy so
            other servers only deliver to you over verified TLS.
          </Card>
          <Card icon={Eye} title="Full audit log">
            Every administrative action is recorded with actor, target,
            timestamp, and source IP. Exportable as CSV.
          </Card>
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        eyebrow="The full table"
        heading="What we do, and how we do it"
        lede="The same table we share with security teams during procurement. If something here is unclear, email security@orvix.com and we will clarify."
      >
        <ComplianceTable
          caption="Orvix security controls by topic. Last updated alongside this release."
          rows={COMPLIANCE_ROWS}
        />
      </Section>

      <Section
        alt
        eyebrow="Vulnerability disclosure"
        heading="How to report a vulnerability"
        lede="Coordinated disclosure. We will acknowledge your report within one business day and triage within five."
      >
        <div
          className="two-col"
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1.1fr",
            gap: "var(--sp-7)",
            alignItems: "flex-start",
          }}
        >
          <div>
            <h3
              style={{
                fontSize: "var(--fs-lg)",
                color: "var(--text-primary)",
                marginBottom: "var(--sp-3)",
              }}
            >
              What to report
            </h3>
            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
                display: "grid",
                gap: "var(--sp-2)",
                color: "var(--text-secondary)",
                fontSize: "var(--fs-sm)",
              }}
            >
              <BulletPoint>
                Authentication or authorization bypass on any Orvix surface
                (webmail, admin, API).
              </BulletPoint>
              <BulletPoint>
                Cross-tenant data access — any way to read or modify another
                customer&apos;s mail.
              </BulletPoint>
              <BulletPoint>
                Injection (SQLi, command injection, template injection) on any
                Orvix endpoint.
              </BulletPoint>
              <BulletPoint>
                Cryptographic weakness in TLS configuration, DKIM, or stored
                data.
              </BulletPoint>
              <BulletPoint>
                A way to bypass DKIM, SPF, or DMARC enforcement on incoming
                mail.
              </BulletPoint>
              <BulletPoint>
                Any other issue that materially affects the confidentiality,
                integrity, or availability of Orvix or its customers.
              </BulletPoint>
            </ul>
            <h3
              style={{
                fontSize: "var(--fs-lg)",
                color: "var(--text-primary)",
                margin: "var(--sp-6) 0 var(--sp-3)",
              }}
            >
              What we ask
            </h3>
            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
                display: "grid",
                gap: "var(--sp-2)",
                color: "var(--text-secondary)",
                fontSize: "var(--fs-sm)",
              }}
            >
              <BulletPoint>
                Give us a reasonable window to fix the issue before public
                disclosure — typically 90 days from confirmation.
              </BulletPoint>
              <BulletPoint>
                Do not access other customers&apos; data, even if you find a way
                to. Stop and tell us.
              </BulletPoint>
              <BulletPoint>
                Do not perform denial-of-service testing against shared
                infrastructure. Set up a pilot org instead.
              </BulletPoint>
            </ul>
          </div>
          <div>
            <ol
              style={{
                listStyle: "none",
                padding: 0,
                margin: 0,
                display: "grid",
                gap: "var(--sp-3)",
                counterReset: "step",
              }}
            >
              {DISCLOSURE_STEPS.map((step) => (
                <li
                  key={step.title}
                  style={{
                    display: "flex",
                    gap: "var(--sp-3)",
                    alignItems: "flex-start",
                    background: "var(--bg-canvas)",
                    border: "1px solid var(--border-default)",
                    borderRadius: "var(--r-md)",
                    padding: "var(--sp-4) var(--sp-5)",
                  }}
                >
                  <span
                    style={{
                      display: "inline-flex",
                      alignItems: "center",
                      justifyContent: "center",
                      width: 28,
                      height: 28,
                      borderRadius: 999,
                      background: "var(--accent-glow)",
                      color: "var(--accent)",
                      fontWeight: 700,
                      fontSize: "var(--fs-sm)",
                      flexShrink: 0,
                    }}
                  >
                    {DISCLOSURE_STEPS.indexOf(step) + 1}
                  </span>
                  <div>
                    <h4
                      style={{
                        margin: 0,
                        color: "var(--text-primary)",
                        fontSize: "var(--fs-base)",
                      }}
                    >
                      {step.title}
                    </h4>
                    <p
                      style={{
                        margin: "var(--sp-1) 0 0",
                        color: "var(--text-secondary)",
                        fontSize: "var(--fs-sm)",
                        lineHeight: 1.6,
                      }}
                    >
                      {step.body}
                    </p>
                  </div>
                </li>
              ))}
            </ol>

            <div
              style={{
                marginTop: "var(--sp-5)",
                padding: "var(--sp-4) var(--sp-5)",
                border: "1px solid var(--accent)",
                borderRadius: "var(--r-md)",
                background: "var(--accent-glow)",
              }}
            >
              <h3
                style={{
                  fontSize: "var(--fs-base)",
                  margin: 0,
                  color: "var(--text-primary)",
                  display: "flex",
                  alignItems: "center",
                  gap: "var(--sp-2)",
                }}
              >
                <Mail size={16} aria-hidden="true" />
                Email us
              </h3>
              <p
                style={{
                  margin: "var(--sp-2) 0 0",
                  color: "var(--text-secondary)",
                  fontSize: "var(--fs-sm)",
                  lineHeight: 1.6,
                }}
              >
                <a
                  href="mailto:security@orvix.com"
                  style={{ color: "var(--accent)", fontWeight: 600 }}
                >
                  security@orvix.com
                </a>
                . PGP key fingerprint:{" "}
                <code style={{ color: "var(--text-primary)" }}>
                  4F3A 8C2E 7B91 D5F6 1A0B
                </code>
                .
              </p>
            </div>
          </div>
        </div>
        <style>{`@media (max-width: 880px) { .two-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section bordered>
        <Container width="narrow">
          <div
            style={{
              display: "flex",
              gap: "var(--sp-3)",
              padding: "var(--sp-4) var(--sp-5)",
              background: "var(--bg-canvas)",
              border: "1px solid var(--border-default)",
              borderRadius: "var(--r-md)",
              color: "var(--text-secondary)",
              fontSize: "var(--fs-sm)",
              alignItems: "flex-start",
            }}
          >
            <AlertTriangle
              size={20}
              aria-hidden="true"
              style={{ color: "var(--warning)", flexShrink: 0 }}
            />
            <div>
              <p style={{ margin: 0 }}>
                If you have found a vulnerability and you need to reach us
                outside business hours, email{" "}
                <a
                  href="mailto:security@orvix.com"
                  style={{ color: "var(--accent)" }}
                >
                  security@orvix.com
                </a>{" "}
                with <code style={{ color: "var(--text-primary)" }}>URGENT</code>{" "}
                in the subject line. Critical issues are paged 24/7.
              </p>
              <p style={{ margin: "var(--sp-2) 0 0" }}>
                For non-security support questions, see the{" "}
                <Link to="/contact" style={{ color: "var(--accent)" }}>
                  contact page
                </Link>
                .
              </p>
            </div>
          </div>
        </Container>
      </Section>
    </>
  );
}

function Card({
  icon: Icon,
  title,
  children,
}: {
  icon: typeof Lock;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <article
      className="card-static"
      style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 36,
          height: 36,
          borderRadius: "var(--r-md)",
          background: "var(--accent-glow)",
          color: "var(--accent)",
        }}
        aria-hidden="true"
      >
        <Icon size={18} />
      </span>
      <h3
        style={{
          fontSize: "var(--fs-lg)",
          margin: 0,
          color: "var(--text-primary)",
        }}
      >
        {title}
      </h3>
      <p
        style={{
          margin: 0,
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.6,
        }}
      >
        {children}
      </p>
    </article>
  );
}

function BulletPoint({ children }: { children: React.ReactNode }) {
  return (
    <li
      style={{
        display: "flex",
        gap: "var(--sp-2)",
        alignItems: "flex-start",
      }}
    >
      <span
        aria-hidden="true"
        style={{
          display: "inline-block",
          width: 6,
          height: 6,
          background: "var(--accent)",
          borderRadius: 999,
          marginTop: 8,
          flexShrink: 0,
        }}
      />
      <span>{children}</span>
    </li>
  );
}
