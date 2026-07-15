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
              <li>Usage: how you use Orvix (pages visited, API endpoints called, messages sent).</li>
              <li>Mail: the content of the messages you send and receive on Orvix. This is processed on your behalf as a processor.</li>
              <li>Logs: connection metadata (source IP, TLS handshake, user agent) for security and debugging.</li>
            </ul>

            <h2>Why we collect it</h2>
            <ul>
              <li>To provide the service you signed up for.</li>
              <li>To bill you for paid plans.</li>
              <li>To secure the service and prevent abuse.</li>
              <li>To improve the product (aggregate, anonymized analytics).</li>
              <li>To comply with legal obligations.</li>
            </ul>

            <h2>How long we keep it</h2>
            <ul>
              <li>Account: for the life of the account. Deleted within 30 days of account closure, except where retention is required by law.</li>
              <li>Mail: stored on the operator&apos;s server. Backups are retained for 30 days. Deleted within 90 days of mailbox deletion.</li>
              <li>Logs: 90 days for security and debugging.</li>
              <li>Billing: 7 years, where required by tax and accounting law.</li>
            </ul>

            <h2>Who we share it with</h2>
            <p>
              We do not sell your data. We share it with subprocessors only
              where necessary to provide the service. The current list is on
              the data and privacy page.
            </p>

            <h2>Your rights</h2>
            <ul>
              <li>Access: you can export your data at any time from the customer portal.</li>
              <li>Rectification: you can update your account information at any time.</li>
              <li>Erasure: you can delete your account, which erases associated personal data per the retention periods above.</li>
              <li>Objection: you can opt out of analytics cookies from the cookie banner.</li>
              <li>Portability: mail can be exported as mbox or EML.</li>
            </ul>
            <p>
              To exercise any of these rights, email{" "}
              <a href="mailto:privacy@orvix.com">privacy@orvix.com</a>. We
              respond within 30 days.
            </p>

            <h2>Children</h2>
            <p>
              Orvix is not intended for children under 16. We do not
              knowingly collect data from children under 16.
            </p>

            <h2>International transfers</h2>
            <p>
              Mail content and metadata are stored in the region the operator
              selected at signup. The data and privacy page lists the regions
              we operate in and the lawful basis for cross-region transfers
              where applicable.
            </p>

            <h2>Changes to this policy</h2>
            <p>
              We&apos;ll notify you of material changes by email at least 30
              days before they take effect.
            </p>

            <h2>Contact</h2>
            <p>
              Email <a href="mailto:privacy@orvix.com">privacy@orvix.com</a>{" "}
              with any questions.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
