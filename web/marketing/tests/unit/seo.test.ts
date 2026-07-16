import { describe, it, expect } from "vitest";
import {
  ROUTE_SEO,
  SITE_NAME,
  SITE_BASE_URL,
  canonical,
  absoluteUrl,
  organizationLd,
  websiteLd,
} from "../../src/lib/seo";

describe("SEO", () => {
  it("every route has a unique title", () => {
    const titles = Object.values(ROUTE_SEO).map((r) => r.title);
    expect(new Set(titles).size).toBe(titles.length);
  });

  it("every route has a unique description", () => {
    const descriptions = Object.values(ROUTE_SEO).map((r) => r.description);
    expect(new Set(descriptions).size).toBe(descriptions.length);
  });

  it("every title includes the brand", () => {
    for (const r of Object.values(ROUTE_SEO)) {
      expect(r.title.toLowerCase()).toContain("orvix");
    }
  });

  it("every description is at most 200 characters (search-engine friendly)", () => {
    for (const r of Object.values(ROUTE_SEO)) {
      expect(r.description.length).toBeGreaterThan(25);
      expect(r.description.length).toBeLessThanOrEqual(200);
    }
  });

  it("canonical() builds absolute URLs", () => {
    expect(canonical("/")).toBe(`${SITE_BASE_URL}/`);
    expect(canonical("/pricing")).toBe(`${SITE_BASE_URL}/pricing`);
    expect(canonical("/legal/terms")).toBe(`${SITE_BASE_URL}/legal/terms`);
  });

  it("absoluteUrl() handles paths with or without leading slash", () => {
    expect(absoluteUrl("/about")).toBe(`${SITE_BASE_URL}/about`);
    expect(absoluteUrl("about")).toBe(`${SITE_BASE_URL}/about`);
  });

  it("organizationLd() returns valid JSON", () => {
    const json = organizationLd();
    const parsed = JSON.parse(json);
    expect(parsed["@context"]).toBe("https://schema.org");
    expect(parsed["@type"]).toBe("Organization");
    expect(parsed.name).toBe(SITE_NAME);
    expect(parsed.url).toBe(SITE_BASE_URL);
  });

  it("websiteLd() returns valid JSON without advertising an unimplemented site search", () => {
    const json = websiteLd();
    const parsed = JSON.parse(json);
    expect(parsed["@context"]).toBe("https://schema.org");
    expect(parsed["@type"]).toBe("WebSite");
    expect(parsed.potentialAction).toBeUndefined();
  });
});
