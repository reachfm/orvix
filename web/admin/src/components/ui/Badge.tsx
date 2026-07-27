import type { BadgeVariant, BadgeSize } from "../../types/ui";

interface BadgeProps {
  variant?: BadgeVariant;
  size?: BadgeSize;
  children: React.ReactNode;
  className?: string;
}

export default function Badge({ variant = "neutral", size = "md", children, className = "" }: BadgeProps) {
  return (
    <span className={`orvix-badge orvix-badge-${variant} orvix-badge-${size} ${className}`}>
      {children}
    </span>
  );
}
