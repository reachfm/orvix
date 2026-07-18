import { describe, expect, test } from "vitest";
import fs from "node:fs";
import path from "node:path";

describe("robots.txt", () => {
  const srcRobots = path.resolve(__dirname, "../../public/robots.txt");
  const builtRobots = path.resolve(__dirname, "../../../../release/marketing/robots.txt");
  const builtSitemap = path.resolve(__dirname, "../../../../release/marketing/sitemap.xml");

  const src = fs.readFileSync(srcRobots, "utf-8");
  const built = fs.existsSync(builtRobots) ? fs.readFileSync(builtRobots, "utf-8") : "";
  const sitemap = fs.existsSync(builtSitemap) ? fs.readFileSync(builtSitemap, "utf-8") : "";

  test("source robots.txt contains canonical sitemap", () => {
    expect(src).toContain("Sitemap: https://orvix.email/sitemap.xml");
  });

  test("source robots.txt contains no orvix.com", () => {
    expect(src).not.toContain("orvix.com");
  });

  test("source robots.txt contains no app.orvix.com", () => {
    expect(src).not.toContain("app.orvix.com");
  });

  test("built robots.txt contains canonical sitemap", () => {
    expect(built).toContain("Sitemap: https://orvix.email/sitemap.xml");
  });

  test("built sitemap.xml contains only orvix.email origins", () => {
    expect(sitemap).toContain("https://orvix.email");
    expect(sitemap).not.toContain("orvix.com");
  });

  test("built sitemap.xml contains page entries", () => {
    expect(sitemap).toContain("<url>");
    expect(sitemap).toContain("<loc>");
  });

  test("neither generated file contains orvix.com", () => {
    if (built) expect(built).not.toContain("orvix.com");
    if (sitemap) expect(sitemap).not.toContain("orvix.com");
  });
});
