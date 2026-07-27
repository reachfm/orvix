import { LucideIcon } from "lucide-react";

export interface NavItem {
  id: string;
  label: string;
  icon: LucideIcon;
  description?: string;
  badge?: string | number;
  badgeVariant?: "success" | "warning" | "danger" | "info" | "neutral" | "teal";
}

export interface NavSection {
  label: string;
  items: NavItem[];
}
