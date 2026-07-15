import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

const SUBPROCESSORS = [
  { name: "Stripe", purpose: "Payment processing for paid plans", data: "Billing email, plan, last 4 of the card" },
  { name: "Cloudflare", purpose: "DNS and DDoS protection", data: "Source IP, query name" },
  { name: "Postmark", purpose: "Outbound mail delivery for transactional mail", data: "From, to, message ID, headers" },
];

const REGIONS = [
  { region: "United States (us-east-1)", residency: "Primary copy of mail content, metadata, and backups" },
  { region: "European Union (eu-west-1)", residency: "Available on Enterprise; data does not leave the region" },
  { region: "Asia-Pacific (ap-southeast-1)", residency: "Available on Enterprise; data does not leave the region" },
];

export default function LegalData() {
  return (
    <>
      <SEO path="/legal/data-and-privacy" />

      <Hero
        eyebrow="Legal"
        heading="Data and privacy"
        subheading="Where your mail lives, who can access it, how long we keep it, and how to exercise your rights under GDPR, CCPA, and other privacy regulations."
      />

      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <h2>Where your data lives</h2>
            <p>
              Mail content, metadata, and backups are stored in the region
              the operator selected at signup. Operators on Enterprise can
              deploy a dedicated instance in a region of their choice.
            </p>

            <h3>Available regions</h3>
            <ul>
              {REGIONS.map((r) => (
                <li key={r.region}>
                  <strong>{r.region}</strong> — {r.residency}
                </li>
              ))}
            </ul>

            <h2>Who can access your data</h2>
            <p>
              Orvix staff do not have routine access to mail content. Access
              is granted only:
            </p>
            <ul>
              <li>With your written request, to help you recover a deleted item or debug a configuration.</li>
              <li>When required to investigate a security incident, and only the minimum necessary, with the access logged in the audit trail.</li>
              <li>When required to comply with a valid legal order. We will notify you before disclosing unless we are legally prohibited from doing so.</li>
            </ul>

            <h2>Subprocessors</h2>
            <p>
              We share data with the following subprocessors. We notify
              customers at least 30 days before adding a new subprocessor.
            </p>
            <div style={{ overflowX: "auto" }}>
              <table
                style={{
                  width: "100%",
                  borderCollapse: "collapse",
                  fontSize: "var(--fs-sm)",
                }}
              >
                <thead>
                  <tr>
                    <th
                      style={{
                        textAlign: "left",
                        padding: "var(--sp-2) var(--sp-3)",
                        borderBottom: "1px solid var(--border-default)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      Subprocessor
                    </th>
                    <th
                      style={{
                        textAlign: "left",
                        padding: "var(--sp-2) var(--sp-3)",
                        borderBottom: "1px solid var(--border-default)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      Purpose
                    </th>
                    <th
                      style={{
                        textAlign: "left",
                        padding: "var(--sp-2) var(--sp-3)",
                        borderBottom: "1px solid var(--border-default)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      Data shared
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {SUBPROCESSORS.map((s) => (
                    <tr key={s.name}>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: "var(--text-primary)",
                          fontWeight: 500,
                        }}
                      >
                        {s.name}
                      </td>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: "var(--text-secondary)",
                        }}
                      >
                        {s.purpose}
                      </td>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: "var(--text-secondary)",
                        }}
                      >
                        {s.data}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <h2>Data subject rights (GDPR, UK GDPR, CCPA, and similar)</h2>
            <p>
              If you are in the EU, UK, California, or a similar regime, you
              have the right to:
            </p>
            <ul>
              <li>Access the personal data we hold about you.</li>
              <li>Rectify inaccurate data.</li>
              <li>Request erasure of your data.</li>
              <li>Restrict or object to processing.</li>
              <li>Port your data in a machine-readable format.</li>
              <li>Withdraw consent at any time, where processing is based on consent.</li>
              <li>Lodge a complaint with your data protection authority.</li>
            </ul>
            <p>
              To exercise any of these rights, email{" "}
              <a href="mailto:privacy@orvix.com">privacy@orvix.com</a>. We
              respond within 30 days.
            </p>

            <h2>Backups and retention</h2>
            <ul>
              <li>Backups are AES-256-GCM sealed with a per-operator key.</li>
              <li>Backups are retained for 30 days by default; configurable per organization up to 365 days.</li>
              <li>Mail deleted from a mailbox is purged from the live store within 90 days and from backups at the next backup rotation.</li>
            </ul>

            <h2>International transfers</h2>
            <p>
              For transfers from the EU/UK to a region outside the EEA, we
              rely on the European Commission&apos;s Standard Contractual
              Clauses and the UK International Data Transfer Addendum.
              The data we transfer is limited to the data necessary to
              provide the service.
            </p>

            <h2>Security controls</h2>
            <p>
              The full list of security controls is on the{" "}
              <a href="/security">security page</a>. The short version: TLS
              1.2 minimum, AES-256-GCM at rest, DKIM/SPF/DMARC by default,
              MFA available everywhere, audit log on every change, and a
              coordinated-disclosure program at{" "}
              <a href="mailto:security@orvix.com">security@orvix.com</a>.
            </p>

            <h2>Changes to this page</h2>
            <p>
              We&apos;ll notify you by email at least 30 days before adding a
              new subprocessor or changing a retention period in a way that
              affects your data. Material changes are also announced on the
              changelog.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
