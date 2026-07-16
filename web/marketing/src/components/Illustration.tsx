import {
  Inbox,
  Send,
  Archive,
  Star,
  Settings,
  Plus,
  Paperclip,
  Trash2,
} from "lucide-react";
import { colors } from "../lib/design-tokens";

/**
 * Labelled SVG illustrations of the actual product surfaces.
 *
 * These render the Orvix webmail + admin surfaces in a clean,
 * low-fidelity style. Each illustration carries a small
 * "Illustration" label so it cannot be confused with a real
 * screenshot. When real screenshots become available they will
 * be added under src/assets/screens/ and the routes that consume
 * this module will swap to <img> tags.
 */

interface IllustrationProps {
  variant:
    | "inbox"
    | "compose"
    | "calendar"
    | "admin-dashboard"
    | "admin-domains"
    | "admin-mailboxes"
    | "admin-queue"
    | "api-explorer";
  label?: string;
  height?: number;
}

export default function Illustration({
  variant,
  label = "Illustration",
  height = 320,
}: IllustrationProps) {
  return (
    <figure
      style={{
        margin: 0,
        position: "relative",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-lg)",
        overflow: "hidden",
      }}
      aria-label={`${label} of the Orvix product`}
    >
      <div
        style={{
          position: "absolute",
          top: "var(--sp-2)",
          right: "var(--sp-2)",
          zIndex: 2,
          fontSize: 10,
          fontWeight: 700,
          textTransform: "uppercase",
          letterSpacing: "0.1em",
          background: "var(--bg-elevated)",
          color: "var(--text-faint)",
          padding: "2px 6px",
          borderRadius: "var(--r-sm)",
          border: "1px solid var(--border-default)",
        }}
      >
        Illustration
      </div>
      <div
        style={{
          width: "100%",
          height,
          display: "block",
        }}
      >
        {renderVariant(variant)}
      </div>
    </figure>
  );
}

function renderVariant(variant: IllustrationProps["variant"]) {
  switch (variant) {
    case "inbox":
      return <InboxScene />;
    case "compose":
      return <ComposeScene />;
    case "calendar":
      return <CalendarScene />;
    case "admin-dashboard":
      return <AdminDashboardScene />;
    case "admin-domains":
      return <AdminDomainsScene />;
    case "admin-mailboxes":
      return <AdminMailboxesScene />;
    case "admin-queue":
      return <AdminQueueScene />;
    case "api-explorer":
      return <ApiExplorerScene />;
  }
}

function Frame({ children }: { children: React.ReactNode }) {
  return (
    <svg
      viewBox="0 0 800 480"
      width="100%"
      height="100%"
      aria-hidden="true"
      focusable="false"
      style={{ display: "block" }}
    >
      {children}
    </svg>
  );
}

function WindowChrome({ title }: { title: string }) {
  return (
    <g>
      <rect x={0} y={0} width={800} height={36} fill={colors.bgApp} />
      <circle cx={18} cy={18} r={5} fill={colors.danger} />
      <circle cx={36} cy={18} r={5} fill={colors.warning} />
      <circle cx={54} cy={18} r={5} fill={colors.success} />
      <text
        x={400}
        y={22}
        textAnchor="middle"
        fontSize={12}
        fill={colors.textMuted}
        fontFamily="sans-serif"
      >
        {title}
      </text>
    </g>
  );
}

function InboxScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix Webmail — Inbox" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <rect x={0} y={36} width={180} height={444} fill={colors.bgApp} />
      <line
        x1={180}
        y1={36}
        x2={180}
        y2={480}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <line
        x1={180}
        y1={36}
        x2={800}
        y2={36}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      {/* Sidebar */}
      <NavRow y={70} icon="compose" label="Compose" accent />
      <NavRow y={102} icon="inbox" label="Inbox" badge="12" active />
      <NavRow y={134} icon="star" label="Starred" />
      <NavRow y={166} icon="send" label="Sent" />
      <NavRow y={198} icon="archive" label="Archive" />
      <NavRow y={230} icon="trash" label="Trash" />
      <NavRow y={410} icon="settings" label="Settings" />
      {/* List */}
      {Array.from({ length: 7 }).map((_, i) => (
        <ListRow key={i} y={70 + i * 48} read={i > 2} />
      ))}
    </Frame>
  );
}

function NavRow({
  y,
  icon,
  label,
  active,
  badge,
  accent,
}: {
  y: number;
  icon: string;
  label: string;
  active?: boolean;
  badge?: string;
  accent?: boolean;
}) {
  const x = 20;
  return (
    <g>
      {active && (
        <rect
          x={8}
          y={y - 12}
          width={164}
          height={28}
          rx={6}
          fill={colors.bgElevated}
        />
      )}
      <foreignObject x={x} y={y - 12} width={140} height={28}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            fontSize: 12,
            color: active ? colors.textPrimary : colors.textMuted,
            fontWeight: active ? 600 : 500,
            fontFamily: "sans-serif",
          }}
        >
          {icon === "compose" && (
            <Plus size={14} color={accent ? colors.accent : colors.textMuted} />
          )}
          {icon === "inbox" && <Inbox size={14} />}
          {icon === "star" && <Star size={14} />}
          {icon === "send" && <Send size={14} />}
          {icon === "archive" && <Archive size={14} />}
          {icon === "trash" && <Trash2 size={14} />}
          {icon === "settings" && <Settings size={14} />}
          <span style={{ flex: 1 }}>{label}</span>
          {badge && (
            <span
              style={{
                fontSize: 10,
                background: colors.accent,
                color: colors.bgApp,
                padding: "1px 6px",
                borderRadius: 999,
                fontWeight: 700,
              }}
            >
              {badge}
            </span>
          )}
        </div>
      </foreignObject>
    </g>
  );
}

function ListRow({ y, read }: { y: number; read: boolean }) {
  return (
    <g>
      <rect
        x={200}
        y={y}
        width={580}
        height={44}
        fill={colors.bgSurface}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <circle
        cx={220}
        cy={y + 22}
        r={10}
        fill={colors.bgElevated}
        stroke={colors.borderDefault}
      />
      <text
        x={220}
        y={y + 26}
        textAnchor="middle"
        fontSize={11}
        fill={colors.textSecondary}
        fontFamily="sans-serif"
        fontWeight={read ? 400 : 700}
      >
        {String.fromCharCode(65 + ((y / 48) % 20))}
      </text>
      <text
        x={244}
        y={y + 20}
        fontSize={12}
        fontFamily="sans-serif"
        fontWeight={read ? 500 : 700}
        fill={read ? colors.textMuted : colors.textPrimary}
      >
        {read
          ? "Quarterly review notes from last week"
          : "Action required: DKIM key rotation for example.com"}
      </text>
      <text
        x={244}
        y={y + 36}
        fontSize={11}
        fontFamily="sans-serif"
        fill={colors.textMuted}
      >
        {read
          ? "Hey team — here are the notes…"
          : "Hi — the DKIM key for example.com expires in 14 days. Please rotate…"}
      </text>
    </g>
  );
}

function ComposeScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix Webmail — New message" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgApp} />
      <rect
        x={120}
        y={80}
        width={560}
        height={360}
        rx={12}
        fill={colors.bgSurface}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <text
        x={140}
        y={110}
        fontFamily="sans-serif"
        fontSize={11}
        fill={colors.textFaint}
      >
        FROM
      </text>
      <text
        x={140}
        y={130}
        fontFamily="sans-serif"
        fontSize={13}
        fill={colors.textPrimary}
      >
        jane@acme.example
      </text>
      <line
        x1={140}
        y1={146}
        x2={660}
        y2={146}
        stroke={colors.borderSubtle}
        strokeWidth={1}
      />
      <text
        x={140}
        y={170}
        fontFamily="sans-serif"
        fontSize={11}
        fill={colors.textFaint}
      >
        TO
      </text>
      <text
        x={140}
        y={190}
        fontFamily="sans-serif"
        fontSize={13}
        fill={colors.textPrimary}
      >
        team@acme.example
      </text>
      <line
        x1={140}
        y1={206}
        x2={660}
        y2={206}
        stroke={colors.borderSubtle}
        strokeWidth={1}
      />
      <text
        x={140}
        y={230}
        fontFamily="sans-serif"
        fontSize={11}
        fill={colors.textFaint}
      >
        SUBJECT
      </text>
      <text
        x={140}
        y={250}
        fontFamily="sans-serif"
        fontSize={14}
        fill={colors.textPrimary}
        fontWeight={600}
      >
        Q3 launch plan
      </text>
      <line
        x1={140}
        y1={266}
        x2={660}
        y2={266}
        stroke={colors.borderSubtle}
        strokeWidth={1}
      />
      <text
        x={140}
        y={296}
        fontFamily="sans-serif"
        fontSize={12}
        fill={colors.textSecondary}
      >
        Hi team,
      </text>
      <text
        x={140}
        y={318}
        fontFamily="sans-serif"
        fontSize={12}
        fill={colors.textSecondary}
      >
        Sharing the launch plan for Q3. We need sign-off by Friday so we can
      </text>
      <text
        x={140}
        y={338}
        fontFamily="sans-serif"
        fontSize={12}
        fill={colors.textSecondary}
      >
        start the rollout on the 15th. Let me know if you have any questions.
      </text>
      <text
        x={140}
        y={360}
        fontFamily="sans-serif"
        fontSize={12}
        fill={colors.textSecondary}
      >
        — Jane
      </text>
      <rect
        x={600}
        y={400}
        width={70}
        height={28}
        rx={6}
        fill={colors.accent}
      />
      <text
        x={635}
        y={418}
        textAnchor="middle"
        fontFamily="sans-serif"
        fontSize={12}
        fontWeight={700}
        fill={colors.bgApp}
      >
        Send
      </text>
      <foreignObject x={140} y={400} width={300} height={28}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            color: colors.textMuted,
            fontSize: 11,
            fontFamily: "sans-serif",
          }}
        >
          <Paperclip size={12} /> Attach files
        </div>
      </foreignObject>
    </Frame>
  );
}

function CalendarScene() {
  const days = ["Mon", "Tue", "Wed", "Thu", "Fri"];
  return (
    <Frame>
      <WindowChrome title="Orvix Webmail — Calendar" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <text
        x={32}
        y={70}
        fontFamily="sans-serif"
        fontSize={14}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        This week
      </text>
      {days.map((d, i) => (
        <g key={d}>
          <rect
            x={32 + i * 148}
            y={90}
            width={140}
            height={360}
            rx={8}
            fill={colors.bgSurface}
            stroke={colors.borderDefault}
            strokeWidth={1}
          />
          <text
            x={32 + i * 148 + 12}
            y={112}
            fontFamily="sans-serif"
            fontSize={12}
            fontWeight={600}
            fill={colors.textPrimary}
          >
            {d}
          </text>
          <text
            x={32 + i * 148 + 12}
            y={130}
            fontFamily="sans-serif"
            fontSize={20}
            fontWeight={700}
            fill={colors.textPrimary}
          >
            {10 + i}
          </text>
          <rect
            x={32 + i * 148 + 12}
            y={150 + (i % 3) * 60}
            width={116}
            height={48}
            rx={6}
            fill={colors.accentGlow}
            stroke={colors.accent}
            strokeWidth={1}
          />
          <text
            x={32 + i * 148 + 20}
            y={170 + (i % 3) * 60}
            fontFamily="sans-serif"
            fontSize={11}
            fontWeight={600}
            fill={colors.accent}
          >
            {(i + 1) * 2}:00 — Sync
          </text>
          <text
            x={32 + i * 148 + 20}
            y={186 + (i % 3) * 60}
            fontFamily="sans-serif"
            fontSize={10}
            fill={colors.textMuted}
          >
            Team room
          </text>
        </g>
      ))}
    </Frame>
  );
}

function AdminDashboardScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix Admin — Dashboard" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <text
        x={32}
        y={70}
        fontFamily="sans-serif"
        fontSize={14}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Today
      </text>
      {[
        { label: "Delivered", value: "12,489", color: colors.success },
        { label: "Deferred", value: "23", color: colors.warning },
        { label: "Bounced", value: "4", color: colors.danger },
        { label: "Inbound (last 1h)", value: "318", color: colors.accent },
      ].map((kpi, i) => (
        <g key={kpi.label}>
          <rect
            x={32 + i * 188}
            y={88}
            width={172}
            height={86}
            rx={8}
            fill={colors.bgSurface}
            stroke={colors.borderDefault}
            strokeWidth={1}
          />
          <text
            x={32 + i * 188 + 14}
            y={112}
            fontFamily="sans-serif"
            fontSize={11}
            fill={colors.textMuted}
          >
            {kpi.label}
          </text>
          <text
            x={32 + i * 188 + 14}
            y={148}
            fontFamily="sans-serif"
            fontSize={22}
            fontWeight={700}
            fill={kpi.color}
          >
            {kpi.value}
          </text>
        </g>
      ))}
      <text
        x={32}
        y={210}
        fontFamily="sans-serif"
        fontSize={12}
        fontWeight={600}
        fill={colors.textPrimary}
      >
        Mail flow (last 24h)
      </text>
      {Array.from({ length: 24 }).map((_, i) => {
        const h = 60 + ((i * 13) % 60);
        return (
          <rect
            key={i}
            x={32 + i * 30}
            y={310 - h}
            width={20}
            height={h}
            rx={3}
            fill={colors.accent}
            opacity={0.55}
          />
        );
      })}
      <line
        x1={32}
        y1={310}
        x2={752}
        y2={310}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <text
        x={32}
        y={340}
        fontFamily="sans-serif"
        fontSize={10}
        fill={colors.textFaint}
      >
        00:00
      </text>
      <text
        x={752}
        y={340}
        textAnchor="end"
        fontFamily="sans-serif"
        fontSize={10}
        fill={colors.textFaint}
      >
        23:00
      </text>
      <text
        x={32}
        y={380}
        fontFamily="sans-serif"
        fontSize={12}
        fontWeight={600}
        fill={colors.textPrimary}
      >
        Recent audit events
      </text>
      {[
        "alice@acme.example logged in",
        "DKIM key rotated for acme.example",
        "Mailbox bob@acme.example suspended",
        "API key orv_live_… created by admin@acme.example",
      ].map((evt, i) => (
        <g key={evt}>
          <text
            x={32}
            y={404 + i * 18}
            fontFamily="monospace"
            fontSize={11}
            fill={colors.textSecondary}
          >
            · {evt}
          </text>
        </g>
      ))}
    </Frame>
  );
}

function AdminDomainsScene() {
  const rows = [
    { d: "acme.example", mx: "ok", dkim: "ok", spf: "ok", dmarc: "warn" },
    { d: "beta.example", mx: "ok", dkim: "ok", spf: "ok", dmarc: "ok" },
    { d: "staging.example", mx: "warn", dkim: "ok", spf: "ok", dmarc: "ok" },
  ];
  return (
    <Frame>
      <WindowChrome title="Orvix Admin — Domains" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <text
        x={32}
        y={70}
        fontFamily="sans-serif"
        fontSize={14}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Domains
      </text>
      <text
        x={32}
        y={88}
        fontFamily="sans-serif"
        fontSize={11}
        fill={colors.textMuted}
      >
        3 of 10 used (Business plan)
      </text>
      {rows.map((r, i) => (
        <g key={r.d}>
          <rect
            x={32}
            y={108 + i * 80}
            width={736}
            height={70}
            rx={8}
            fill={colors.bgSurface}
            stroke={colors.borderDefault}
            strokeWidth={1}
          />
          <text
            x={48}
            y={134 + i * 80}
            fontFamily="sans-serif"
            fontSize={14}
            fontWeight={600}
            fill={colors.textPrimary}
          >
            {r.d}
          </text>
          <Pill x={48 + 130} y={124 + i * 80} label="MX" state={r.mx} />
          <Pill x={48 + 220} y={124 + i * 80} label="DKIM" state={r.dkim} />
          <Pill x={48 + 320} y={124 + i * 80} label="SPF" state={r.spf} />
          <Pill x={48 + 420} y={124 + i * 80} label="DMARC" state={r.dmarc} />
          <text
            x={48}
            y={160 + i * 80}
            fontFamily="monospace"
            fontSize={11}
            fill={colors.textMuted}
          >
            10 mailboxes · 2.1 GB used · last activity 12 min ago
          </text>
        </g>
      ))}
    </Frame>
  );
}

function Pill({
  x,
  y,
  label,
  state,
}: {
  x: number;
  y: number;
  label: string;
  state: string;
}) {
  const color =
    state === "ok"
      ? colors.success
      : state === "warn"
        ? colors.warning
        : colors.danger;
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={80}
        height={26}
        rx={13}
        fill={colors.bgApp}
        stroke={color}
        strokeWidth={1}
      />
      <circle cx={x + 14} cy={y + 13} r={4} fill={color} />
      <text
        x={x + 26}
        y={y + 17}
        fontFamily="sans-serif"
        fontSize={11}
        fontWeight={600}
        fill={colors.textPrimary}
      >
        {label}
      </text>
    </g>
  );
}

function AdminMailboxesScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix Admin — Mailboxes" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <text
        x={32}
        y={70}
        fontFamily="sans-serif"
        fontSize={14}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Mailboxes (10)
      </text>
      {[
        "admin@acme.example",
        "alice@acme.example",
        "bob@acme.example",
        "carol@acme.example",
        "dave@acme.example",
      ].map((m, i) => (
        <g key={m}>
          <rect
            x={32}
            y={92 + i * 56}
            width={736}
            height={46}
            rx={8}
            fill={colors.bgSurface}
            stroke={colors.borderDefault}
            strokeWidth={1}
          />
          <circle
            cx={56}
            cy={115 + i * 56}
            r={10}
            fill={colors.accentGlow}
            stroke={colors.accent}
            strokeWidth={1}
          />
          <text
            x={56}
            y={119 + i * 56}
            textAnchor="middle"
            fontFamily="sans-serif"
            fontSize={11}
            fontWeight={700}
            fill={colors.accent}
          >
            {m[0].toUpperCase()}
          </text>
          <text
            x={80}
            y={119 + i * 56}
            fontFamily="monospace"
            fontSize={12}
            fill={colors.textPrimary}
          >
            {m}
          </text>
          <text
            x={80}
            y={134 + i * 56}
            fontFamily="sans-serif"
            fontSize={10}
            fill={colors.textMuted}
          >
            312 MB · 24 messages today
          </text>
          <text
            x={750}
            y={119 + i * 56}
            textAnchor="end"
            fontFamily="sans-serif"
            fontSize={10}
            fill={colors.textMuted}
          >
            active
          </text>
        </g>
      ))}
    </Frame>
  );
}

function AdminQueueScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix Admin — Queue" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <text
        x={32}
        y={70}
        fontFamily="sans-serif"
        fontSize={14}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Queue
      </text>
      {[
        {
          from: "alice@acme.example",
          to: "x@external.test",
          state: "delivered",
          code: "250",
        },
        {
          from: "team@acme.example",
          to: "y@external.test",
          state: "deferred",
          code: "421",
        },
        {
          from: "bob@acme.example",
          to: "z@external.test",
          state: "delivered",
          code: "250",
        },
        {
          from: "news@acme.example",
          to: "list@external.test",
          state: "bounced",
          code: "550",
        },
        {
          from: "carol@acme.example",
          to: "q@external.test",
          state: "delivered",
          code: "250",
        },
      ].map((row, i) => {
        const color =
          row.state === "delivered"
            ? colors.success
            : row.state === "deferred"
              ? colors.warning
              : colors.danger;
        return (
          <g key={i}>
            <rect
              x={32}
              y={92 + i * 64}
              width={736}
              height={54}
              rx={8}
              fill={colors.bgSurface}
              stroke={colors.borderDefault}
              strokeWidth={1}
            />
            <rect x={32} y={92 + i * 64} width={4} height={54} fill={color} />
            <text
              x={56}
              y={114 + i * 64}
              fontFamily="monospace"
              fontSize={12}
              fill={colors.textPrimary}
            >
              {row.from} → {row.to}
            </text>
            <text
              x={56}
              y={132 + i * 64}
              fontFamily="sans-serif"
              fontSize={10}
              fill={colors.textMuted}
            >
              {row.state} · SMTP {row.code} · 1.2s
            </text>
            <text
              x={750}
              y={114 + i * 64}
              textAnchor="end"
              fontFamily="monospace"
              fontSize={11}
              fontWeight={700}
              fill={color}
            >
              {row.state.toUpperCase()}
            </text>
          </g>
        );
      })}
    </Frame>
  );
}

function ApiExplorerScene() {
  return (
    <Frame>
      <WindowChrome title="Orvix API — Explorer" />
      <rect x={0} y={36} width={800} height={444} fill={colors.bgCanvas} />
      <rect
        x={32}
        y={60}
        width={350}
        height={400}
        rx={8}
        fill={colors.bgSurface}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <text
        x={48}
        y={88}
        fontFamily="sans-serif"
        fontSize={12}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Endpoints
      </text>
      {[
        "GET /api/v1/health",
        "GET /api/v1/billing/plans",
        "POST /api/v1/auth/login",
        "GET /api/v1/me",
        "GET /api/v1/enterprise/domains",
        "GET /api/v1/enterprise/mailboxes",
        "POST /api/v1/enterprise/domains",
        "POST /api/v1/enterprise/mailboxes",
      ].map((e, i) => (
        <text
          key={e}
          x={48}
          y={112 + i * 28}
          fontFamily="monospace"
          fontSize={11}
          fill={i % 2 === 0 ? colors.accent : colors.textSecondary}
        >
          {e}
        </text>
      ))}
      <rect
        x={400}
        y={60}
        width={368}
        height={400}
        rx={8}
        fill={colors.bgSurface}
        stroke={colors.borderDefault}
        strokeWidth={1}
      />
      <text
        x={416}
        y={88}
        fontFamily="sans-serif"
        fontSize={12}
        fontWeight={700}
        fill={colors.textPrimary}
      >
        Response · 200
      </text>
      <text
        x={416}
        y={110}
        fontFamily="monospace"
        fontSize={11}
        fill={colors.textMuted}
      >
        {`{
  "id": "msg_01HXYZ…",
  "from": "alice@acme.example",
  "to": ["team@acme.example"],
  "subject": "Q3 launch plan",
  "status": "queued",
  "created_at": "2026-07-15T18:24:01Z"
}`}
      </text>
    </Frame>
  );
}

// (All variants rendered; no further exports needed.)
