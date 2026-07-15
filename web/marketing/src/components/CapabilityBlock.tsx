import type { LucideIcon } from "lucide-react";
import { Link } from "react-router-dom";

interface CapabilityBlockProps {
  icon: LucideIcon;
  title: string;
  body: string;
  href?: string;
  hrefLabel?: string;
}

export default function CapabilityBlock({
  icon: Icon,
  title,
  body,
  href,
  hrefLabel = "Learn more",
}: CapabilityBlockProps) {
  return (
    <article className="card" aria-label={title}>
      <div
        style={{
          width: 40,
          height: 40,
          borderRadius: "var(--r-md)",
          background: "var(--accent-glow)",
          color: "var(--accent)",
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          marginBottom: "var(--sp-4)",
        }}
        aria-hidden="true"
      >
        <Icon size={20} />
      </div>
      <h3
        style={{
          fontSize: "var(--fs-lg)",
          marginBottom: "var(--sp-2)",
          color: "var(--text-primary)",
        }}
      >
        {title}
      </h3>
      <p
        style={{
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.6,
          margin: 0,
        }}
      >
        {body}
      </p>
      {href && (
        <p style={{ marginTop: "var(--sp-4)" }}>
          <Link
            to={href}
            style={{
              color: "var(--accent)",
              fontWeight: 600,
              fontSize: "var(--fs-sm)",
              textDecoration: "none",
            }}
          >
            {hrefLabel} →
          </Link>
        </p>
      )}
    </article>
  );
}
