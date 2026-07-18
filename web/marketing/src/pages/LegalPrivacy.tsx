import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

export default function LegalPrivacy() {
  return (
    <>
      <SEO path="/legal/privacy" />

      <Hero
        eyebrow="Legal"
        heading="Privacy policy"
        subheading="Last updated July 2026. Plain-English summary first, then the full text."
      />

      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <p>
              <em>Plain-English summary</em> — we collect the personal data we
              need to run the service, we don&apos;t sell it, and you can ask
              for it back. The full text is below.
            </p>

            <h2>What we collect</h2>
            <ul>
              <li>Account: name, email, organization, password hash.</li>
              <li>Billing: plan, billing email, last 4 of the payment card on file (the full card is held by the billing provider, not by us).</li>
              <li>Service usage: API operations, messages sent, quota accounting, and security events needed to operate the product.</li>
              <li>Mail: the content of the messages you send and receive on Orvix. This is processed on your behalf as a processor.</li>
              <li>Logs: connection metadata (source IP, TLS handshake, user agent) for security and debugging.</li>
            </ul>

            <h2>Why we collect it</h2>
            <ul>
              <li>To provide the service you signed up for.</li>
              <li>To bill you for paid plans.</li>
              <li>To secure the service and prevent abuse.</li>
              <li>To comply with legal obligations.</li>
            </ul>

            <h2>How long we keep it</h2>
            <ul>
              <li>Account, mail, logs, billing, and backup retention depend on product behavior, operator configuration, and applicable legal requirements.</li>
              <li>Obtain the deployment-specific retention schedule from the operator or applicable customer agreement.</li>
            </ul>

            <h2>Who we share it with</h2>
            <p>
              We do not claim to sell personal data. Service providers vary by
              deployment; the applicable provider list belongs in the operator
              privacy notice or customer agreement.
            </p>

            <h2>Your rights</h2>
            <ul>
              <li>Access, rectification, erasure, objection, and portability requests may be submitted to the address below.</li>
              <li>Identity and authority may be verified before a request is processed.</li>
            </ul>
            <p>
              To exercise any of these rights, email{" "}
              <a href="mailto:privacy@orvix.email">privacy@orvix.email</a>. We
              will process requests under the applicable policy and law.
            </p>

            <h2>Children</h2>
            <p>
              Orvix is not intended for children under 16. We do not
              knowingly collect data from children under 16.
            </p>

            <h2>International transfers</h2>
            <p>
              Data location and any international transfer depend on the actual
              deployment and its service providers. This page does not promise
              region selection or multi-region residency.
            </p>

            <h2>Changes to this policy</h2>
            <p>
              Material changes will be published with an updated effective date.
            </p>

            <h2>Contact</h2>
            <p>
              Email <a href="mailto:privacy@orvix.email">privacy@orvix.email</a>{" "}
              with any questions.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
