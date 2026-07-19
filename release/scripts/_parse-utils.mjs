// Shared parser utilities for admin module-graph smoke tests.
// This is an internal module, not a standalone executable.
// Imported by smoke-admin-import-graph.mjs (release-bundle canonical)
// and by tests.

// parseExportListItems: given the inside of `export { ... }` (no braces),
// returns the exported names. For `localName as exportedName` the right
// side (exportedName) is returned.  For `localName` alone, it is returned
// as-is.
export function parseExportListItems(raw) {
  return raw.split(',').map((s) => s.trim()).filter(Boolean).map((s) => {
    const m = s.match(/^(?:type\s+)?([A-Za-z0-9_$]+)(?:\s+as\s+([A-Za-z0-9_$]+))?$/);
    if (m) return m[2] || m[1];
    return s.replace(/^type\s+/, '');
  });
}

// parseImportNames: given the inside of `import { ... }` (no braces),
// returns an array of { imported, local, kind } records.
//   `r`          → { imported:"r", local:"r",         kind:"named" }
//   `r as oy`    → { imported:"r", local:"oy",        kind:"named" }
//   `type Foo`   → skipped (type-only import, no runtime export needed)
//   `type Foo as Bar` → skipped
export function parseImportNames(raw) {
  const items = raw.split(',').map((s) => s.trim()).filter(Boolean);
  const out = [];
  for (const item of items) {
    if (item.startsWith('type ')) continue;
    const m = item.match(/^(?:type\s+)?([A-Za-z0-9_$]+)(?:\s+as\s+([A-Za-z0-9_$]+))?$/);
    if (m) {
      out.push({ imported: m[1], local: m[2] || m[1], kind: 'named' });
    }
  }
  return out;
}
