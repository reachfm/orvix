import { useState } from "react";
import { ChevronDown } from "lucide-react";

export interface FaqItem {
  q: string;
  a: string;
}

interface FAQProps {
  items: FaqItem[];
  title?: string;
}

export default function FAQ({ items, title = "Frequently asked questions" }: FAQProps) {
  return (
    <div>
      <h2
        style={{
          fontSize: "var(--fs-2xl)",
          marginBottom: "var(--sp-5)",
          color: "var(--text-primary)",
        }}
      >
        {title}
      </h2>
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
        {items.map((item, idx) => (
          <FAQRow key={idx} item={item} />
        ))}
      </ul>
    </div>
  );
}

function FAQRow({ item }: { item: FaqItem }) {
  const [open, setOpen] = useState(false);
  return (
    <li
      style={{
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
        overflow: "hidden",
      }}
    >
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        style={{
          width: "100%",
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          gap: "var(--sp-3)",
          padding: "var(--sp-4) var(--sp-5)",
          background: "transparent",
          border: "none",
          color: "var(--text-primary)",
          fontSize: "var(--fs-base)",
          fontWeight: 600,
          textAlign: "left",
          cursor: "pointer",
        }}
      >
        <span>{item.q}</span>
        <ChevronDown
          size={18}
          aria-hidden="true"
          style={{
            flexShrink: 0,
            transform: open ? "rotate(180deg)" : "rotate(0deg)",
            transition: "transform var(--dur-2) var(--ease-out)",
            color: "var(--text-muted)",
          }}
        />
      </button>
      {open && (
        <div
          style={{
            padding: "0 var(--sp-5) var(--sp-4)",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-sm)",
            lineHeight: 1.7,
          }}
        >
          {item.a}
        </div>
      )}
    </li>
  );
}
