import SEO from "../components/SEO";
import Section from "../components/Section";
import Container from "../components/Container";
import Hero from "../components/Hero";

const COOKIES = [
  {
    name: "orvix.cookie.choice.v1",
    purpose: "Stores your choice from the cookie banner (accepted or rejected).",
    type: "Local storage / first-party cookie",
    retention: "12 months or until you clear it",
    required: true,
  },
  {
    name: "orvix_locale",
    purpose: "Stores your language preference (English or Arabic) and RTL choice.",
    type: "Local storage",
    retention: "12 months or until you clear it",
    required: false,
  },
  {
    name: "__Host-orvix_session",
    purpose: "Holds your session token. Set by the portal (app.orvix.com), not by this site.",
    type: "First-party cookie",
    retention: "Session",
    required: true,
  },
];

export default function LegalCookies() {
  return (
    <>
      <SEO path="/legal/cookies" />

      <Hero
        eyebrow="Legal"
        heading="Cookie policy"
        subheading="The full list of cookies and similar storage used on orvix.com. The portal (app.orvix.com) has its own cookie notice; this page is for the marketing site only."
      />

      <Section>
        <Container width="narrow">
          <div className="legal-doc prose">
            <h2>What we use cookies for</h2>
            <p>
              We use a small set of cookies and local-storage items to make
              this site work, to remember your language preference, and — only
              if you accept the analytics category — to count visits.
            </p>

            <h2>Categories</h2>

            <h3>Strictly necessary</h3>
            <p>
              These cookies are required for the site to work. They include
              the cookie-choice preference, the locale preference, and the
              session token used by the portal after you sign in. They cannot
              be disabled.
            </p>

            <h3>Functional</h3>
            <p>
              These cookies remember choices you make (language, RTL) so you
              don&apos;t have to set them on every visit. Disabling them in
              your browser is fine; the site will still work, you&apos;ll just
              have to set the choice again next time.
            </p>

            <h3>Analytics</h3>
            <p>
              Analytics is opt-in. We do not set analytics cookies until you
              accept the analytics category in the cookie banner. If you
              reject, we don&apos;t set them at all.
            </p>

            <h2>The cookies we set</h2>
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
                      Name
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
                      Retention
                    </th>
                    <th
                      style={{
                        textAlign: "left",
                        padding: "var(--sp-2) var(--sp-3)",
                        borderBottom: "1px solid var(--border-default)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      Required?
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {COOKIES.map((c) => (
                    <tr key={c.name}>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          fontFamily: "monospace",
                          color: "var(--text-primary)",
                          verticalAlign: "top",
                        }}
                      >
                        {c.name}
                      </td>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: "var(--text-secondary)",
                          verticalAlign: "top",
                        }}
                      >
                        {c.purpose}
                        <div
                          style={{
                            fontSize: "var(--fs-xs)",
                            color: "var(--text-faint)",
                            marginTop: 4,
                          }}
                        >
                          {c.type}
                        </div>
                      </td>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: "var(--text-secondary)",
                          verticalAlign: "top",
                        }}
                      >
                        {c.retention}
                      </td>
                      <td
                        style={{
                          padding: "var(--sp-2) var(--sp-3)",
                          borderBottom: "1px solid var(--border-subtle)",
                          color: c.required ? "var(--success)" : "var(--text-muted)",
                          verticalAlign: "top",
                        }}
                      >
                        {c.required ? "Yes" : "No"}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <h2>Managing cookies</h2>
            <p>
              You can clear orvix.com cookies in your browser at any time. To
              change your choice on the cookie banner, clear the
              <code>orvix.cookie.choice.v1</code> storage entry and reload the
              page; the banner will reappear.
            </p>

            <h2>Changes to this policy</h2>
            <p>
              We&apos;ll update this page when we add or remove a cookie. The
              last-updated date at the top of the page reflects the most
              recent change.
            </p>
          </div>
        </Container>
      </Section>
    </>
  );
}
