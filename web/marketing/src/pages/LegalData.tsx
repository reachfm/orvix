import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

export default function LegalData() {
  return (
    <>
      <SEO path="/legal/data-and-privacy" />
      <Hero eyebrow="Legal" heading="Data and privacy" subheading="A deployment-aware summary of data handling. Contractual privacy terms belong in the applicable agreement." />
      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <h2>Deployment location</h2>
            <p>Orvix is deployment software. Mail, metadata, database records, logs, and backups are stored wherever the operator configures the service and its infrastructure. This page does not promise region selection, a dedicated instance, or multi-region residency.</p>

            <h2>Operator responsibility</h2>
            <p>The operator controls hosting, storage, database, backup, retention, encryption-at-rest, key management, monitoring, and access policy. Buyers should obtain deployment-specific evidence before relying on any of those controls.</p>

            <h2>Service providers</h2>
            <p>This release does not publish a universal subprocessor list because providers vary by deployment. Any applicable provider list and advance-notice obligation must be stated in the customer agreement or the operator's privacy notice.</p>

            <h2>Product data</h2>
            <p>Orvix processes account, tenant, mailbox, message, delivery, audit, security, billing, and operational data needed to provide the configured service. Access is controlled through the product's authentication and authorization mechanisms.</p>

            <h2>Retention and deletion</h2>
            <p>Retention is controlled by product behavior and operator configuration. This page does not promise a universal backup-retention or deletion schedule. Confirm the schedule for the deployment covered by your agreement.</p>

            <h2>Privacy requests</h2>
            <p>Email <a href="mailto:privacy@orvix.com">privacy@orvix.com</a> with the account, organization, and request type. Identity and authority may need to be verified before a request is processed.</p>

            <h2>Security controls</h2>
            <p>The <a href="/security">security page</a> separates implemented product controls from deployment-dependent controls. It does not represent an external certification.</p>
          </div>
        </Container>
      </Section>
    </>
  );
}
