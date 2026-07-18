import { Link } from "react-router-dom";
import Container from "./Container";
import Logo from "./Logo";

const COLUMNS = [
  {
    title: "Product",
    links: [
      { to: "/features", label: "Features" },
      { to: "/enterprise", label: "Enterprise" },
      { to: "/security", label: "Security" },
      { to: "/pricing", label: "Pricing" },
      { to: "/status", label: "Status" },
    ],
  },
  {
    title: "Resources",
    links: [
      { to: "/docs", label: "Docs" },
      { to: "/api", label: "API" },
      { to: "/blog", label: "Blog" },
    ],
  },
  {
    title: "Company",
    links: [
      { to: "/about", label: "About" },
      { to: "/contact", label: "Contact" },
      { to: "mailto:careers@orvix.email", label: "Careers" },
    ],
  },
  {
    title: "Legal",
    links: [
      { to: "/legal/terms", label: "Terms" },
      { to: "/legal/privacy", label: "Privacy" },
      { to: "/legal/aup", label: "AUP" },
      { to: "/legal/data-and-privacy", label: "Data & privacy" },
      { to: "/legal/cookies", label: "Cookies" },
    ],
  },
];

export default function Footer() {
  return (
    <footer
      role="contentinfo"
      style={{
        marginTop: "auto",
        background: "var(--bg-canvas)",
        borderTop: "1px solid var(--border-default)",
        padding: "var(--sp-8) 0 var(--sp-6)",
      }}
    >
      <Container width="wide">
        <div
          className="footer-grid"
          style={{
            display: "grid",
            gridTemplateColumns: "1.4fr repeat(4, 1fr)",
            gap: "var(--sp-7)",
            marginBottom: "var(--sp-7)",
          }}
        >
          <div>
            <Link
              to="/"
              aria-label="Orvix home"
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: "var(--sp-2)",
                textDecoration: "none",
                color: "var(--text-primary)",
                fontWeight: 700,
                fontSize: "var(--fs-lg)",
              }}
            >
              <Logo size={28} />
              <span>Orvix</span>
            </Link>
            <p
              style={{
                marginTop: "var(--sp-3)",
                color: "var(--text-muted)",
                fontSize: "var(--fs-sm)",
                lineHeight: 1.6,
                maxWidth: "32ch",
              }}
            >
              Email hosting for teams that need it to work. Custom domains,
              encrypted transport, and admin controls.
            </p>
          </div>

          {COLUMNS.map((col) => (
            <nav key={col.title} aria-label={col.title}>
              <h3
                style={{
                  fontSize: "var(--fs-xs)",
                  fontWeight: 700,
                  textTransform: "uppercase",
                  letterSpacing: "0.1em",
                  color: "var(--text-faint)",
                  marginBottom: "var(--sp-3)",
                }}
              >
                {col.title}
              </h3>
              <ul
                style={{
                  listStyle: "none",
                  padding: 0,
                  margin: 0,
                  display: "flex",
                  flexDirection: "column",
                  gap: "var(--sp-2)",
                }}
              >
                {col.links.map((link) =>
                  link.to.startsWith("mailto:") ? (
                    <li key={link.to}>
                      <a
                        href={link.to}
                        style={{
                          color: "var(--text-secondary)",
                          fontSize: "var(--fs-sm)",
                          textDecoration: "none",
                        }}
                      >
                        {link.label}
                      </a>
                    </li>
                  ) : (
                    <li key={link.to}>
                      <Link
                        to={link.to}
                        style={{
                          color: "var(--text-secondary)",
                          fontSize: "var(--fs-sm)",
                          textDecoration: "none",
                        }}
                      >
                        {link.label}
                      </Link>
                    </li>
                  ),
                )}
              </ul>
            </nav>
          ))}
        </div>

        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: "var(--sp-3)",
            alignItems: "center",
            justifyContent: "space-between",
            paddingTop: "var(--sp-5)",
            borderTop: "1px solid var(--border-subtle)",
            color: "var(--text-muted)",
            fontSize: "var(--fs-xs)",
          }}
        >
          <span>© {new Date().getFullYear()} Orvix, Inc.</span>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "var(--sp-3)",
            }}
            aria-label="Language"
          >
            <a
              href="/"
              aria-label="English"
              style={{ color: "var(--text-primary)", fontWeight: 600 }}
            >
              English
            </a>
            <span aria-hidden="true">·</span>
            <a
              href="/?rtl=1"
              aria-label="العربية"
              style={{ color: "var(--text-muted)" }}
            >
              العربية
            </a>
          </div>
        </div>
      </Container>

      <style>{`
        @media (max-width: 880px) {
          .footer-grid {
            grid-template-columns: 1fr 1fr !important;
          }
        }
        @media (max-width: 520px) {
          .footer-grid {
            grid-template-columns: 1fr !important;
          }
        }
      `}</style>
    </footer>
  );
}
