export type Theme = "light" | "dark";

export const THEME_STORAGE_KEY = "orvix-admin-theme";

// Light is the mandatory default — never inferred from
// prefers-color-scheme. Only an explicit prior user choice in
// localStorage can select dark.
export function getStoredTheme(): Theme {
  try {
    const raw = window.localStorage.getItem(THEME_STORAGE_KEY);
    return raw === "dark" ? "dark" : "light";
  } catch {
    return "light";
  }
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle("dark", theme === "dark");
}

export function storeTheme(theme: Theme): void {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    /* localStorage unavailable (private mode, etc.) — theme still
       applies for this session via applyTheme, just doesn't persist. */
  }
}
