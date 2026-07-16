import { describe, it, expect } from "vitest";
import { PAGE_LOADERS, PUBLIC_PATHS } from "../../src/lib/route-table";
import { ROUTE_SEO } from "../../src/lib/seo";

/**
 * Pins the route table — every public marketing route must have
 * both a lazy loader and an SEO entry. A missing pair is a
 * 500-error waiting to happen.
 */

describe("route table", () => {
  it("every public path has a lazy loader", () => {
    for (const p of PUBLIC_PATHS) {
      expect(PAGE_LOADERS[p], `missing loader for ${p}`).toBeDefined();
    }
  });

  it("every public path has SEO meta", () => {
    for (const p of PUBLIC_PATHS) {
      expect(ROUTE_SEO[p], `missing SEO for ${p}`).toBeDefined();
      expect(ROUTE_SEO[p].title.length).toBeGreaterThan(0);
      expect(ROUTE_SEO[p].description.length).toBeGreaterThan(0);
    }
  });

  it("a 404 loader is registered", () => {
    expect(PAGE_LOADERS["*"]).toBeDefined();
  });

  it("the route table has 18 public paths + the 404 wildcard", () => {
    expect(PUBLIC_PATHS.length).toBe(18);
    expect(Object.keys(PAGE_LOADERS).length).toBe(19);
  });

  it("includes all 18 required pages from the spec", () => {
    const required = [
      "/",
      "/pricing",
      "/features",
      "/enterprise",
      "/security",
      "/docs",
      "/api",
      "/status",
      "/about",
      "/contact",
      "/blog",
      "/blog/welcome-to-orvix",
      "/legal",
      "/legal/terms",
      "/legal/privacy",
      "/legal/aup",
      "/legal/cookies",
      "/legal/data-and-privacy",
    ];
    for (const p of required) {
      expect(PUBLIC_PATHS, `missing required route: ${p}`).toContain(p);
    }
  });
});
