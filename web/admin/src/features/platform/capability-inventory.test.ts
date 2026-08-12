import { describe, expect, it } from "vitest";
import { CAPABILITY_INVENTORY, PLATFORM_NAVIGATION, capabilitiesForFeature, isWired } from "./capability-inventory";

describe("capability-inventory", () => {
  it("classifies every entry with a valid state and ownership", () => {
    const validStates = ["WIRED", "BACKEND_ONLY", "UNAVAILABLE", "TENANT_ONLY", "PLATFORM_ONLY"];
    for (const c of CAPABILITY_INVENTORY) {
      expect(validStates).toContain(c.state);
      expect(c.owner).toMatch(/^(platform|tenant|shared|none)$/);
      expect(c.method).toMatch(/^(GET|POST|PATCH|PUT|DELETE)$/);
      expect(c.route.startsWith("/")).toBe(true);
      if (c.state === "UNAVAILABLE") {
        expect(c.backendRoute).toBe(false);
      } else {
        expect(c.backendRoute).toBe(true);
      }
      if (c.tenantFamilyOnly) {
        expect(c.owner).toBe("tenant");
      }
      if (c.requiresSupportAccess) {
        expect(c.owner).toBe("shared");
      }
    }
  });

  it("has no duplicate method+route entries", () => {
    const keys = CAPABILITY_INVENTORY.map((c) => `${c.method} ${c.route}`);
    expect(new Set(keys).size).toBe(keys.length);
  });

  it("covers every wired navigation feature with at least one entry", () => {
    for (const nav of PLATFORM_NAVIGATION) {
      expect(isWired(nav.feature)).toBe(true);
      const caps = capabilitiesForFeature(nav.feature);
      expect(caps.length).toBeGreaterThan(0);
    }
  });

  it("classifies mail-control routes by verified ownership", () => {
    // Platform-owned queue routes.
    const summary = capabilitiesForFeature("mail-operations").find((c) => c.route === "/admin/queue/summary");
    expect(summary?.owner).toBe("platform");
    expect(summary?.state).toBe("WIRED");
    // Support-access-aware domain read.
    const domainList = capabilitiesForFeature("domains").find((c) => c.route === "/domains");
    expect(domainList?.owner).toBe("shared");
    expect(domainList?.requiresSupportAccess).toBe(true);
    // Tenant-family-only domain mutations.
    const domainCreate = capabilitiesForFeature("domains").find((c) => c.route === "/domains" && c.method === "POST");
    expect(domainCreate?.owner).toBe("tenant");
    expect(domainCreate?.tenantFamilyOnly).toBe(true);
    // Aliases and groups are tenant-only.
    const aliasList = capabilitiesForFeature("aliases").find((c) => c.method === "GET");
    expect(aliasList?.state).toBe("TENANT_ONLY");
    // Suppression/deliverability have no backend routes.
    expect(capabilitiesForFeature("suppression")[0]?.state).toBe("UNAVAILABLE");
    expect(capabilitiesForFeature("deliverability")[0]?.state).toBe("UNAVAILABLE");
  });

  it("exposes capabilities grouped by feature", () => {
    const orgCaps = capabilitiesForFeature("organizations");
    expect(orgCaps.some((c) => c.route === "/platform/organizations")).toBe(true);
    const billingCaps = capabilitiesForFeature("platform-billing");
    expect(billingCaps.some((c) => c.route.includes("/adjustments"))).toBe(true);
  });
});
