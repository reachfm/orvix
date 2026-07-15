#!/usr/bin/env node
/**
 * Copy the freshly-built web/marketing/dist into release/marketing/
 * so the artifacts are committed alongside the source — matching
 * the existing release policy used by release/webmail/ and
 * release/admin/.
 *
 * Idempotent: overwrites the destination in place. Only operates
 * on the marketing artifacts; it will not touch any other release
 * directory.
 */

import { readdir, copyFile, mkdir, stat, rm } from "node:fs/promises";
import { resolve, dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");
const srcDir = resolve(repoRoot, "web", "marketing", "dist");
const dstDir = resolve(repoRoot, "release", "marketing");

async function exists(p) {
  try {
    await stat(p);
    return true;
  } catch {
    return false;
  }
}

async function copyTree(src, dst) {
  const entries = await readdir(src, { withFileTypes: true });
  await mkdir(dst, { recursive: true });
  for (const entry of entries) {
    const s = join(src, entry.name);
    const d = join(dst, entry.name);
    if (entry.isDirectory()) {
      await copyTree(s, d);
    } else {
      await copyFile(s, d);
    }
  }
}

if (!(await exists(srcDir))) {
  console.error(
    `[release] ${relative(repoRoot, srcDir)} does not exist — run \`npm run marketing:build\` first`,
  );
  process.exit(1);
}

// Wipe and re-copy. The release/marketing/ directory only contains
// build artifacts, so this is safe and idempotent.
await rm(dstDir, { recursive: true, force: true });
await mkdir(dstDir, { recursive: true });
await copyTree(srcDir, dstDir);

const finalEntries = await readdir(dstDir);
console.log(
  `[release] copied ${finalEntries.length} entries from ${relative(repoRoot, srcDir)} to ${relative(repoRoot, dstDir)}`,
);
