import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "fs";
import { join } from "path";

// Guards the theme conversion: application components must reference
// only the semantic tokens in index.css (var(--...)), never a literal
// hex color or a raw Tailwind color-scale utility, both of which break
// on a theme switch (baked into one palette, illegible in the other).
const SRC_DIR = join(__dirname, "..", "..");
const SKIP_DIRS = new Set(["node_modules", "dist"]);
const HEX_PATTERN = /#[0-9A-Fa-f]{3,8}\b/;
// Raw Tailwind color-scale utilities (text-red-400, bg-gray-800, ...).
// "white"/"black" are intentionally excluded — they're used deliberately
// for fixed-contrast text/icons on solid colored buttons and badges,
// which read correctly in both themes by design (see LoginPage.tsx's
// "text-white" on an accent-colored button, or SecurityPage.tsx's QR
// code "bg-white" backdrop).
const NAMED_COLOR_PATTERN =
  /\b(?:text|bg|border|ring|divide|from|to|via)-(?:red|green|blue|yellow|orange|purple|indigo|teal|cyan|pink|rose|sky|violet|lime|emerald|amber|slate|zinc|neutral|stone|gray)-\d{2,3}\b/;

function walk(dir: string, files: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    if (SKIP_DIRS.has(entry)) continue;
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      walk(full, files);
    } else if (/\.(tsx|ts)$/.test(entry) && !/\.test\.(tsx|ts)$/.test(entry)) {
      files.push(full);
    }
  }
  return files;
}

describe("theme: no hardcoded colors in application components", () => {
  const files = walk(SRC_DIR);
  it("scanned at least the known component set", () => {
    expect(files.length).toBeGreaterThan(20);
  });

  for (const file of files) {
    const rel = file.slice(SRC_DIR.length + 1);
    it(`${rel} has no hardcoded hex or Tailwind color-scale utility`, () => {
      const content = readFileSync(file, "utf8");
      const hexMatch = content.match(HEX_PATTERN);
      const namedMatch = content.match(NAMED_COLOR_PATTERN);
      if (hexMatch) {
        throw new Error(`hardcoded hex color ${hexMatch[0]} found — use a var(--token) from index.css instead`);
      }
      if (namedMatch) {
        throw new Error(`hardcoded Tailwind color utility ${namedMatch[0]} found — use a var(--token) from index.css instead`);
      }
    });
  }
});
