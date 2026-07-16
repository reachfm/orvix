import { Link } from "react-router-dom";
import { Mail, Briefcase, Heart, Compass, Building } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import { PORTAL_SIGNUP } from "../lib/links";

const PRINCIPLES = [
  {
    title: "Predictable, not magical",
    body:
      "Email is the kind of infrastructure you only notice when it breaks. We optimize for the case where nothing surprising happens — boring defaults, documented behavior, and change logs you can read.",
  },
  {
    title: "Open standards, not lock-in",
    body:
      "We use IMAP, JMAP, SMTP, and OpenAPI. Product behavior and supported export paths are documented rather than replaced with proprietary protocol claims.",
  },
  {
    title: "Honest numbers",
    body:
      "Pricing, limits, and SLAs are spelled out in the launch spec and the same numbers are returned by the billing API. There is no 'contact sales' wall between you and the price.",
  },
  {
    title: "Real security",
    body:
      "TLS, DKIM, SPF, DMARC, MFA, audit logging, and responsible-disclosure contact information. The security page separates product controls from deployment responsibilities.",
  },
];

const TEAM_PLACEHOLDERS = [
  {
    role: "Engineering",
    body:
      "Builds the mail runtime, administrative controls, customer portal, tests, and release tooling published with Orvix.",
  },
  {
    role: "Support",
    body:
      "Handles product and deployment questions through the contact channels published for each plan.",
  },
  {
    role: "Security",
    body:
      "Security reports can be sent to security@orvix.com. The security page explains what information to include.",
  },
];

export default function About() {
  return (
    <>
      <SEO path="/about" />

      <Hero
        eyebrow="About"
        heading="Email is critical infrastructure. We treat it that way."
        subheading="Orvix is email infrastructure for custom domains, with mail protocols, transport security, webmail, and administrative controls in one release."
        primaryCta={{ to: "/contact", label: "Get in touch" }}
        secondaryCta={{ to: "/security", label: "Read the security overview" }}
      />

      <Section
        alt
        eyebrow="What we believe"
        heading="The principles that shape the product"
        lede="The short version of how we think about Orvix. The long version is the launch spec and the docs."
      >
        <div
          className="two-col"
          style={{
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "var(--sp-4)",
          }}
        >
          {PRINCIPLES.map((p) => (
            <article
              key={p.title}
              className="card-static"
              style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}
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
                }}
                aria-hidden="true"
              >
                <Compass size={18} />
              </span>
              <h3
                style={{
                  fontSize: "var(--fs-lg)",
                  margin: 0,
                  color: "var(--text-primary)",
                }}
              >
                {p.title}
              </h3>
              <p
                style={{
                  margin: 0,
                  color: "var(--text-secondary)",
                  fontSize: "var(--fs-sm)",
                  lineHeight: 1.7,
                }}
              >
                {p.body}
              </p>
            </article>
          ))}
        </div>
        <style>{`@media (max-width: 880px) { .two-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section
        eyebrow="The team"
        heading="The work behind Orvix"
        lede="The product combines mail runtime, operations, security controls, documentation, and release engineering."
      >
        <Container>
          <div
            className="three-col"
            style={{
              display: "grid",
              gridTemplateColumns: "repeat(3, 1fr)",
              gap: "var(--sp-4)",
            }}
          >
            {TEAM_PLACEHOLDERS.map((t) => (
              <article
                key={t.role}
                className="card-static"
                style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}
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
                  }}
                  aria-hidden="true"
                >
                  <Briefcase size={18} />
                </span>
                <h3
                  style={{
                    fontSize: "var(--fs-lg)",
                    margin: 0,
                    color: "var(--text-primary)",
                  }}
                >
                  {t.role}
                </h3>
                <p
                  style={{
                    margin: 0,
                    color: "var(--text-secondary)",
                    fontSize: "var(--fs-sm)",
                    lineHeight: 1.7,
                  }}
                >
                  {t.body}
                </p>
              </article>
            ))}
          </div>
          <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
        </Container>
      </Section>

      <Section
        alt
        eyebrow="Where we are"
        heading="Where to find us"
        lede="We are a small distributed team. The addresses below are the right ones to use; everything else bounces."
      >
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
            <Row icon={Mail} label="General" value="hello@orvix.com" href="mailto:hello@orvix.com" />
            <Row icon={Briefcase} label="Sales" value="sales@orvix.com" href="mailto:sales@orvix.com" />
            <Row icon={Building} label="Press" value="press@orvix.com" href="mailto:press@orvix.com" />
            <Row icon={Heart} label="Careers" value="careers@orvix.com" href="mailto:careers@orvix.com" />
          </ul>
        </Container>
      </Section>

      <Section bordered>
        <div
          style={{
            background: "var(--bg-canvas)",
            border: "1px solid var(--border-default)",
            borderRadius: "var(--r-lg)",
            padding: "var(--sp-6)",
            textAlign: "center",
          }}
        >
          <h2
            style={{
              fontSize: "var(--fs-2xl)",
              margin: 0,
              color: "var(--text-primary)",
            }}
          >
            Want to see what we built?
          </h2>
          <p
            style={{
              marginTop: "var(--sp-3)",
              marginBottom: "var(--sp-5)",
              color: "var(--text-secondary)",
            }}
          >
            Sign up for a free account and follow the documented domain and
            DNS setup flow at your own deployment pace.
          </p>
          <p style={{ display: "flex", gap: "var(--sp-3)", justifyContent: "center", flexWrap: "wrap" }}>
            <a href={PORTAL_SIGNUP} className="btn btn-primary btn-lg">
              Start free
            </a>
            <Link to="/features" className="btn btn-secondary btn-lg">
              Tour the product
            </Link>
          </p>
        </div>
      </Section>
    </>
  );
}

function Row({
  icon: Icon,
  label,
  value,
  href,
}: {
  icon: typeof Mail;
  label: string;
  value: string;
  href: string;
}) {
  return (
    <li
      style={{
        display: "flex",
        alignItems: "center",
        gap: "var(--sp-3)",
        padding: "var(--sp-4) var(--sp-5)",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
      }}
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 32,
          height: 32,
          borderRadius: "var(--r-md)",
          background: "var(--accent-glow)",
          color: "var(--accent)",
        }}
        aria-hidden="true"
      >
        <Icon size={16} />
      </span>
      <span
        style={{
          color: "var(--text-muted)",
          fontSize: "var(--fs-sm)",
          width: 96,
        }}
      >
        {label}
      </span>
      <a
        href={href}
        style={{
          color: "var(--text-primary)",
          fontWeight: 600,
          fontSize: "var(--fs-sm)",
        }}
      >
        {value}
      </a>
    </li>
  );
}
