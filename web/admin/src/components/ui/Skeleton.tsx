interface SkeletonProps {
  width?: string | number;
  height?: string | number;
  className?: string;
  variant?: "rect" | "text" | "circle";
}

export default function Skeleton({ width, height, className = "", variant = "rect" }: SkeletonProps) {
  const style: React.CSSProperties = {};
  if (width) style.width = typeof width === "number" ? `${width}px` : width;
  if (height) style.height = typeof height === "number" ? `${height}px` : height;
  const cls = ["orvix-skeleton", variant === "circle" ? "rounded-full" : variant === "text" ? "h-4 rounded" : "", className].filter(Boolean).join(" ");
  return <div className={cls} style={style} />;
}
