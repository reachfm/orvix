import { useCallback, useState } from "react";
import { applyTheme, getStoredTheme, storeTheme, type Theme } from "./theme";

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(() => getStoredTheme());

  const setTheme = useCallback((next: Theme) => {
    setThemeState(next);
    applyTheme(next);
    storeTheme(next);
  }, []);

  const toggleTheme = useCallback(() => {
    setTheme(theme === "dark" ? "light" : "dark");
  }, [theme, setTheme]);

  return { theme, setTheme, toggleTheme };
}
