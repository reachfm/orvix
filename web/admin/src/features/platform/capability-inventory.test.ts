import { describe, expect, it } from "vitest";
import { CAPABILITY_INVENTORY, PLATFORM_NAVIGATION, capabilitiesForFeature, isWired } from "./capability-inventory";

describe("capability-inventory", () => {
  it("classifies every entry as WIRED or BACKEND_ONLY (never UNAVAILABLE for a real route)", () => {
    for (const c of CAPABILITY_INVENTORY) {
      expect(["WIRED", "BACKEND_ONLY"]).toContain(c.state);
      expect(c.backendRoute).toBe(true);
      expect(c.method).toMatch(/^(GET|POST|PATCH|PUT|DELETE)$/);
      expect(c.route.startsWith("/")).toBe(true);
    }
  });

  it("has no duplicate method+route entries", () => {
    const keys = CAPABILITY_INVENTORY.map((c) => `${c.method} ${c.route}`);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it("covers every wired navigation feature", () => {
    for (const nav of PLATFORM_NAVIGATION) {
      expect(isWired(nav.feature)).toBe(true);
      const caps = capabilitiesForFeature(nav.feature);
      expect(caps.length).toBeGreaterThan(0);
      expect(caps.every((c) => c.state === "WIRED")).toBe(true);
    }
  });

  it("exposes capabilities grouped by feature", () => {
    const orgCaps = capabilitiesForFeature("organizations");
    expect(orgCaps.some((c) => c.route === "/platform/organizations")).toBe(true);
    const billingCaps = capabilitiesForFeature("platform-billing");
    expect(billingCaps.some((c) => c.route.includes("/adjustments"))).toBe(true);
  });
});
