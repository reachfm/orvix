import { forwardRef, ButtonHTMLAttributes } from "react";
import type { ButtonVariant, ButtonSize } from "../../types/ui";

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  iconLeft?: React.ReactNode;
  iconRight?: React.ReactNode;
  fullWidth?: boolean;
}

const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = "primary", size = "md", loading, disabled, iconLeft, iconRight, fullWidth, children, className = "", ...rest }, ref) => {
    const cls = [
      "orvix-btn",
      `orvix-btn-${variant}`,
      size === "sm" ? "orvix-btn-sm" : size === "lg" ? "orvix-btn-lg" : "orvix-btn-md",
      fullWidth ? "orvix-btn-full" : "",
      className,
    ].filter(Boolean).join(" ");
    return (
      <button ref={ref} className={cls} disabled={disabled || loading} {...rest}>
        {loading && <span className="orvix-btn-spinner" />}
        {!loading && iconLeft}
        {children}
        {!loading && iconRight}
      </button>
    );
  }
);
Button.displayName = "Button";
export default Button;
