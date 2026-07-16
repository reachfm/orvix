import { Link } from "react-router-dom";
import { KeyRound, Mail, ShieldCheck, SlidersHorizontal } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import StepStrip from "../components/StepStrip";
import Illustration from "../components/Illustration";
import { PLANS, formatStorage } from "../lib/plans";

const ONBOARDING_STEPS = [
  {
    title: "Discovery",
    body: "Define domain count, migration volume, security requirements, and the current mail environment.",
  },
  {
    title: "Evaluation",
    body: "Use a sandbox domain to validate administration, authentication, and mail flow before cutover.",
  },
  {
    title: "Cutover plan",
    body: "Document MX, SPF, DKIM, DMARC, migration, validation, and rollback steps.",
  },
  {
    title: "Production validation",
    body: "Verify DNS, mail flow, monitoring, and rollback readiness before production use.",
  },
];

export default function Enterprise() {
  const plan = PLANS.find((item) => item.id === "enterprise")!;

  return (
    <>
      <SEO path="/enterprise" />
      <Hero
        eyebrow="Enterprise"
        heading="Enterprise capacity with explicit limits"
        subheading="100 domains, 1,000 mailboxes, 1 TB of storage, 100,000 sends per day, SSO, a 99.99% uptime SLA, and priority support."
        primaryCta={{ to: "/contact", label: "Talk to sales" }}
        secondaryCta={{ to: "/security", label: "Review security" }}
      />

      <Section alt eyebrow="Plan catalog" heading="What Enterprise includes" lede="These values match the plan catalog seeded by the billing service.">
        <div className="ent-grid" style={{ display: "grid", gridTemplateColumns: "1.1fr 1fr", gap: "var(--sp-7)", alignItems: "center" }}>
          <ul style={{ listStyle: "none", padding: 0, margin: 0, display: "grid", gap: "var(--sp-3)" }}>
            <Stat label="Domains" value={String(plan.domains)} />
            <Stat label="Mailboxes" value={String(plan.mailboxes)} />
            <Stat label="Storage" value={formatStorage(plan.storageBytes)} />
            <Stat label="Sends per day" value={plan.sendsPerDay.toLocaleString()} />
            <Stat label="SLA" value="99.99% uptime" />
            <Stat label="Support" value="Priority support" />
          </ul>
          <Illustration variant="admin-queue" height={420} />
        </div>
        <style>{`@media (max-width: 880px) { .ent-grid { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section eyebrow="Enterprise controls" heading="The implemented plan differences" lede="This page describes product entitlements, not private-cloud, certification, or response-time commitments.">
        <div className="three-col" style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "var(--sp-4)" }}>
          <Feature icon={KeyRound} title="SAML / OIDC SSO" body="Connect a supported identity provider through the Enterprise SSO entitlement." />
          <Feature icon={ShieldCheck} title="Published SLA" body="The Enterprise plan includes the published 99.99% uptime SLA. Applicable remedies are defined in the executed order and terms." />
          <Feature icon={SlidersHorizontal} title="Priority support" body="Enterprise requests use the priority support entitlement. Order-specific channels and response terms must be stated in the executed order." />
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section alt eyebrow="Onboarding" heading="A controlled path to production" lede="Timing depends on DNS control, migration volume, domain count, and the operator's change process.">
        <StepStrip steps={ONBOARDING_STEPS} />
      </Section>

      <Section eyebrow="Procurement questions" heading="Deployment claims require deployment evidence" lede="Confirm the actual hosting, storage, backup, and contractual controls for the deployment covered by your order.">
        <Container width="narrow">
          <div style={{ display: "grid", gap: "var(--sp-3)" }}>
            <QABlock q="Where is mail stored?" a="Mail is stored by the Orvix deployment selected and operated for the account. Confirm its region and storage configuration before purchase." />
            <QABlock q="Does the public plan include a private VPC deployment?" a="No private-cloud or customer-VPC deployment is promised by this page. A different deployment model must be stated in an executed order." />
            <QABlock q="What encryption is used at rest?" a="At-rest protection depends on the database, filesystem, backup, and key-management controls used by the operator. Review that configuration before relying on a specific control." />
            <QABlock q="Is Orvix certified for SOC 2 or ISO 27001?" a="This release does not claim SOC 2 or ISO 27001 certification. The security page describes implemented controls, not a certification." />
            <QABlock q="Does this page create data-processing commitments?" a="No. Compliance and data-processing commitments must be stated in the executed customer order or agreement." />
          </div>
        </Container>
      </Section>

      <Section alt bordered>
        <div className="cta-grid" style={{ display: "grid", gridTemplateColumns: "1.1fr 1fr", gap: "var(--sp-7)", alignItems: "center" }}>
          <div>
            <h2 style={{ fontSize: "var(--fs-3xl)", margin: 0, color: "var(--text-primary)" }}>Discuss a deployment</h2>
            <p style={{ marginTop: "var(--sp-3)", color: "var(--text-secondary)", lineHeight: 1.7 }}>Use the contact form to discuss plan fit, deployment assumptions, migration scope, and order terms.</p>
            <p style={{ marginTop: "var(--sp-5)", display: "flex", gap: "var(--sp-3)", flexWrap: "wrap" }}>
              <Link to="/contact" className="btn btn-primary btn-lg"><Mail size={16} aria-hidden="true" />Talk to sales</Link>
              <Link to="/security" className="btn btn-secondary btn-lg"><ShieldCheck size={16} aria-hidden="true" />Review security</Link>
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
  return <li style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "var(--sp-3) var(--sp-4)", background: "var(--bg-canvas)", border: "1px solid var(--border-default)", borderRadius: "var(--r-md)" }}><span style={{ color: "var(--text-muted)", fontSize: "var(--fs-sm)" }}>{label}</span><strong>{value}</strong></li>;
}

function Feature({ icon: Icon, title, body }: { icon: typeof KeyRound; title: string; body: string }) {
  return <article className="card-static" style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}><Icon size={20} aria-hidden="true" /><h3 style={{ margin: 0 }}>{title}</h3><p style={{ margin: 0, color: "var(--text-secondary)", lineHeight: 1.6 }}>{body}</p></article>;
}

function QABlock({ q, a }: { q: string; a: string }) {
  return <details style={{ background: "var(--bg-canvas)", border: "1px solid var(--border-default)", borderRadius: "var(--r-md)", padding: "var(--sp-4) var(--sp-5)" }}><summary style={{ cursor: "pointer", fontWeight: 600 }}>{q}</summary><p style={{ marginBottom: 0, color: "var(--text-secondary)", lineHeight: 1.7 }}>{a}</p></details>;
}
