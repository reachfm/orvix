/**
 * Icon wrapper that picks the right Lucide icon by name and
 * applies consistent styling. Pages can use the named exports
 * from lucide-react directly when they need a specific icon.
 */
import type { LucideIcon } from "lucide-react";

interface IconProps {
  icon: LucideIcon;
  size?: number;
  color?: string;
  "aria-hidden"?: boolean;
}

export default function Icon({
  icon: IconComponent,
  size = 18,
  color,
  "aria-hidden": ariaHidden = true,
}: IconProps) {
  return (
    <IconComponent
      size={size}
      color={color}
      aria-hidden={ariaHidden}
      focusable="false"
    />
  );
}
