import { Link } from "react-router-dom";
import { ArrowUpRight } from "lucide-react";

interface DocLinkCardProps {
  title: string;
  body: string;
  href: string;
  external?: boolean;
}

export default function DocLinkCard({ title, body, href, external }: DocLinkCardProps) {
  const content = (
    <>
      <h3
        style={{
          fontSize: "var(--fs-base)",
          margin: 0,
          color: "var(--text-primary)",
          display: "inline-flex",
          alignItems: "center",
          gap: "var(--sp-2)",
        }}
      >
        {title}
        {external && (
          <ArrowUpRight
            size={14}
            aria-hidden="true"
            style={{ color: "var(--text-faint)" }}
          />
        )}
      </h3>
      <p
        style={{
          margin: "var(--sp-2) 0 0",
          fontSize: "var(--fs-sm)",
          color: "var(--text-secondary)",
          lineHeight: 1.6,
        }}
      >
        {body}
      </p>
    </>
  );

  const cardClass = "card";

  if (external) {
    return (
      <a
        href={href}
        rel="noopener"
        className={cardClass}
        style={{
          textDecoration: "none",
          color: "inherit",
          display: "block",
        }}
      >
        {content}
      </a>
    );
  }
  return (
    <Link
      to={href}
      className={cardClass}
      style={{
        textDecoration: "none",
        color: "inherit",
        display: "block",
      }}
    >
      {content}
    </Link>
  );
}
