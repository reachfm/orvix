import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import Container from "./Container";

interface HeroProps {
  eyebrow?: string;
  heading: ReactNode;
  subheading?: ReactNode;
  primaryCta?: { to: string; label: string; external?: boolean };
  secondaryCta?: { to: string; label: string; external?: boolean };
  belowCta?: ReactNode;
  /** Render the children below the CTA row. Used for illustrations. */
  illustration?: ReactNode;
}

export default function Hero({
  eyebrow,
  heading,
  subheading,
  primaryCta,
  secondaryCta,
  belowCta,
  illustration,
}: HeroProps) {
  return (
    <div
      style={{
        position: "relative",
        padding: "var(--sp-9) 0 var(--sp-8)",
        background:
          "radial-gradient(ellipse 80% 60% at 50% 0%, var(--accent-glow) 0%, transparent 70%), var(--bg-app)",
        overflow: "hidden",
      }}
    >
      <Container>
        <div
          style={{
            display: "grid",
            gap: "var(--sp-7)",
            textAlign: "center",
            maxWidth: "780px",
            margin: "0 auto",
          }}
        >
          {eyebrow && <span className="eyebrow">{eyebrow}</span>}
          <h1
            style={{
              fontSize: "clamp(36px, 5vw, 56px)",
              lineHeight: 1.05,
              letterSpacing: "-0.02em",
              color: "var(--text-primary)",
              margin: 0,
            }}
          >
            {heading}
          </h1>
          {subheading && (
            <p
              className="lede"
              style={{
                margin: "0 auto",
                fontSize: "var(--fs-lg)",
                color: "var(--text-secondary)",
              }}
            >
              {subheading}
            </p>
          )}
          {(primaryCta || secondaryCta) && (
            <div
              style={{
                display: "flex",
                flexWrap: "wrap",
                gap: "var(--sp-3)",
                justifyContent: "center",
                marginTop: "var(--sp-2)",
              }}
            >
              {primaryCta &&
                (primaryCta.external ? (
                  <a
                    href={primaryCta.to}
                    className="btn btn-primary btn-lg"
                    rel="noopener"
                  >
                    {primaryCta.label}
                  </a>
                ) : (
                  <Link to={primaryCta.to} className="btn btn-primary btn-lg">
                    {primaryCta.label}
                  </Link>
                ))}
              {secondaryCta &&
                (secondaryCta.external ? (
                  <a
                    href={secondaryCta.to}
                    className="btn btn-secondary btn-lg"
                    rel="noopener"
                  >
                    {secondaryCta.label}
                  </a>
                ) : (
                  <Link to={secondaryCta.to} className="btn btn-secondary btn-lg">
                    {secondaryCta.label}
                  </Link>
                ))}
            </div>
          )}
          {belowCta && (
            <div
              style={{
                color: "var(--text-muted)",
                fontSize: "var(--fs-sm)",
                marginTop: "var(--sp-1)",
              }}
            >
              {belowCta}
            </div>
          )}
        </div>

        {illustration && (
          <div style={{ marginTop: "var(--sp-8)" }}>{illustration}</div>
        )}
      </Container>
    </div>
  );
}
