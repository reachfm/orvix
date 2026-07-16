interface Step {
  title: string;
  body: string;
}

interface StepStripProps {
  steps: Step[];
}

export default function StepStrip({ steps }: StepStripProps) {
  return (
    <ol
      style={{
        listStyle: "none",
        padding: 0,
        margin: 0,
        display: "grid",
        gridTemplateColumns: `repeat(${steps.length}, 1fr)`,
        gap: "var(--sp-4)",
        counterReset: "step",
      }}
      className="step-strip"
    >
      {steps.map((step, idx) => (
        <li
          key={idx}
          style={{
            background: "var(--bg-canvas)",
            border: "1px solid var(--border-default)",
            borderRadius: "var(--r-lg)",
            padding: "var(--sp-5)",
            display: "flex",
            flexDirection: "column",
            gap: "var(--sp-3)",
            position: "relative",
          }}
        >
          <span
            aria-hidden="true"
            style={{
              display: "inline-flex",
              alignItems: "center",
              justifyContent: "center",
              width: 32,
              height: 32,
              borderRadius: 999,
              background: "var(--accent-glow)",
              color: "var(--accent)",
              fontWeight: 700,
              fontSize: "var(--fs-sm)",
            }}
          >
            {idx + 1}
          </span>
          <h3
            style={{
              margin: 0,
              fontSize: "var(--fs-lg)",
              color: "var(--text-primary)",
            }}
          >
            {step.title}
          </h3>
          <p
            style={{
              margin: 0,
              color: "var(--text-secondary)",
              fontSize: "var(--fs-sm)",
              lineHeight: 1.6,
            }}
          >
            {step.body}
          </p>
        </li>
      ))}

      <style>{`
        @media (max-width: 880px) {
          .step-strip {
            grid-template-columns: 1fr !important;
          }
        }
      `}</style>
    </ol>
  );
}
