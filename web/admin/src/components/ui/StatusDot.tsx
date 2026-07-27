import type { StatusDotStatus } from "../../types/ui";

interface StatusDotProps {
  status: StatusDotStatus;
  className?: string;
}

export default function StatusDot({ status, className = "" }: StatusDotProps) {
  return <span className={`orvix-status-dot orvix-status-dot-${status} ${className}`} />;
}
