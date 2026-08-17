import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Bell, LogOut, Search, Server } from "lucide-react";
import ThemeToggle from "../../../shared/theme/ThemeToggle";
import { useMonitoringHealthQuery } from "../monitoring/queries";

export interface PlatformShellTab<TId extends string> {
  id: TId;
  label: string;
  icon: React.ComponentType<{ size?: number; className?: string }>;
  section?: string;
}

interface PlatformShellProps<TId extends string> {
  tabs: PlatformShellTab<TId>[];
  currentTab: TId;
  onSelectTab: (id: TId) => void;
  userEmail: string;
  onLogout: () => void;
  children: ReactNode;
}

// PlatformShell is the premium sidebar + top-bar layout used ONLY for
// portal === "platform". It renders exactly the tab list App.tsx
// already filtered via PLATFORM_TAB_IDS — this component has no
// authorization logic of its own; App.tsx remains the sole source of
// truth for which tabs exist and which portal a user belongs to. See
// App.tsx's PLATFORM_TAB_IDS/ORGANIZATION_TAB_IDS comment for the
// ownership contract this must never weaken.
export default function PlatformShell<TId extends string>({
  tabs,
  currentTab,
  onSelectTab,
  userEmail,
  onLogout,
  children,
}: PlatformShellProps<TId>) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const searchInputRef = useRef<HTMLInputElement>(null);

  const healthQ = useMonitoringHealthQuery();
  const openAlerts = healthQ.data?.openAlerts ?? 0;

  const initials = useMemo(() => {
    const local = (userEmail.split("@")[0] || "").trim();
    if (!local) return "PA";
    const parts = local.split(/[._-]+/).filter(Boolean);
    if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
    return local.slice(0, 2).toUpperCase();
  }, [userEmail]);

  const searchResults = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return [];
    return tabs.filter((t) => t.label.toLowerCase().includes(q)).slice(0, 8);
  }, [searchQuery, tabs]);

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearchOpen(true);
        requestAnimationFrame(() => searchInputRef.current?.focus());
      }
      if (e.key === "Escape") {
        setSearchOpen(false);
        setSearchQuery("");
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  function selectSearchResult(id: TId) {
    onSelectTab(id);
    setSearchOpen(false);
    setSearchQuery("");
  }

  return (
    <div className="flex h-screen overflow-hidden">
      {/* Sidebar — collapsible on small viewports via sidebarOpen; a
          fixed overlay below lg, a static column at lg+. */}
      <aside
        data-sidebar-open={sidebarOpen}
        className={`fixed inset-y-0 left-0 z-40 w-64 bg-[var(--bg-surface)] border-r border-[var(--border)] flex flex-col transition-transform lg:static lg:translate-x-0 ${
          sidebarOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        <div className="h-16 px-5 border-b border-[var(--border)] flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-[var(--accent-soft)] flex items-center justify-center">
            <Server size={18} className="text-[var(--accent)]" />
          </div>
          <div className="flex-1 min-w-0">
            <h1 className="text-sm font-semibold text-[var(--text-primary)] leading-tight">Orvix</h1>
            <p className="text-[11px] text-[var(--text-muted)] leading-tight">Platform Admin</p>
          </div>
        </div>

        <nav className="flex-1 px-2.5 py-4 space-y-0.5 overflow-y-auto" aria-label="Platform navigation">
          {tabs.map((t) => {
            const Icon = t.icon;
            const active = currentTab === t.id;
            return (
              <div key={t.id}>
                {t.section && (
                  <div className="px-2.5 pt-5 pb-1.5 text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider first:pt-1">
                    {t.section}
                  </div>
                )}
                <button
                  onClick={() => {
                    onSelectTab(t.id);
                    setSidebarOpen(false);
                  }}
                  aria-current={active ? "page" : undefined}
                  className={`w-full flex items-center gap-2.5 px-2.5 py-2 rounded-[10px] text-[13px] font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)] ${
                    active
                      ? "bg-[var(--accent-soft)] text-[var(--accent)]"
                      : "text-[var(--text-secondary)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text-primary)]"
                  }`}
                >
                  <Icon size={17} className={active ? "text-[var(--accent)]" : ""} />
                  <span className="truncate">{t.label}</span>
                </button>
              </div>
            );
          })}
        </nav>

        <div className="p-3 border-t border-[var(--border)]">
          <div className="flex items-center gap-2.5 px-2 py-2 mb-1">
            <div className="w-8 h-8 rounded-full bg-[var(--accent-soft)] text-[var(--accent)] flex items-center justify-center text-xs font-semibold flex-shrink-0">
              {initials}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-xs font-medium text-[var(--text-primary)] truncate">{userEmail || "Platform Super Admin"}</p>
              <p className="text-[11px] text-[var(--text-muted)] truncate">Platform Super Admin</p>
            </div>
          </div>
          <button
            onClick={onLogout}
            className="w-full flex items-center gap-2.5 px-2.5 py-2 rounded-[10px] text-[13px] text-[var(--text-secondary)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text-primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)]"
          >
            <LogOut size={17} /> Sign out
          </button>
        </div>
      </aside>

      {sidebarOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/30 lg:hidden"
          onClick={() => setSidebarOpen(false)}
          aria-hidden="true"
        />
      )}

      <div className="flex-1 flex flex-col overflow-hidden">
        {/* Top bar */}
        <header className="relative z-50 h-16 flex-shrink-0 border-b border-[var(--border)] bg-[var(--bg-surface)] flex items-center gap-3 px-4 lg:px-6">
          <button
            onClick={() => setSidebarOpen((v) => !v)}
            className="lg:hidden p-2 -ml-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--bg-subtle)]"
            aria-label="Toggle sidebar"
          >
            <MenuIcon />
          </button>

          <div className="relative flex-1 max-w-md">
            <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[var(--text-muted)] pointer-events-none" />
            <input
              ref={searchInputRef}
              type="text"
              value={searchQuery}
              onChange={(e) => {
                setSearchQuery(e.target.value);
                setSearchOpen(true);
              }}
              onFocus={() => setSearchOpen(true)}
              onBlur={() => setTimeout(() => setSearchOpen(false), 150)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && searchResults.length > 0) {
                  selectSearchResult(searchResults[0].id);
                }
              }}
              placeholder="Search platform console…"
              aria-label="Search platform console destinations"
              className="w-full pl-9 pr-14 py-2 text-sm rounded-lg bg-[var(--bg-subtle)] border border-transparent text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:border-[var(--accent)] focus:bg-[var(--bg-surface)]"
            />
            <kbd className="hidden sm:flex absolute right-2.5 top-1/2 -translate-y-1/2 items-center gap-0.5 px-1.5 py-0.5 text-[10px] font-medium text-[var(--text-muted)] bg-[var(--bg-surface)] border border-[var(--border)] rounded">
              ⌘K
            </kbd>
            {searchOpen && searchResults.length > 0 && (
              <ul
                role="listbox"
                className="absolute z-50 mt-1.5 w-full bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl shadow-lg py-1.5 max-h-80 overflow-y-auto"
              >
                {searchResults.map((t) => {
                  const Icon = t.icon;
                  return (
                    <li key={t.id}>
                      <button
                        role="option"
                        aria-selected={false}
                        onMouseDown={(e) => e.preventDefault()}
                        onClick={() => selectSearchResult(t.id)}
                        className="w-full flex items-center gap-2.5 px-3.5 py-2 text-sm text-[var(--text-primary)] hover:bg-[var(--bg-subtle)] text-left"
                      >
                        <Icon size={15} className="text-[var(--text-muted)]" />
                        {t.label}
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>

          <div className="flex items-center gap-1.5 ml-auto">
            <button
              onClick={() => onSelectTab("health" as TId)}
              className="relative p-2 rounded-lg text-[var(--text-secondary)] hover:bg-[var(--bg-subtle)] hover:text-[var(--text-primary)] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)]"
              aria-label={openAlerts > 0 ? `${openAlerts} open alert${openAlerts === 1 ? "" : "s"} — view Health` : "No open alerts — view Health"}
              title="System Health"
            >
              <Bell size={18} />
              {openAlerts > 0 && (
                <span
                  className="absolute top-1 right-1 min-w-[16px] h-4 px-1 rounded-full bg-[var(--danger)] text-white text-[10px] font-semibold flex items-center justify-center"
                  aria-hidden="true"
                >
                  {openAlerts > 99 ? "99+" : openAlerts}
                </span>
              )}
            </button>
            <ThemeToggle compact />
          </div>
        </header>

        <main className="flex-1 overflow-auto bg-[var(--bg-base)]">
          <div className="max-w-7xl mx-auto p-6 lg:p-8">{children}</div>
        </main>
      </div>
    </div>
  );
}

function MenuIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path d="M3 5.5h14M3 10h14M3 14.5h14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}
