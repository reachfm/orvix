import { Link } from "react-router-dom";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";

const POPULAR = [
  { to: "/", label: "Home" },
  { to: "/pricing", label: "Pricing" },
  { to: "/features", label: "Features" },
  { to: "/enterprise", label: "Enterprise" },
  { to: "/security", label: "Security" },
  { to: "/docs", label: "Docs" },
  { to: "/api", label: "API" },
  { to: "/contact", label: "Contact" },
];

export default function NotFound() {
  return (
    <>
      <SEO path="/" title="Page not found — Orvix" noindex />
      <Hero
        eyebrow="404"
        heading="We couldn't find that page"
        subheading="It may have been moved, renamed, or it may never have existed. Here are some pages that do exist:"
      />
      <Section>
        <Container width="narrow">
          <ul
            style={{
              listStyle: "none",
              padding: 0,
              margin: 0,
              display: "grid",
              gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))",
              gap: "var(--sp-2)",
            }}
          >
            {POPULAR.map((p) => (
              <li key={p.to}>
                <Link
                  to={p.to}
                  className="card-static"
                  style={{
                    display: "block",
                    color: "var(--text-primary)",
                    textDecoration: "none",
                    padding: "var(--sp-4) var(--sp-5)",
                    textAlign: "center",
                    fontWeight: 600,
                  }}
                >
                  {p.label}
                </Link>
              </li>
            ))}
          </ul>
          <p
            style={{
              marginTop: "var(--sp-5)",
              color: "var(--text-muted)",
              fontSize: "var(--fs-sm)",
              textAlign: "center",
            }}
          >
            Still can&apos;t find what you&apos;re looking for?{" "}
            <Link to="/contact" style={{ color: "var(--accent)" }}>
              Get in touch
            </Link>{" "}
            and we&apos;ll point you in the right direction.
          </p>
        </Container>
      </Section>
    </>
  );
}
