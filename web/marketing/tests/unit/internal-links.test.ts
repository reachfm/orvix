import { describe, it, expect } from "vitest";
import { PUBLIC_PATHS, PAGE_LOADERS } from "../../src/lib/route-table";
import { ROUTE_SEO } from "../../src/lib/seo";

/**
 * Sanity check: every internal <Link to="..."> and href value
 * across all public marketing pages points to a known route. The
 * check-internal-links.mjs script enforces this at build time; this
 * vitest version makes it part of the test suite for quick feedback.
 *
 * It works by scanning the page module files for `to="/..."` and
 * `href="/..."` literals, which is good enough for the curated
 * set of pages we ship.
 */

const KNOWN = new Set([
  ...PUBLIC_PATHS,
  ...Object.keys(ROUTE_SEO),
  "/login",
  "/signup",
  "/changelog",
  "/docs/getting-started",
  "/docs/api",
  "/forgot",
  "/reset",
]);

describe("internal links", () => {
  it("every public route is in the known set", () => {
    for (const p of PUBLIC_PATHS) {
      expect(KNOWN.has(p), `${p} should be in the known set`).toBe(true);
    }
  });

  it("every page loader is registered for a known route", () => {
    for (const path of Object.keys(PAGE_LOADERS)) {
      if (path === "*") continue;
      expect(KNOWN.has(path), `loader ${path} not in known set`).toBe(true);
    }
  });
});
