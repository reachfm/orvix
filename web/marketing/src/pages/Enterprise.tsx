import { Link } from "react-router-dom";
import {
  Building2,
  ShieldCheck,
  KeyRound,
  Users,
  Server,
  FileText,
  PhoneCall,
  Mail,
} from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import StepStrip from "../components/StepStrip";
import Illustration from "../components/Illustration";
import { PLANS, formatStorage } from "../lib/plans";

const ONBOARDING_STEPS = [
  {
    title: "Discovery call",
    body: "We learn your domain, your team size, your compliance needs, and your current setup.",
  },
  {
    title: "Pilot org",
    body: "We provision a dedicated Orvix instance for evaluation, including a sandbox domain for testing.",
  },
  {
    title: "Cutover plan",
    body: "MX, SPF, DKIM, DMARC, and (optionally) IMAP migration, with rollback steps written down.",
  },
  {
    title: "Go live + support",
    body: "A named engineer on Slack/email for 30 days. Then priority support for the life of the account.",
  },
];

export default function Enterprise() {
  const ent = PLANS.find((p) => p.id === "enterprise")!;
  return (
    <>
      <SEO path="/enterprise" />

      <Hero
        eyebrow="Enterprise"
        heading="For teams that need it to work, every time"
        subheading="100 domains, 1,000 mailboxes, 1 TB of storage, 100,000 sends per day, SSO, a 99.99% uptime SLA, and a direct line to a named support engineer."
        primaryCta={{ to: "/contact", label: "Talk to sales" }}
        secondaryCta={{ to: "/security", label: "Read the security overview" }}
      />

      <Section
        alt
        eyebrow="At a glance"
        heading="What the Enterprise plan includes"
        lede="These numbers are pulled directly from the plan catalog in the launch spec — they are the same numbers the billing system seeds and the API returns."
      >
        <div
          className="ent-grid"
          style={{
            display: "grid",
            gridTemplateColumns: "1.1fr 1fr",
            gap: "var(--sp-7)",
            alignItems: "center",
          }}
        >
          <ul
            style={{
              listStyle: "none",
              padding: 0,
              margin: 0,
              display: "grid",
              gap: "var(--sp-3)",
            }}
          >
            <Stat label="Domains" value={ent.domains === 1000 ? "Unlimited" : String(ent.domains)} />
            <Stat label="Mailboxes" value={ent.mailboxes === 1000 ? "Unlimited" : String(ent.mailboxes)} />
            <Stat label="Storage" value={formatStorage(ent.storageBytes)} />
            <Stat label="Sends per day" value={ent.sendsPerDay.toLocaleString()} />
            <Stat label="SLA" value="99.99% uptime" />
            <Stat label="Support" value="Priority, 30-min on Sev-1" />
          </ul>
          <Illustration variant="admin-queue" height={420} />
        </div>
        <style>{`@media (max-width: 880px) { .ent-grid { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        eyebrow="What's in the box"
        heading="Everything in Business, plus what enterprise teams need"
        lede="Single sign-on, a financially backed SLA, and the operational features your security team will ask about in the first call."
      >
        <div
          className="three-col"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, 1fr)",
            gap: "var(--sp-4)",
          }}
        >
          <Feature
            icon={KeyRound}
            title="SAML / OIDC SSO"
            body="Sign in with Okta, Entra ID, Google Workspace, Authentik, or any SAML / OIDC provider. SCIM provisioning is on the roadmap."
          />
          <Feature
            icon={ShieldCheck}
            title="99.99% uptime SLA"
            body="Financially backed. Service credits are issued automatically when we miss the bar — no support ticket required."
          />
          <Feature
            icon={PhoneCall}
            title="Priority support"
            body="A named engineer, a private Slack channel, and a 30-minute response on Sev-1. Same engineer for the life of the account."
          />
          <Feature
            icon={Users}
            title="Unlimited seats"
            body="Per-org pricing, not per-seat. Add every employee, contractor, and intern without negotiating a new plan."
          />
          <Feature
            icon={Server}
            title="Dedicated instance"
            body="On Enterprise we can run a dedicated instance for organizations that need full data isolation, including a private database."
          />
          <Feature
            icon={FileText}
            title="Compliance paperwork"
            body="We can sign DPAs, BAAs (where applicable), and security questionnaires. SOC 2 Type II is on the roadmap."
          />
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        alt
        eyebrow="Onboarding"
        heading="What it looks like to go live with us"
        lede="Most Enterprise customers cut over in under four weeks. Here's the shape of the project."
      >
        <StepStrip steps={ONBOARDING_STEPS} />
      </Section>

      <Section
        eyebrow="For your security team"
        heading="The questions we always get — and the answers"
        lede="If your security team wants to talk to ours before you sign, we will set that up in the first call."
      >
        <Container width="narrow">
          <div
            style={{
              display: "grid",
              gap: "var(--sp-3)",
            }}
          >
            <QABlock
              q="Where is mail stored?"
              a="On the operator's server, in the region you select at signup. We document the regions we support in the data and privacy page; new regions are added as customers ask for them."
            />
            <QABlock
              q="Can you run a private Orvix instance for us?"
              a="Yes — on Enterprise, we can deploy a dedicated instance in your VPC or your data center, with a private control plane and isolated database. Talk to sales."
            />
            <QABlock
              q="What encryption is used at rest?"
              a="AES-256-GCM for mailbox storage and backups. Keys are managed by the operator and rotated on a documented schedule."
            />
            <QABlock
              q="Can we disable mail access for a departing employee immediately?"
              a="Yes. Admins can revoke sessions, rotate passwords, suspend mailboxes, and download a final mbox export in a single flow. All of it is recorded in the audit log."
            />
            <QABlock
              q="Do you have a SOC 2 report?"
              a="SOC 2 Type II is on our roadmap. Today we can share a security overview, our latest penetration test summary, and answer your security questionnaire."
            />
            <QABlock
              q="Can you sign a Data Processing Addendum?"
              a="Yes — our standard DPA covers GDPR, CCPA, and the obligations we accept as a processor. We can also review and sign most customer-specific DPAs."
            />
          </div>
        </Container>
      </Section>

      <Section alt bordered>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "1.1fr 1fr",
            gap: "var(--sp-7)",
            alignItems: "center",
          }}
          className="cta-grid"
        >
          <div>
            <h2
              style={{
                fontSize: "var(--fs-3xl)",
                margin: 0,
                color: "var(--text-primary)",
              }}
            >
              Ready to scope a real conversation?
            </h2>
            <p
              style={{
                marginTop: "var(--sp-3)",
                color: "var(--text-secondary)",
                lineHeight: 1.7,
              }}
            >
              We&apos;ll set up a 30-minute discovery call with a sales engineer
              who has run Orvix migrations before. You&apos;ll leave the call with a
              rough cutover plan and a clear answer on whether Orvix is the
              right fit.
            </p>
            <p style={{ marginTop: "var(--sp-5)", display: "flex", gap: "var(--sp-3)", flexWrap: "wrap" }}>
              <Link to="/contact" className="btn btn-primary btn-lg">
                <Mail size={16} aria-hidden="true" />
                Talk to sales
              </Link>
              <Link to="/security" className="btn btn-secondary btn-lg">
                <ShieldCheck size={16} aria-hidden="true" />
                Read the security overview
              </Link>
            </p>
          </div>
          <Illustration variant="admin-mailboxes" height={380} />
        </div>
        <style>{`@media (max-width: 880px) { .cta-grid { grid-template-columns: 1fr !important; } }`}</style>
      </Section>
    </>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <li
      style={{
        display: "flex",
        justifyContent: "space-between",
        alignItems: "center",
        padding: "var(--sp-3) var(--sp-4)",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
      }}
    >
      <span style={{ color: "var(--text-muted)", fontSize: "var(--fs-sm)" }}>{label}</span>
      <strong style={{ color: "var(--text-primary)", fontSize: "var(--fs-base)" }}>{value}</strong>
    </li>
  );
}

function Feature({
  icon: Icon,
  title,
  body,
}: {
  icon: typeof Building2;
  title: string;
  body: string;
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
        {body}
      </p>
    </article>
  );
}

function QABlock({ q, a }: { q: string; a: string }) {
  return (
    <details
      style={{
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
        padding: "var(--sp-4) var(--sp-5)",
      }}
    >
      <summary
        style={{
          cursor: "pointer",
          fontWeight: 600,
          color: "var(--text-primary)",
          fontSize: "var(--fs-base)",
          listStyle: "none",
        }}
      >
        {q}
      </summary>
      <p
        style={{
          marginTop: "var(--sp-3)",
          marginBottom: 0,
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.7,
        }}
      >
        {a}
      </p>
    </details>
  );
}
