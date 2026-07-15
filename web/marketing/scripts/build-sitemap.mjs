#!/usr/bin/env node
/**
 * Build a sitemap.xml from the canonical list of public paths.
 *
 * Runs as part of `npm run build`. Writes dist/sitemap.xml so the
 * Vite static-site output is a complete, crawlable site.
 *
 * The path list is mirrored from src/lib/route-table.ts; if you
 * change one, change both. (The check-internal-links script
 * asserts the two agree.)
 */

import { writeFile, mkdir, readFile } from "node:fs/promises";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const PUBLIC_PATHS = [
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

const SITE = "https://orvix.com";
const today = new Date().toISOString().slice(0, 10);

const urls = PUBLIC_PATHS.map((path) => {
  const priority =
    path === "/"
      ? "1.0"
      : path === "/pricing" || path === "/features"
        ? "0.9"
        : path === "/enterprise" ||
            path === "/security" ||
            path === "/docs" ||
            path === "/api" ||
            path === "/status"
          ? "0.8"
          : "0.6";
  return `  <url>
    <loc>${SITE}${path}</loc>
    <lastmod>${today}</lastmod>
    <changefreq>${path === "/" ? "weekly" : "monthly"}</changefreq>
    <priority>${priority}</priority>
  </url>`;
}).join("\n");

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls}
</urlset>
`;

const here = dirname(fileURLToPath(import.meta.url));
const distDir = resolve(here, "..", "dist");
try {
  await mkdir(distDir, { recursive: true });
  await writeFile(resolve(distDir, "sitemap.xml"), xml, "utf8");
  console.log(
    `[sitemap] wrote ${PUBLIC_PATHS.length} URLs to dist/sitemap.xml`,
  );
} catch (err) {
  console.error(`[sitemap] could not write dist/sitemap.xml:`, err.message);
  process.exitCode = 1;
}
