import type { StatusState } from "./StatusTile";

interface StatusBannerProps {
  state: StatusState;
  message: string;
}

const STATE_LABEL: Record<StatusState, string> = {
  operational: "All systems operational",
  degraded: "Some systems are degraded",
  outage: "Active outage",
  maintenance: "Scheduled maintenance in progress",
};

const STATE_COLOR: Record<StatusState, string> = {
  operational: "var(--success)",
  degraded: "var(--warning)",
  outage: "var(--danger)",
  maintenance: "var(--info)",
};

export default function StatusBanner({ state, message }: StatusBannerProps) {
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        background: "var(--bg-canvas)",
        border: `1px solid ${STATE_COLOR[state]}`,
        borderRadius: "var(--r-md)",
        padding: "var(--sp-4) var(--sp-5)",
        display: "flex",
        flexDirection: "column",
        gap: "var(--sp-1)",
      }}
    >
      <p
        style={{
          margin: 0,
          fontWeight: 700,
          color: "var(--text-primary)",
          fontSize: "var(--fs-sm)",
        }}
      >
        {STATE_LABEL[state]}
      </p>
      <p
        style={{
          margin: 0,
          fontSize: "var(--fs-sm)",
          color: "var(--text-secondary)",
        }}
      >
        {message}
      </p>
    </div>
  );
}
