import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { applyTheme, getStoredTheme, storeTheme, THEME_STORAGE_KEY } from "./theme";

describe("shared/theme: theme persistence and default", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.classList.remove("dark");
  });
  afterEach(() => {
    window.localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("defaults to light when nothing is stored, even if the OS prefers dark", () => {
    expect(getStoredTheme()).toBe("light");
  });

  it("defaults to light on garbage/legacy stored values", () => {
    window.localStorage.setItem(THEME_STORAGE_KEY, "blue");
    expect(getStoredTheme()).toBe("light");
  });

  it("returns dark only after an explicit dark selection was stored", () => {
    window.localStorage.setItem(THEME_STORAGE_KEY, "dark");
    expect(getStoredTheme()).toBe("dark");
  });

  it("storeTheme persists under the exact orvix-admin-theme key", () => {
    storeTheme("dark");
    expect(window.localStorage.getItem("orvix-admin-theme")).toBe("dark");
    storeTheme("light");
    expect(window.localStorage.getItem("orvix-admin-theme")).toBe("light");
  });

  it("applyTheme toggles the .dark class on <html> without touching storage", () => {
    applyTheme("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();

    applyTheme("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
  });

  it("round-trip: store then apply reflects the persisted choice after reload", () => {
    storeTheme("dark");
    applyTheme(getStoredTheme());
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });
});
