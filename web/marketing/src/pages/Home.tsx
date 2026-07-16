import { Link } from "react-router-dom";
import {
  ShieldCheck,
  Mail,
  KeyRound,
  Globe,
  Inbox,
  Users,
  Lock,
  Server,
  Activity,
  Layers,
} from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import PlanTable from "../components/PlanTable";
import FAQ, { type FaqItem } from "../components/FAQ";
import CapabilityBlock from "../components/CapabilityBlock";
import Illustration from "../components/Illustration";
import { PORTAL_SIGNUP } from "../lib/links";

const FAQ_ITEMS: FaqItem[] = [
  {
    q: "Do I need to bring my own domain?",
    a: "Yes. Orvix is for teams that want mail on their own domain (you@yourcompany.com). The Free plan supports one custom domain; paid plans add more.",
  },
  {
    q: "Is there a free plan?",
    a: "Yes. The Free plan is $0 forever and includes 1 domain, 5 mailboxes, 1 GB of storage, and 500 sends per day. No credit card required.",
  },
  {
    q: "Can I migrate from Google Workspace or Microsoft 365?",
    a: "Yes. The Getting Started guide walks through MX cutover, DKIM rotation, and (optionally) IMAP import for historical mail.",
  },
  {
    q: "Where is my data stored?",
    a: "Mail content and metadata are stored on the operator's server. The data and privacy page documents retention, backups, and the conditions under which Orvix staff can access a mailbox.",
  },
  {
    q: "Do you support SSO?",
    a: "Yes — SAML and OIDC SSO are included on the Enterprise plan.",
  },
  {
    q: "How do I report a vulnerability?",
    a: "Email security@orvix.com. The security page has our disclosure policy and PGP key.",
  },
];

export default function Home() {
  return (
    <>
      <SEO path="/" />

      <Hero
        eyebrow="Email hosting for teams"
        heading={
          <>
            Email that just works —{" "}
            <span style={{ color: "var(--accent)" }}>on your own domain.</span>
          </>
        }
        subheading="Orvix is professional email hosting with custom domains, encrypted transport, and the admin controls a real team needs. Start free with up to 5 mailboxes on 1 domain."
        primaryCta={{ to: PORTAL_SIGNUP, label: "Start free", external: true }}
        secondaryCta={{ to: "/pricing", label: "See pricing" }}
        belowCta={
          <>
            No credit card required · DKIM + MTA-STS included · Cancel any time
          </>
        }
        illustration={<Illustration variant="inbox" height={380} />}
      />

      <Section
        alt
        eyebrow="The shape of Orvix"
        heading="Everything you need to run a real mail team"
        lede="Orvix is one product with three surfaces — webmail for the people who send and receive, admin for the people who run the org, and an API for the people who wire it into their stack."
      >
        <div
          className="three-col"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, 1fr)",
            gap: "var(--sp-4)",
          }}
        >
          <CapabilityBlock
            icon={Inbox}
            title="Webmail"
            body="Webmail with folders, search, drafts, compose, attachments, settings, and notifications."
            href="/features"
            hrefLabel="Tour the webmail"
          />
          <CapabilityBlock
            icon={Server}
            title="Admin"
            body="Manage domains, mailboxes, aliases, groups, runtime status, and security controls from one place."
            href="/enterprise"
            hrefLabel="Tour the admin"
          />
          <CapabilityBlock
            icon={Layers}
            title="API"
            body="Documented public, customer, enterprise, and operator HTTP routes with an OpenAPI 3.0 contract."
            href="/api"
            hrefLabel="Read the API one-pager"
          />
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        eyebrow="Built for the boring 99% of mail"
        heading="Encryption, authentication, and quotas — done right, out of the box"
        lede="Orvix combines transport configuration, domain-authentication workflows, MFA, access controls, quotas, and audit facilities. Deployment-dependent controls remain the operator's responsibility."
      >
        <div
          className="two-col"
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
              gap: "var(--sp-4)",
            }}
          >
            <Bullet
              icon={Lock}
              title="Transport and storage controls"
              body="TLS support for mail transport, with at-rest storage and backup protection determined by the operator's deployment configuration."
            />
            <Bullet
              icon={ShieldCheck}
              title="DKIM, SPF, DMARC, and MTA-STS"
              body="The DNS workflow generates the records and policy data the operator must publish and verify for each domain."
            />
            <Bullet
              icon={KeyRound}
              title="Multi-factor authentication"
              body="TOTP-based MFA on every account, with optional enforcement at the organization level."
            />
            <Bullet
              icon={Activity}
              title="Real audit log"
              body="Every admin action — who, what, when, from where. Exportable as CSV for your security team."
            />
          </ul>
          <Illustration variant="admin-dashboard" height={420} />
        </div>
        <style>{`@media (max-width: 880px) { .two-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        alt
        eyebrow="The whole product"
        heading="All four plans, side by side"
        lede="Four plans, four honest price points. Pick the one that matches your team and upgrade when you outgrow it."
      >
        <PlanTable />
        <p
          style={{
            textAlign: "center",
            marginTop: "var(--sp-5)",
            color: "var(--text-muted)",
            fontSize: "var(--fs-sm)",
          }}
        >
          Need a closer look?{" "}
          <Link to="/pricing" style={{ color: "var(--accent)" }}>
            See the full pricing page
          </Link>{" "}
          or{" "}
          <Link to="/features" style={{ color: "var(--accent)" }}>
            compare every feature
          </Link>
          .
        </p>
      </Section>

      <Section
        eyebrow="Why teams pick Orvix"
        heading="Email is the kind of infrastructure you only notice when it breaks"
        lede="So we built Orvix to be the opposite: predictable, transparent, and easy to leave if you ever want to."
      >
        <div
          className="three-col"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(3, 1fr)",
            gap: "var(--sp-4)",
          }}
        >
          <CapabilityBlock
            icon={Globe}
            title="Your domain, your identity"
            body="Mail for your own domain, not a shared orvix.com address. Set up DKIM and DMARC in minutes — we give you the exact DNS records to publish."
            href="/security"
            hrefLabel="Read the security overview"
          />
          <CapabilityBlock
            icon={Users}
            title="Real team controls"
            body="Invite teammates with roles, set per-mailbox quotas, enforce MFA, and review every administrative change in the audit log."
            href="/features"
            hrefLabel="See all features"
          />
          <CapabilityBlock
            icon={Mail}
            title="Standard protocols, no lock-in"
            body="IMAP, JMAP, and SMTP. Connect any client, export your data any time, and walk away with your mail if you ever want to."
            href="/api"
            hrefLabel="See the API"
          />
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section alt bordered id="faq">
        <Container width="narrow">
          <FAQ items={FAQ_ITEMS} />
        </Container>
      </Section>

      <Section>
        <div
          style={{
            background:
              "linear-gradient(180deg, var(--accent-soft) 0%, var(--bg-canvas) 100%)",
            border: "1px solid var(--accent)",
            borderRadius: "var(--r-xl)",
            padding: "var(--sp-8) var(--sp-6)",
            textAlign: "center",
          }}
        >
          <h2
            style={{
              fontSize: "var(--fs-3xl)",
              margin: 0,
              color: "var(--text-primary)",
            }}
          >
            Ready to put your mail on a real product?
          </h2>
          <p
            style={{
              marginTop: "var(--sp-3)",
              marginBottom: "var(--sp-5)",
              color: "var(--text-secondary)",
              maxWidth: "60ch",
              marginLeft: "auto",
              marginRight: "auto",
            }}
          >
            Free forever for one domain and up to five mailboxes. Upgrade when
            you need more — same product, same team, same status page.
          </p>
          <div
            style={{
              display: "flex",
              flexWrap: "wrap",
              gap: "var(--sp-3)",
              justifyContent: "center",
            }}
          >
            <a href={PORTAL_SIGNUP} className="btn btn-primary btn-lg">
              Start free
            </a>
            <Link to="/contact" className="btn btn-secondary btn-lg">
              Talk to sales
            </Link>
          </div>
        </div>
      </Section>
    </>
  );
}

function Bullet({
  icon: Icon,
  title,
  body,
}: {
  icon: typeof Inbox;
  title: string;
  body: string;
}) {
  return (
    <li
      style={{
        display: "flex",
        gap: "var(--sp-3)",
        alignItems: "flex-start",
      }}
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
          flexShrink: 0,
        }}
        aria-hidden="true"
      >
        <Icon size={18} />
      </span>
      <div>
        <h4
          style={{
            fontSize: "var(--fs-base)",
            margin: 0,
            color: "var(--text-primary)",
          }}
        >
          {title}
        </h4>
        <p
          style={{
            margin: "var(--sp-1) 0 0",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-sm)",
            lineHeight: 1.6,
          }}
        >
          {body}
        </p>
      </div>
    </li>
  );
}

// (Icons used inline above.)
