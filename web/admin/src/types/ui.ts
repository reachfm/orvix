export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ButtonSize = "sm" | "md" | "lg";
export type BadgeVariant = "success" | "warning" | "danger" | "info" | "neutral" | "teal" | "blue" | "purple";
export type BadgeSize = "sm" | "md";
export type StatusDotStatus = "success" | "warning" | "danger" | "neutral" | "checking";
export type ToastVariant = "success" | "warning" | "danger" | "info";

export interface ToastConfig {
  id: string;
  message: string;
  variant: ToastVariant;
  duration: number;
}

export interface PaginationState {
  page: number;
  pageSize: number;
  total: number;
}

export interface TableColumn<T> {
  key: string;
  label: string;
  width?: string;
  render?: (row: T) => React.ReactNode;
  sortable?: boolean;
}
