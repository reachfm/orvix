import { Eye, KeyRound, Lock, Mail, Server, ShieldCheck } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import ComplianceTable from "../components/ComplianceTable";
import Illustration from "../components/Illustration";

const CONTROLS = [
  { topic: "Transport encryption", detail: "TLS is configured by the deployment for HTTPS and secure mail protocols. SMTP STARTTLS behavior remains visible to the operator and depends on the remote peer." },
  { topic: "Domain authentication", detail: "Orvix provides DKIM, SPF, DMARC, MTA-STS, and TLS reporting configuration through its DNS operations workflow. Operators must publish and verify the generated records." },
  { topic: "Account security", detail: "TOTP MFA, login throttling, lockout status, session controls, and administrative audit records are implemented in the product." },
  { topic: "Access control", detail: "Administrative, webmail, JMAP, and mail-protocol access are controlled independently. Authentication failures use generic responses." },
  { topic: "Backups", detail: "Backup creation, listing, verification status, and restore workflows are implemented. Encryption, off-host storage, and retention depend on the operator's deployment configuration." },
  { topic: "Data residency", detail: "This website does not promise region selection or multi-region residency. Data location is determined by the actual Orvix deployment and its operators." },
];

export default function Security() {
  return (
    <>
      <SEO path="/security" />
      <Hero
        eyebrow="Security"
        heading="Security controls without certification theater"
        subheading="Review the implemented transport, authentication, access-control, audit, and backup capabilities. Deployment-dependent controls are identified as such."
        primaryCta={{ to: "/contact", label: "Contact security" }}
        secondaryCta={{ to: "/legal/data-and-privacy", label: "Data and privacy" }}
        illustration={<Illustration variant="admin-domains" height={360} />}
      />

      <Section alt eyebrow="Product controls" heading="What the software implements" lede="These are product capabilities. Their effectiveness still depends on correct installation, DNS, monitoring, and operator policy.">
        <div className="three-col" style={{ display: "grid", gridTemplateColumns: "repeat(3, 1fr)", gap: "var(--sp-4)" }}>
          <Card icon={Lock} title="Transport security">HTTPS, IMAPS, POP3S, SMTPS, SMTP STARTTLS, MTA-STS, and TLS reporting are supported by the deployment and DNS workflows.</Card>
          <Card icon={ShieldCheck} title="Domain authentication">DKIM, SPF, and DMARC records are generated from configured domain and public-IP data for operator publication.</Card>
          <Card icon={KeyRound} title="Account protection">TOTP MFA, login protection, session controls, and protocol-level access settings are implemented.</Card>
          <Card icon={Eye} title="Auditability">Administrative actions and security-sensitive operations are recorded through the audit facilities exposed by the product.</Card>
          <Card icon={Server} title="Deployment responsibility">Storage encryption, backup location, retention, host hardening, and residency are deployment choices, not universal website promises.</Card>
          <Card icon={Mail} title="Responsible disclosure">Report reproducible security issues to security@orvix.com. Do not include credentials or unrelated personal data.</Card>
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section eyebrow="Control matrix" heading="Implemented versus deployment-dependent" lede="Use this distinction during security review and procurement.">
        <ComplianceTable caption="Orvix security controls and deployment responsibilities." rows={CONTROLS} />
      </Section>

      <Section alt eyebrow="Vulnerability disclosure" heading="Report a security issue" lede="Send a concise report with impact and reproduction steps. No response-time or bounty commitment is created by this page.">
        <Container width="narrow">
          <div className="card-static">
            <h3 style={{ marginTop: 0 }}>Include</h3>
            <ul>
              <li>The affected endpoint, protocol, or component.</li>
              <li>Steps that reproduce the issue and the observed impact.</li>
              <li>A safe contact address for follow-up.</li>
            </ul>
            <p style={{ marginBottom: 0 }}><a href="mailto:security@orvix.com" className="btn btn-primary"><Mail size={16} aria-hidden="true" />Email security@orvix.com</a></p>
          </div>
        </Container>
      </Section>

      <Section bordered>
        <Container width="narrow">
          <p style={{ color: "var(--text-secondary)", lineHeight: 1.7 }}><strong>Certification notice:</strong> this release does not claim SOC 2, ISO 27001, PCI DSS, or HIPAA certification. A product feature list is not an independent audit or legal compliance determination.</p>
        </Container>
      </Section>
    </>
  );
}

function Card({ icon: Icon, title, children }: { icon: typeof Lock; title: string; children: React.ReactNode }) {
  return <article className="card-static"><Icon size={20} aria-hidden="true" /><h3>{title}</h3><p style={{ color: "var(--text-secondary)", lineHeight: 1.6 }}>{children}</p></article>;
}
