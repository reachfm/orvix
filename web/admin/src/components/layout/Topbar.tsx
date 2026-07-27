import { Menu, Languages, Search, Sun, Moon } from "lucide-react";

interface TopbarProps {
  title: string;
  subtitle?: string;
  direction: "ltr" | "rtl";
  onToggleDirection: () => void;
  onOpenSidebar: () => void;
  theme?: "dark" | "light";
  onToggleTheme?: () => void;
}

export default function Topbar({ title, subtitle, direction, onToggleDirection, onOpenSidebar, theme, onToggleTheme }: TopbarProps) {
  return (
    <header className="border-b border-[var(--border)] bg-[rgba(7,11,18,0.82)] backdrop-blur-xl">
      <div className="flex items-center gap-3 px-4 py-3 lg:px-6">
        <button
          onClick={onOpenSidebar}
          className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-2 text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
          aria-label="Open navigation"
        >
          <Menu size={20} />
        </button>

        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-lg font-semibold text-[var(--text-primary)]">{title}</h2>
            <span className="rounded-full border border-[var(--accent)]/25 bg-[var(--accent)]/10 px-2 py-0.5 text-[11px] font-medium text-[var(--accent)]">
              Live API
            </span>
          </div>
          {subtitle && <p className="mt-0.5 text-sm text-[var(--text-secondary)]">{subtitle}</p>}
        </div>

        <div className="hidden items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 text-sm text-[var(--text-muted)] md:flex min-w-[180px]">
          <Search size={16} />
          <span>Use page controls</span>
        </div>

        {onToggleTheme && (
          <button onClick={onToggleTheme} className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-2 text-[var(--text-secondary)] hover:text-[var(--text-primary)]" aria-label="Toggle theme">
            {theme === "light" ? <Moon size={16} /> : <Sun size={16} />}
          </button>
        )}

        <button
          onClick={onToggleDirection}
          className="inline-flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 text-sm text-[var(--text-secondary)] hover:border-[var(--accent)] hover:text-[var(--text-primary)]"
          aria-label="Toggle language direction"
        >
          <Languages size={16} />
          <span className="hidden sm:inline">{direction === "ltr" ? "العربية" : "English"}</span>
        </button>
      </div>
    </header>
  );
}
