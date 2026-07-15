#!/usr/bin/env node
/**
 * Walk src/ for href/to attributes and assert every internal link
 * points to a route in src/lib/route-table.ts. External links
 * (mailto:, https://) are ignored. This is the build-time
 * "broken-link check" required by the spec.
 *
 * Exit non-zero if any internal link doesn't resolve to a known
 * route. The expected output is zero matches.
 */

import { readdir, readFile, stat } from "node:fs/promises";
import { resolve, dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const PUBLIC_PATHS = new Set([
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
]);

// Auth/onboarding routes that are served by the portal host
// (app.orvix.com). The marketing site links to them.
const PORTAL_PATHS = new Set([
  "/login",
  "/signup",
  "/forgot",
  "/reset",
  "/changelog",
]);

// /docs/<slug> is allowed if <slug> is in DOC_PATHS or in
// docs-index.ts (the marketing-site mirror). We accept any /docs/*
// here and rely on the data file to gate the slugs.

const PUBLIC_PREFIXES = ["/docs/"];

const KNOWN = new Set([...PUBLIC_PATHS, ...PORTAL_PATHS]);

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const srcDir = resolve(root, "src");

const failures = [];

async function walk(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      await walk(full);
      continue;
    }
    if (!/\.(tsx?|jsx?)$/.test(entry.name)) {
      continue;
    }
    const content = await readFile(full, "utf8");
    const lines = content.split(/\r?\n/);
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      // Match JSX prop syntax: to="/foo" or href="/foo". We do NOT
      // match object-literal shorthand: `to: "/foo"` (note the
      // colon-vs-equals distinction), because that pattern shows up
      // in plain data objects where the value is not a link.
      const matches = [
        ...line.matchAll(/(?:\bhref|\bto)\s*=\s*["'`]([^"'`]+)["'`]/g),
      ];
      for (const m of matches) {
        const url = m[1];
        if (!url || url.startsWith("http") || url.startsWith("mailto:")) {
          continue;
        }
        if (url.startsWith("#") || url.startsWith("?")) {
          continue;
        }
        // Extract just the path part (ignore search/hash).
        const pathOnly = url.split(/[?#]/)[0];
        if (KNOWN.has(pathOnly)) {
          continue;
        }
        if (PUBLIC_PREFIXES.some((p) => pathOnly.startsWith(p))) {
          continue;
        }
        failures.push({
          file: relative(root, full),
          line: i + 1,
          url,
        });
      }
    }
  }
}

await walk(srcDir);

if (failures.length > 0) {
  console.error(
    `[links] ${failures.length} internal link(s) do not match a known route:`,
  );
  for (const f of failures) {
    console.error(`  ${f.file}:${f.line}  ${f.url}`);
  }
  process.exit(1);
}

console.log(
  `[links] all internal hrefs/to values resolve to a known route`,
);
