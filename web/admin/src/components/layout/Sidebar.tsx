import { PanelLeftClose, PanelLeftOpen, LogOut, Server } from "lucide-react";
import type { NavSection } from "../../types/navigation";

interface SidebarProps {
  sections: NavSection[];
  collapsed: boolean;
  activeTab: string;
  onNavigate: (id: string) => void;
  onCollapse: () => void;
  onClose?: () => void;
  userEmail?: string;
  userRole?: string;
  initials?: string;
  onLogout?: () => void;
  arabicLabels?: Record<string, string>;
  direction?: "ltr" | "rtl";
}

export default function Sidebar({
  sections, collapsed, activeTab, onNavigate, onCollapse, onClose,
  userEmail, userRole, initials: init, onLogout, arabicLabels, direction,
}: SidebarProps) {
  const labelText = (item: { id: string; label: string }) =>
    direction === "rtl" && arabicLabels?.[item.id] ? arabicLabels[item.id] : item.label;

  return (
    <aside
      className={`flex shrink-0 flex-col border-r border-[var(--border)] bg-[var(--bg-shell)] transition-all duration-300 ${
        collapsed ? "w-[72px]" : "w-[280px]"
      }`}
    >
      {/* Logo */}
      <div className="flex items-center gap-3 border-b border-[var(--border)] p-4">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-[var(--accent)]/30 bg-[var(--accent)]/10 text-[var(--accent)]">
          <Server size={22} />
        </div>
        {!collapsed && (
          <div className="min-w-0">
            <h1 className="truncate text-sm font-semibold text-[var(--text-primary)]">Orvix Admin</h1>
            <p className="text-xs text-[var(--text-muted)]">Enterprise control plane</p>
          </div>
        )}
        {onClose && (
          <button onClick={onClose} className="ms-auto lg:hidden p-1.5 text-[var(--text-muted)] hover:text-[var(--text-primary)]" aria-label="Close sidebar">
            <PanelLeftClose size={18} />
          </button>
        )}
      </div>

      {/* Collapse toggle */}
      <div className="hidden lg:block border-b border-[var(--border)] p-3">
        <button onClick={onCollapse} className="flex w-full items-center justify-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2 text-xs text-[var(--text-secondary)] hover:border-[var(--accent)] hover:text-[var(--text-primary)]">
          {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
          {!collapsed && <span>Collapse</span>}
        </button>
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto px-3 py-4" aria-label="Primary">
        {sections.map((section) => (
          <div key={section.label} className="mb-6">
            {!collapsed && (
              <p className="mb-2 px-3 text-[11px] font-semibold uppercase tracking-[0.14em] text-[var(--text-muted)]">
                {section.label}
              </p>
            )}
            <div className="space-y-0.5">
              {section.items.map((item) => {
                const Icon = item.icon;
                const active = activeTab === item.id;
                return (
                  <button
                    key={item.id}
                    onClick={() => { onNavigate(item.id); }}
                    title={collapsed ? labelText(item) : undefined}
                    className={`group relative flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-sm transition-colors ${
                      collapsed ? "justify-center" : ""
                    } ${
                      active
                        ? "bg-[var(--bg-elevated)] text-[var(--text-primary)]"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-surface)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    {active && <span className="absolute inset-y-2 start-0 w-0.5 rounded-full bg-[var(--accent)]" />}
                    <Icon size={18} className={active ? "text-[var(--accent)]" : ""} />
                    {!collapsed && <span className="truncate flex-1 text-left">{labelText(item)}</span>}
                    {!collapsed && item.badge !== undefined && (
                      <span className={`orvix-badge orvix-badge-sm orvix-badge-${item.badgeVariant || "teal"}`}>
                        {item.badge}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      {/* User card */}
      <div className="border-t border-[var(--border)] p-3">
        {!collapsed && userEmail && (
          <div className="mb-3 flex items-center gap-3 rounded-lg bg-[var(--bg-surface)] p-3">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-[var(--accent)]/15 text-xs font-bold text-[var(--accent)]">
              {init || "OX"}
            </div>
            <div className="min-w-0">
              <p className="truncate text-xs font-medium text-[var(--text-primary)] orvix-ltr-value">{userEmail}</p>
              <p className="text-[11px] capitalize text-[var(--text-muted)]">{userRole || "user"}</p>
            </div>
          </div>
        )}
        {onLogout && (
          <button onClick={onLogout} aria-label="Logout" className={`flex w-full items-center gap-2 rounded-lg px-3 py-2 text-sm text-[var(--text-secondary)] hover:bg-[var(--bg-elevated)] hover:text-[var(--text-primary)] ${collapsed ? "justify-center" : ""}`}>
            <LogOut size={18} aria-hidden="true" /> {!collapsed && <span>Logout</span>}
          </button>
        )}
      </div>
    </aside>
  );
}
