/**
 * Re-export design tokens for components that want to consume them
 * via JS (e.g. inline SVG). The values mirror src/styles/tokens.css
 * exactly; if you change one, change both.
 */

export const colors = {
  bgApp: "#0C0E12",
  bgCanvas: "#13161C",
  bgSurface: "#1A1E26",
  bgElevated: "#222736",
  bgRaised: "#2A2F3E",
  textPrimary: "#E8EAF0",
  textSecondary: "#B5BCC9",
  textMuted: "#8B92A8",
  textFaint: "#555D73",
  borderSubtle: "#1F2430",
  borderDefault: "#2A2F3E",
  borderStrong: "#3A4154",
  accent: "#5b9eff",
  accentStrong: "#7ab1ff",
  accentSoft: "#2A4A7A",
  accentGlow: "rgba(91, 158, 255, 0.18)",
  success: "#34D399",
  warning: "#FBBF24",
  danger: "#F87171",
  info: "#60A5FA",
} as const;
