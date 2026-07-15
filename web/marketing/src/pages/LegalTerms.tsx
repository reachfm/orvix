import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

export default function LegalTerms() {
  return (
    <>
      <SEO path="/legal/terms" />

      <Hero
        eyebrow="Legal"
        heading="Terms of service"
        subheading="Last updated July 2026. Plain-English summary followed by the full terms."
      />

      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <p>
              <em>Plain-English summary</em> — these are the rules of the road
              for using Orvix. The full terms are below. If anything in the
              summary disagrees with the full text, the full text wins.
            </p>

            <h2>Plain-English summary</h2>
            <ul>
              <li>You can use Orvix to send and receive mail for your own domain.</li>
              <li>You can&apos;t use Orvix to send spam, host phishing, or do anything illegal.</li>
              <li>We can suspend or terminate your account if you break these rules or the AUP.</li>
              <li>We&apos;ll give you 30 days&apos; notice of any material change to these terms.</li>
              <li>The full text is below.</li>
            </ul>

            <h2>1. Who we are and who you are</h2>
            <p>
              Orvix is a professional email hosting service operated by Orvix,
              Inc. (&quot;Orvix&quot;, &quot;we&quot;, &quot;us&quot;). When you create
              an account, you become a &quot;customer&quot; and you agree to these
              terms.
            </p>

            <h2>2. What we provide</h2>
            <p>
              We provide email hosting: SMTP inbound and outbound, IMAP and
              JMAP, a webmail client, an admin console, and a REST API. The
              plan you have determines the limits. The plan catalog is on the
              pricing page and is also returned by{" "}
              <code>GET /api/v1/billing/plans</code>.
            </p>

            <h2>3. What you agree to</h2>
            <p>You agree to:</p>
            <ul>
              <li>Provide accurate account information.</li>
              <li>Keep your password and API keys secure.</li>
              <li>Use Orvix in compliance with the acceptable use policy.</li>
              <li>Pay the fees for your plan on time.</li>
              <li>Comply with all applicable laws.</li>
            </ul>

            <h2>4. What you may not do</h2>
            <p>You may not use Orvix to:</p>
            <ul>
              <li>Send unsolicited bulk email (spam).</li>
              <li>Host phishing, malware, or other malicious content.</li>
              <li>Infringe intellectual property rights.</li>
              <li>Violate the privacy of any person.</li>
              <li>Do anything illegal in the jurisdiction you operate from.</li>
            </ul>
            <p>
              The full list is in the acceptable use policy. Violations may
              result in immediate suspension.
            </p>

            <h2>5. Fees and billing</h2>
            <p>
              Fees for paid plans are billed monthly or annually, in advance.
              The Free plan is $0. We don&apos;t issue refunds for partial
              billing periods except as required by law. Annual plans can be
              cancelled; the cancellation takes effect at the end of the
              billing period and is not pro-rated.
            </p>

            <h2>6. Suspension and termination</h2>
            <p>
              We may suspend or terminate your account if:
            </p>
            <ul>
              <li>You breach these terms or the AUP.</li>
              <li>Your payment fails and is not cured within the grace period documented in the subscription-states guide.</li>
              <li>We are required to do so by law.</li>
            </ul>
            <p>
              For suspensions, we&apos;ll tell you why and what you need to do
              to fix it. For terminations, we&apos;ll give you at least 30
              days&apos; notice unless the reason is illegal activity or an
              immediate threat to other customers.
            </p>

            <h2>7. Your data</h2>
            <p>
              You retain all rights to the mail content you create on Orvix. We
              process that data on your behalf as a processor. The data and
              privacy page documents the data we collect, where it lives, who
              can access it, and how to ask for it back.
            </p>

            <h2>8. Our data</h2>
            <p>
              We retain the right to use anonymized, aggregate data about how
              Orvix is used to improve the service. We do not sell your data
              to third parties.
            </p>

            <h2>9. Service-level commitments</h2>
            <p>
              The Free, Starter, and Business plans are provided as-is, with
              no uptime SLA. The Enterprise plan includes a 99.99% uptime SLA
              with credits documented in the order form.
            </p>

            <h2>10. Warranties and disclaimers</h2>
            <p>
              Except as expressly stated, Orvix is provided &quot;as is&quot;.
              We disclaim all implied warranties to the maximum extent
              permitted by law.
            </p>

            <h2>11. Limitation of liability</h2>
            <p>
              To the maximum extent permitted by law, our total liability for
              any claim arising out of or relating to Orvix is limited to the
              fees you paid us in the 12 months preceding the claim. We are
              not liable for indirect, incidental, special, or consequential
              damages.
            </p>

            <h2>12. Indemnification</h2>
            <p>
              You agree to indemnify and hold Orvix harmless from any claim
              arising out of your use of Orvix in violation of these terms
              or the AUP.
            </p>

            <h2>13. Changes to these terms</h2>
            <p>
              We may update these terms from time to time. We&apos;ll give
              you 30 days&apos; notice of any material change. Continuing to
              use Orvix after the change takes effect constitutes acceptance.
            </p>

            <h2>14. Disputes</h2>
            <p>
              These terms are governed by the laws of the State of Delaware,
              without regard to its conflict-of-laws rules. Any dispute will
              be resolved in the state or federal courts located in Delaware.
            </p>

            <h2>15. Contact</h2>
            <p>
              Questions about these terms? Email{" "}
              <a href="mailto:legal@orvix.com">legal@orvix.com</a>.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
