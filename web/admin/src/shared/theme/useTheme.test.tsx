import "@testing-library/jest-dom/vitest";
import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import ThemeToggle from "./ThemeToggle";
import { THEME_STORAGE_KEY } from "./theme";

describe("ThemeToggle", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.classList.remove("dark");
  });
  afterEach(() => {
    cleanup();
    window.localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  it("renders in the Light state by default", () => {
    render(<ThemeToggle />);
    const toggle = screen.getByRole("switch");
    expect(toggle).toHaveAttribute("aria-checked", "false");
    expect(screen.getByText("Light")).toBeInTheDocument();
  });

  it("clicking switches to Dark, applies the .dark class, and persists it", () => {
    render(<ThemeToggle />);
    fireEvent.click(screen.getByRole("switch"));

    expect(screen.getByText("Dark")).toBeInTheDocument();
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
  });

  it("clicking twice returns to Light and removes the .dark class", () => {
    render(<ThemeToggle />);
    const toggle = screen.getByRole("switch");
    fireEvent.click(toggle);
    fireEvent.click(toggle);

    expect(screen.getByText("Light")).toBeInTheDocument();
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
  });

  it("is keyboard accessible as a switch with an aria-label", () => {
    render(<ThemeToggle />);
    const toggle = screen.getByRole("switch");
    expect(toggle).toHaveAttribute("aria-label");
  });
});
