import { Link } from "react-router-dom";
import { ArrowRight, FileText } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";

const DOCUMENTS = [
  {
    to: "/legal/terms",
    title: "Terms of service",
    summary: "What we provide, what you agree to, and how we handle disputes.",
  },
  {
    to: "/legal/privacy",
    title: "Privacy policy",
    summary: "What personal data we collect, why, how long we keep it, and how to ask for it back.",
  },
  {
    to: "/legal/aup",
    title: "Acceptable use policy",
    summary: "What you can and cannot do on Orvix: spam, abuse, content rules, and enforcement.",
  },
  {
    to: "/legal/cookies",
    title: "Cookie policy",
    summary: "How Orvix uses cookies and local storage, and how to opt out.",
  },
  {
    to: "/legal/data-and-privacy",
    title: "Data and privacy",
    summary: "Where your data lives, who can access it, how long we keep it, and how to exercise your data subject rights under GDPR.",
  },
];

export default function LegalIndex() {
  return (
    <>
      <SEO path="/legal" />

      <Hero
        eyebrow="Legal"
        heading="The documents that govern your use of Orvix"
        subheading="Plain-English summaries of our terms, privacy policy, acceptable use policy, and data handling. The full text of each document is on its own page."
      />

      <Section>
        <Container width="narrow">
          <ul
            style={{
              listStyle: "none",
              padding: 0,
              margin: 0,
              display: "grid",
              gap: "var(--sp-3)",
            }}
          >
            {DOCUMENTS.map((doc) => (
              <li key={doc.to}>
                <Link
                  to={doc.to}
                  className="card"
                  style={{
                    display: "flex",
                    alignItems: "flex-start",
                    gap: "var(--sp-3)",
                    textDecoration: "none",
                    color: "inherit",
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
                    <FileText size={18} />
                  </span>
                  <div style={{ flex: 1 }}>
                    <h3
                      style={{
                        fontSize: "var(--fs-lg)",
                        margin: 0,
                        color: "var(--text-primary)",
                      }}
                    >
                      {doc.title}
                    </h3>
                    <p
                      style={{
                        margin: "var(--sp-1) 0 0",
                        color: "var(--text-secondary)",
                        fontSize: "var(--fs-sm)",
                        lineHeight: 1.6,
                      }}
                    >
                      {doc.summary}
                    </p>
                  </div>
                  <ArrowRight
                    size={18}
                    aria-hidden="true"
                    style={{ color: "var(--text-faint)", flexShrink: 0, marginTop: 8 }}
                  />
                </Link>
              </li>
            ))}
          </ul>
        </Container>
      </Section>
    </>
  );
}
