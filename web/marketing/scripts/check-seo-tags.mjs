#!/usr/bin/env node
/**
 * Read the index.html that Vite produced in dist/ and assert it
 * has the SEO meta tags the spec requires: title, description,
 * canonical, OG, robots. Run after `vite build` as a final
 * sanity check.
 *
 * Fails non-zero if a required tag is missing or empty.
 */

import { readFile } from "node:fs/promises";
import { resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const distDir = resolve(here, "..", "dist");
const indexPath = resolve(distDir, "index.html");

const required = [
  { attr: "title", label: "<title>" },
  { attr: 'name="description"', label: 'meta name="description"' },
  { attr: 'rel="canonical"', label: 'link rel="canonical"' },
  { attr: 'property="og:title"', label: 'meta property="og:title"' },
  { attr: 'property="og:description"', label: 'meta property="og:description"' },
  { attr: 'name="robots"', label: 'meta name="robots"' },
];

let html;
try {
  html = await readFile(indexPath, "utf8");
} catch (err) {
  console.error(
    `[seo] cannot read ${indexPath} — did you run \`vite build\` first?`,
  );
  process.exit(1);
}

const failures = [];
for (const r of required) {
  // Look for an exact or substring match. Substring is fine for HTML
  // because the attribute order is fixed.
  if (!html.includes(r.attr)) {
    failures.push(r.label);
  }
}

// Title must be non-empty.
const titleMatch = html.match(/<title>([^<]+)<\/title>/);
if (!titleMatch || !titleMatch[1].trim()) {
  failures.push("<title> must be non-empty");
}

if (failures.length > 0) {
  console.error(`[seo] missing or invalid SEO tags in dist/index.html:`);
  for (const f of failures) {
    console.error(`  - ${f}`);
  }
  process.exit(1);
}

console.log(
  `[seo] all required SEO tags present in dist/index.html (title: "${titleMatch[1].trim()}")`,
);
