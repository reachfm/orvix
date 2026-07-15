import type { CSSProperties, ReactNode } from "react";

interface ContainerProps {
  children: ReactNode;
  /** Use "narrow" for prose/legal pages. */
  width?: "default" | "narrow" | "wide";
  className?: string;
  as?: "div" | "section" | "article" | "main" | "header" | "footer" | "nav" | "aside";
  style?: CSSProperties;
  id?: string;
  role?: string;
  "aria-label"?: string;
  "aria-labelledby"?: string;
}

export default function Container({
  children,
  width = "default",
  className,
  as: Tag = "div",
  style: extraStyle,
  id,
  role,
  "aria-label": ariaLabel,
  "aria-labelledby": ariaLabelledBy,
}: ContainerProps) {
  const maxWidth =
    width === "narrow"
      ? "var(--container-narrow)"
      : width === "wide"
        ? "1320px"
        : "var(--container-max)";

  const style: CSSProperties = {
    maxWidth,
    marginLeft: "auto",
    marginRight: "auto",
    paddingLeft: "var(--sp-5)",
    paddingRight: "var(--sp-5)",
    width: "100%",
    ...extraStyle,
  };

  return (
    <Tag
      id={id}
      role={role}
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      className={className}
      style={style}
    >
      {children}
    </Tag>
  );
}
