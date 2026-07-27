import { useState, useCallback } from "react";
import Sidebar from "./Sidebar";
import Topbar from "./Topbar";
import type { NavSection } from "../../types/navigation";

interface AppShellProps {
  sections: NavSection[];
  activeTab: string;
  onNavigate: (id: string) => void;
  userEmail?: string;
  userRole?: string;
  onLogout?: () => void;
  arabicLabels?: Record<string, string>;
  headerTitle?: string;
  headerSubtitle?: string;
  children: React.ReactNode;
}

export default function AppShell({
  sections, activeTab, onNavigate, userEmail, userRole, onLogout,
  arabicLabels, headerTitle, headerSubtitle, children,
}: AppShellProps) {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [direction, setDirection] = useState<"ltr" | "rtl">(() => {
    if (typeof document !== "undefined") return document.documentElement.dir === "rtl" ? "rtl" : "ltr";
    return "ltr";
  });
  const [theme, setTheme] = useState<"dark" | "light">("dark");

  const toggleDirection = useCallback(() => {
    setDirection((v) => {
      const next = v === "ltr" ? "rtl" : "ltr";
      document.documentElement.dir = next;
      document.documentElement.lang = next === "rtl" ? "ar" : "en";
      return next;
    });
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme((v) => {
      const next = v === "dark" ? "light" : "dark";
      document.documentElement.classList.toggle("theme-light", next === "light");
      return next;
    });
  }, []);

  const handleNavigate = useCallback((id: string) => {
    onNavigate(id);
    setMobileSidebarOpen(false);
  }, [onNavigate]);

  const initials = userEmail
    ? userEmail.split("@")[0].replace(/[^a-z0-9]+/gi, " ").trim().split(/\s+/).slice(0, 2).map((p) => p[0]?.toUpperCase()).join("") || "OX"
    : "OX";

  return (
    <div className={`min-h-screen bg-[var(--bg-base)] text-[var(--text-primary)] ${theme === "light" ? "theme-light" : ""}`}>
      {/* Mobile overlay */}
      {mobileSidebarOpen && (
        <button
          onClick={() => setMobileSidebarOpen(false)}
          className="fixed inset-0 z-30 bg-black/50 backdrop-blur-sm lg:hidden"
          aria-label="Close navigation"
          tabIndex={-1}
        />
      )}

      <div className="flex h-screen">
        {/* Desktop sidebar — always in DOM so tests/a11y can reach it; zero-width on mobile */}
        <div className={`flex overflow-hidden transition-[width] duration-300 max-lg:!w-0 ${sidebarCollapsed ? "w-[72px]" : "w-[280px]"}`}>
          <Sidebar
            sections={sections}
            collapsed={sidebarCollapsed}
            activeTab={activeTab}
            onNavigate={handleNavigate}
            onCollapse={() => setSidebarCollapsed((v) => !v)}
            userEmail={userEmail}
            userRole={userRole}
            initials={initials}
            onLogout={onLogout}
            arabicLabels={arabicLabels}
            direction={direction}
          />
        </div>

        {/* Mobile sidebar — only mounted when open to avoid duplicate DOM nodes */}
        {mobileSidebarOpen && (
          <div className="fixed inset-y-0 z-40 lg:hidden animate-in slide-in-from-left-full duration-300">
            <Sidebar
              sections={sections}
              collapsed={false}
              activeTab={activeTab}
              onNavigate={handleNavigate}
              onCollapse={() => {}}
              onClose={() => setMobileSidebarOpen(false)}
              userEmail={userEmail}
              userRole={userRole}
              initials={initials}
              onLogout={onLogout}
              arabicLabels={arabicLabels}
              direction={direction}
            />
          </div>
        )}

        <main className="flex min-w-0 flex-1 flex-col overflow-hidden">
          <Topbar
            title={headerTitle || "Dashboard"}
            subtitle={headerSubtitle}
            direction={direction}
            onToggleDirection={toggleDirection}
            onOpenSidebar={() => setMobileSidebarOpen(true)}
            theme={theme}
            onToggleTheme={toggleTheme}
          />
          <div className="flex-1 overflow-auto">
            <div className="mx-auto w-full max-w-[1500px] p-4 sm:p-5 lg:p-6">
              {children}
            </div>
          </div>
        </main>
      </div>
    </div>
  );
}
