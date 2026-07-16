interface LogoProps {
  size?: number;
}

/**
 * Orvix wordmark + envelope glyph. Used in the top nav and footer.
 * Built as inline SVG so it picks up the parent's text color
 * and stays sharp at any DPI.
 */
export default function Logo({ size = 32 }: LogoProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 64 64"
      fill="none"
      aria-hidden="true"
      focusable="false"
    >
      <rect
        x="2"
        y="2"
        width="60"
        height="60"
        rx="14"
        fill="var(--bg-elevated)"
        stroke="var(--accent)"
        strokeWidth="2"
      />
      <path
        d="M16 22 L32 36 L48 22"
        stroke="var(--accent)"
        strokeWidth="3"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <rect
        x="14"
        y="20"
        width="36"
        height="24"
        rx="4"
        stroke="var(--accent)"
        strokeWidth="3"
        fill="none"
      />
    </svg>
  );
}
