import type { CSSProperties, ReactNode } from "react";
import Container from "./Container";

interface SectionProps {
  children: ReactNode;
  /** Optional eyebrow text shown above the heading. */
  eyebrow?: string;
  heading?: ReactNode;
  lede?: ReactNode;
  /** Center-align the heading block. */
  centered?: boolean;
  /** Add a soft top-and-bottom border. */
  bordered?: boolean;
  /** Use the alt background colour. */
  alt?: boolean;
  /** Compact vertical padding. */
  tight?: boolean;
  id?: string;
}

export default function Section({
  children,
  eyebrow,
  heading,
  lede,
  centered = false,
  bordered = false,
  alt = false,
  tight = false,
  id,
}: SectionProps) {
  const wrapperStyle: CSSProperties = {
    padding: tight ? "var(--sp-6) 0" : "var(--sp-8) 0",
    background: alt ? "var(--bg-canvas)" : "transparent",
    borderTop: bordered ? "1px solid var(--border-subtle)" : "none",
    borderBottom: bordered ? "1px solid var(--border-subtle)" : "none",
  };

  return (
    <section id={id} style={wrapperStyle}>
      <Container>
        {(eyebrow || heading || lede) && (
          <header
            style={{
              textAlign: centered ? "center" : "left",
              marginBottom: "var(--sp-6)",
              maxWidth: centered ? "100%" : "60ch",
              marginLeft: centered ? "auto" : undefined,
              marginRight: centered ? "auto" : undefined,
            }}
          >
            {eyebrow && <span className="eyebrow">{eyebrow}</span>}
            {heading && (
              <h2
                style={{
                  fontSize: "var(--fs-3xl)",
                  margin: 0,
                  lineHeight: 1.15,
                  color: "var(--text-primary)",
                }}
              >
                {heading}
              </h2>
            )}
            {lede && (
              <p
                className="lede"
                style={{
                  marginTop: "var(--sp-3)",
                  marginLeft: centered ? "auto" : 0,
                  marginRight: centered ? "auto" : 0,
                }}
              >
                {lede}
              </p>
            )}
          </header>
        )}
        {children}
      </Container>
    </section>
  );
}
