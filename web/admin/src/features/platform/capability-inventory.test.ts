import { describe, expect, it } from "vitest";
import {
  CAPABILITY_INVENTORY,
  PLATFORM_NAVIGATION,
  capabilitiesForFeature,
  isWired,
  navigationDeadLinkCheck,
} from "./capability-inventory";

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

  it("covers every wired navigation feature with at least one entry and no dead links", () => {
    expect(navigationDeadLinkCheck()).toEqual([]);
    for (const nav of PLATFORM_NAVIGATION) {
      expect(isWired(nav.feature)).toBe(true);
      const caps = capabilitiesForFeature(nav.feature);
      expect(caps.length).toBeGreaterThan(0);
      expect(caps.some((c) => c.state === "WIRED")).toBe(true);
    }
  });

  it("classifies platform mail-control routes as WIRED with platform ownership and permissions", () => {
    // PSA mail control runs on /platform/* routes — no support grant.
    const domainList = capabilitiesForFeature("domains").find((c) => c.route === "/platform/domains/:tenant_id");
    expect(domainList?.owner).toBe("platform");
    expect(domainList?.state).toBe("WIRED");
    expect(domainList?.requiresSupportAccess).toBeUndefined();
    expect(domainList?.permission).toBe("domains.read");

    const mailboxBulk = capabilitiesForFeature("bulk-mailboxes").find((c) => c.route === "/platform/mailboxes/:tenant_id/bulk/status");
    expect(mailboxBulk?.state).toBe("WIRED");
    expect(mailboxBulk?.permission).toBe("mailboxes.write");

    const aliasCreate = capabilitiesForFeature("aliases").find((c) => c.method === "POST" && c.route === "/platform/aliases/:tenant_id");
    expect(aliasCreate?.state).toBe("WIRED");
    expect(aliasCreate?.permission).toBe("aliases.write");

    const groupMembers = capabilitiesForFeature("groups").find((c) => c.route === "/platform/groups/:tenant_id/:id/members");
    expect(groupMembers?.state).toBe("WIRED");

    const suppressionRelease = capabilitiesForFeature("suppressions").find((c) => c.route === "/platform/suppressions/:tenant_id/:id/release");
    expect(suppressionRelease?.state).toBe("WIRED");
    expect(suppressionRelease?.permission).toBe("suppressions.write");

    const metrics = capabilitiesForFeature("deliverability").find((c) => c.route === "/platform/deliverability/:tenant_id/metrics");
    expect(metrics?.state).toBe("WIRED");
    expect(metrics?.permission).toBe("deliverability.read");

    const relayRotate = capabilitiesForFeature("relay").find((c) => c.route === "/platform/relays/:id/rotate-credentials");
    expect(relayRotate?.state).toBe("WIRED");
    expect(relayRotate?.permission).toBe("relay.write");
  });

  it("keeps tenant-family mail routes TENANT_ONLY and never wired for PSA", () => {
    const tenantDomainList = capabilitiesForFeature("domains").find((c) => c.route === "/domains" && c.method === "GET");
    expect(tenantDomainList?.state).toBe("TENANT_ONLY");
    expect(tenantDomainList?.tenantFamilyOnly).toBe(true);
    const tenantMailboxCreate = capabilitiesForFeature("mailboxes").find((c) => c.method === "POST" && c.route === "/mailboxes");
    expect(tenantMailboxCreate?.state).toBe("TENANT_ONLY");
    const tenantAlias = capabilitiesForFeature("aliases").find((c) => c.route === "/enterprise/aliases");
    expect(tenantAlias?.state).toBe("TENANT_ONLY");
    // No PSA mail-control feature may list a tenant-only route as its
    // only WIRED path.
    for (const feature of ["domains", "mailboxes", "aliases", "groups", "relay", "suppressions", "deliverability"]) {
      const wired = capabilitiesForFeature(feature).filter((c) => c.state === "WIRED");
      expect(wired.length).toBeGreaterThan(0);
      expect(wired.every((c) => c.owner === "platform")).toBe(true);
    }
  });

  it("no PSA mail-control capability requires support access", () => {
    for (const feature of ["domains", "mailboxes", "aliases", "groups", "relay", "suppressions", "deliverability", "bulk-mailboxes"]) {
      for (const c of capabilitiesForFeature(feature)) {
        expect(c.requiresSupportAccess).toBeUndefined();
      }
    }
  });

  it("exposes capabilities grouped by feature", () => {
    const orgCaps = capabilitiesForFeature("organizations");
    expect(orgCaps.some((c) => c.route === "/platform/organizations")).toBe(true);
    const billingCaps = capabilitiesForFeature("platform-billing");
    expect(billingCaps.some((c) => c.route.includes("/adjustments"))).toBe(true);
  });
});
