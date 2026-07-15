import { describe, it, expect } from "vitest";
import {
  PLANS,
  formatPrice,
  formatStorage,
  ALL_FEATURE_KEYS,
} from "../../src/lib/plans";

/**
 * Pins the pricing numbers to the Launch Specification v1.0 §0.4
 * and the Go billing seed. If you change these numbers you also
 * need to change the spec and the seed.
 */

describe("plans — Launch Specification v1.0 §0.4", () => {
  it("has exactly four plans in the right order", () => {
    expect(PLANS.map((p) => p.id)).toEqual([
      "free",
      "starter",
      "business",
      "enterprise",
    ]);
  });

  it("Free plan matches the spec", () => {
    const free = PLANS.find((p) => p.id === "free")!;
    expect(free.name).toBe("Free");
    expect(free.usdMonthly).toBe(0);
    expect(free.usdYearly).toBe(0);
    expect(free.domains).toBe(1);
    expect(free.mailboxes).toBe(5);
    expect(free.storageBytes).toBe(1024 ** 3);
    expect(free.sendsPerDay).toBe(500);
    expect(free.features.map((f) => f.key).sort()).toEqual(
      ["custom_domain", "dkim"].sort(),
    );
  });

  it("Starter plan matches the spec", () => {
    const s = PLANS.find((p) => p.id === "starter")!;
    expect(s.name).toBe("Starter");
    expect(s.usdMonthly).toBe(9.99);
    expect(s.usdYearly).toBe(99.9);
    expect(s.domains).toBe(3);
    expect(s.mailboxes).toBe(25);
    expect(s.storageBytes).toBe(10 * 1024 ** 3);
    expect(s.sendsPerDay).toBe(2000);
  });

  it("Business plan matches the spec", () => {
    const b = PLANS.find((p) => p.id === "business")!;
    expect(b.name).toBe("Business");
    expect(b.usdMonthly).toBe(29.99);
    expect(b.usdYearly).toBe(299.9);
    expect(b.domains).toBe(10);
    expect(b.mailboxes).toBe(100);
    expect(b.storageBytes).toBe(100 * 1024 ** 3);
    expect(b.sendsPerDay).toBe(10000);
    expect(b.isFeatured).toBe(true);
  });

  it("Enterprise plan matches the spec", () => {
    const e = PLANS.find((p) => p.id === "enterprise")!;
    expect(e.name).toBe("Enterprise");
    expect(e.usdMonthly).toBe(99.99);
    expect(e.usdYearly).toBe(999.9);
    expect(e.domains).toBe(100);
    expect(e.mailboxes).toBe(1000);
    expect(e.storageBytes).toBe(1024 ** 4);
    expect(e.sendsPerDay).toBe(100000);
  });

  it("annual prices are 10x monthly (a 16% discount), per the spec", () => {
    for (const p of PLANS) {
      if (p.usdMonthly === 0) {
        expect(p.usdYearly).toBe(0);
      } else {
        expect(p.usdYearly).toBeCloseTo(p.usdMonthly * 10, 2);
      }
    }
  });

  it("Free is the only plan that includes DKIM and custom_domain by default", () => {
    const free = PLANS.find((p) => p.id === "free")!;
    const starter = PLANS.find((p) => p.id === "starter")!;
    expect(free.features.find((f) => f.key === "dkim")).toBeTruthy();
    expect(free.features.find((f) => f.key === "custom_domain")).toBeTruthy();
    // starter and above have at least the same set
    expect(starter.features.find((f) => f.key === "dkim")).toBeTruthy();
  });

  it("MTA-STS is on Starter and above, not on Free", () => {
    const free = PLANS.find((p) => p.id === "free")!;
    const starter = PLANS.find((p) => p.id === "starter")!;
    expect(free.features.some((f) => f.key === "mta_sts")).toBe(false);
    expect(starter.features.some((f) => f.key === "mta_sts")).toBe(true);
  });

  it("SSO, SLA, priority_support are Enterprise-only", () => {
    for (const key of ["sso", "sla", "priority_support"]) {
      for (const p of PLANS) {
        const has = p.features.some((f) => f.key === key);
        if (p.id === "enterprise") {
          expect(has, `${p.id} should have ${key}`).toBe(true);
        } else {
          expect(has, `${p.id} should not have ${key}`).toBe(false);
        }
      }
    }
  });

  it("formatPrice renders USD correctly", () => {
    expect(formatPrice(0, "monthly")).toBe("Free");
    expect(formatPrice(9.99, "monthly")).toBe("$9.99/mo");
    expect(formatPrice(99.9, "yearly")).toBe("$99.90/yr");
  });

  it("formatStorage renders bytes as GB or TB", () => {
    expect(formatStorage(1 * 1024 ** 3)).toBe("1 GB");
    expect(formatStorage(10 * 1024 ** 3)).toBe("10 GB");
    expect(formatStorage(1 * 1024 ** 4)).toBe("1 TB");
  });

  it("feature keys are unique across the catalog", () => {
    expect(new Set(ALL_FEATURE_KEYS).size).toBe(ALL_FEATURE_KEYS.length);
  });
});
