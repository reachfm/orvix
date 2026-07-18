import { describe, expect, test } from "vitest";
import { CANONICAL, PORTAL_LOGIN, PORTAL_SIGNUP } from "../../src/lib/links";

const ALLOWED = Object.values(CANONICAL);

const FORBIDDEN = ["orvix.com", "app.orvix.com", "app.orvix.email"];

describe("domain allowlist", () => {
  test("canonical origins are all .email variants", () => {
    for (const origin of ALLOWED) {
      expect(origin).toMatch(/\.email/);
    }
  });

  test("no forbidden domains in allowed set", () => {
    for (const forbidden of FORBIDDEN) {
      for (const allowed of ALLOWED) {
        expect(allowed).not.toContain(forbidden);
      }
    }
  });

  test("portal is admin subdomain", () => {
    expect(CANONICAL.portal).toMatch(/^https:\/\/admin\.orvix\.email/);
  });

  test("portal login is under admin", () => {
    expect(PORTAL_LOGIN).toBe("https://admin.orvix.email/admin/login");
    expect(PORTAL_SIGNUP).toBe("https://admin.orvix.email/admin/signup");
  });

  test("marketing is primary domain", () => {
    expect(CANONICAL.marketing).toBe("https://orvix.email");
  });
});
