import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

export default function LegalAup() {
  return (
    <>
      <SEO path="/legal/aup" />

      <Hero
        eyebrow="Legal"
        heading="Acceptable use policy"
        subheading="What you can and cannot do on Orvix. Plain English, with the consequences for violations."
      />

      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <h2>The short version</h2>
            <ul>
              <li>Use Orvix for legitimate mail for your own domain.</li>
              <li>Don&apos;t send spam, host phishing, or distribute malware.</li>
              <li>Don&apos;t use Orvix to do anything illegal in your jurisdiction.</li>
              <li>Don&apos;t try to abuse the platform, the network, or other customers.</li>
            </ul>

            <h2>What you may not do</h2>

            <h3>Spam and unsolicited mail</h3>
            <p>
              You may not use Orvix to send unsolicited bulk email. This
              includes (without limitation):
            </p>
            <ul>
              <li>Email to recipients who have not given you permission to contact them.</li>
              <li>Purchased or scraped mailing lists.</li>
              <li>Mail sent to harvested or guessed addresses.</li>
              <li>Mail designed to obscure its origin or bypass filters.</li>
            </ul>
            <p>
              Transactional mail (order confirmations, password resets,
              receipts) to existing customers is allowed and is not
              considered spam.
            </p>

            <h3>Abuse and fraud</h3>
            <p>You may not use Orvix to:</p>
            <ul>
              <li>Send phishing or other social-engineering attacks.</li>
              <li>Distribute malware, ransomware, or other malicious content.</li>
              <li>Run a command-and-control channel.</li>
              <li>Impersonate another person, organization, or domain.</li>
              <li>Conduct fraud or other financial crime.</li>
            </ul>

            <h3>Content</h3>
            <p>You may not use Orvix to host or transmit content that:</p>
            <ul>
              <li>Is illegal in the jurisdiction you operate from.</li>
              <li>Infringes intellectual property rights.</li>
              <li>Is obscene or exploitative of minors.</li>
              <li>Incites violence or terrorism.</li>
              <li>Harasses, threatens, or defames another person.</li>
            </ul>

            <h3>Platform abuse</h3>
            <p>You may not:</p>
            <ul>
              <li>Attempt to gain unauthorized access to Orvix or another customer&apos;s account.</li>
              <li>Bypass rate limits or other technical controls.</li>
              <li>Reverse-engineer the service in violation of applicable law.</li>
              <li>Interfere with another customer&apos;s use of the service.</li>
              <li>Use Orvix in a way that creates a security or stability risk for the platform.</li>
            </ul>

            <h2>Enforcement</h2>
            <p>
              If we detect a violation, we will, in the order appropriate to
              the severity:
            </p>
            <ul>
              <li>Send you a warning with a description of the issue.</li>
              <li>Throttle or quarantine your outbound mail.</li>
              <li>Suspend your account pending resolution.</li>
              <li>Terminate your account and report the activity to the appropriate authorities.</li>
            </ul>
            <p>
              For severe violations (phishing, malware, child exploitation)
              we will skip the warning step and proceed directly to
              termination and reporting.
            </p>

            <h2>Reporting violations</h2>
            <p>
              If you see something on Orvix that violates this policy, email{" "}
              <a href="mailto:abuse@orvix.com">abuse@orvix.com</a> with the
              message ID, the source address, and a description of the issue.
              Reports are reviewed through the abuse process.
            </p>

            <h2>Changes to this policy</h2>
            <p>
              We may update this policy from time to time. We&apos;ll give
              you 30 days&apos; notice of any material change.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
