import { CheckCircle2, AlertTriangle, XCircle, MinusCircle } from "lucide-react";

export type StatusState = "operational" | "degraded" | "outage" | "maintenance";

interface StatusTileProps {
  service: string;
  state: StatusState;
  note?: string;
}

const STATE_META: Record<
  StatusState,
  { label: string; color: string; Icon: typeof CheckCircle2 }
> = {
  operational: {
    label: "Operational",
    color: "var(--success)",
    Icon: CheckCircle2,
  },
  degraded: {
    label: "Degraded performance",
    color: "var(--warning)",
    Icon: AlertTriangle,
  },
  outage: {
    label: "Outage",
    color: "var(--danger)",
    Icon: XCircle,
  },
  maintenance: {
    label: "Scheduled maintenance",
    color: "var(--info)",
    Icon: MinusCircle,
  },
};

export default function StatusTile({ service, state, note }: StatusTileProps) {
  const meta = STATE_META[state];
  const Icon = meta.Icon;
  return (
    <li
      style={{
        display: "flex",
        alignItems: "center",
        gap: "var(--sp-4)",
        padding: "var(--sp-4) var(--sp-5)",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
      }}
    >
      <Icon
        size={20}
        aria-hidden="true"
        style={{ color: meta.color, flexShrink: 0 }}
      />
      <div style={{ flex: 1, minWidth: 0 }}>
        <p
          style={{
            fontWeight: 600,
            color: "var(--text-primary)",
            margin: 0,
            fontSize: "var(--fs-sm)",
          }}
        >
          {service}
        </p>
        {note && (
          <p
            style={{
              margin: 0,
              fontSize: "var(--fs-xs)",
              color: "var(--text-muted)",
            }}
          >
            {note}
          </p>
        )}
      </div>
      <span
        className="badge"
        style={{
          background: "transparent",
          color: meta.color,
          borderColor: meta.color,
        }}
      >
        {meta.label}
      </span>
    </li>
  );
}
