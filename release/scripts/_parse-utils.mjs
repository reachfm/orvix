// Shared parser utilities for admin module-graph smoke tests.
// This is an internal module, not a standalone executable.
// Imported by smoke-admin-import-graph.mjs (release-bundle canonical)
// and by tests.

export function parseExportListItems(raw) {
  return raw.split(',').map((s) => s.trim()).filter(Boolean).map((s) => {
    const m = s.match(/^(?:type\s+)?([A-Za-z0-9_$]+)(?:\s+as\s+([A-Za-z0-9_$]+))?$/);
    if (m) return m[2] || m[1];
    return s.replace(/^type\s+/, '');
  });
}

export function parseImportNames(raw) {
  const items = raw.split(',').map((s) => s.trim()).filter(Boolean);
  const out = [];
  for (const item of items) {
    if (item.startsWith('type ')) continue;
    const m = item.match(/^(?:type\s+)?([A-Za-z0-9_$]+)(?:\s+as\s+([A-Za-z0-9_$]+))?$/);
    if (m) {
      out.push({ imported: m[1], local: m[2] || m[1] });
    }
  }
  return out;
}
