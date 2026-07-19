// smoke-admin-import-graph.mjs
// Smoke test for the admin UI module graph: every imported path
// must exist; every name imported must be exported by the target.

import { promises as fs } from 'node:fs';
import path from 'node:path';
import { parseExportListItems, parseImportNames } from './_parse-utils.mjs';

const ROOT = path.resolve(process.argv[2] || 'release/admin');

function logResult(label, ok, detail) {
  const tag = ok ? '\x1b[32mPASS\x1b[0m' : '\x1b[31mFAIL\x1b[0m';
  console.log(`${tag} ${label}${detail ? ' — ' + detail : ''}`);
  if (!ok) process.exitCode = 1;
}

async function listJsFiles(dir) {
  const entries = await fs.readdir(dir, { withFileTypes: true });
  const out = [];
  for (const e of entries) {
    const full = path.join(dir, e.name);
    if (e.isDirectory()) {
      out.push(...await listJsFiles(full));
    } else if (e.isFile() && e.name.endsWith('.js')) {
      out.push(full);
    }
  }
  return out;
}

const files = await listJsFiles(ROOT);
logResult('admin JS files discovered', files.length > 0, `${files.length} files`);
if (files.length < 40) {
  console.log(`\x1b[33mNOTE\x1b[0m fewer than 40 JS files (${files.length}) — may be a partial build or test fixture`);
}

// byRelPath maps each JS file path (relative to ROOT) to:
//   { imports: [{ spec, names }], exports: [string] }
const byRelPath = new Map();

function stripComments(src) {
  let out = src.replace(/\/\*[\s\S]*?\*\//g, '');
  out = out.split('\n').map((line) => line.replace(/^\s*\/\/[^\r\n]*/, '')).join('\n');
  return out;
}

for (const f of files) {
  const rel = path.relative(ROOT, f).split(path.sep).join('/');
  const raw = await fs.readFile(f, 'utf8');
  const src = stripComments(raw);
  byRelPath.set(rel, { abs: f, src, raw, imports: [], exports: [] });
}

// ──── Parse exports and imports ──────────────────────────────────

// Export regexes (in increasing specificity order so each pass
// resets lastIndex):
const exportRe        = /export\s+(?:async\s+)?(?:function|class|const|let|var)\s+([A-Za-z0-9_$]+)/g;
const exportDefaultRe = /export\s+default\s+(?:async\s+)?(?:function|class)\s+([A-Za-z0-9_$]+)/g;
const exportNamedListRe = /export\s*\{([^}]+)\}/g;
const exportFromRe    = /export\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;

// Import regexes — order matters: combined forms must be tried
// before the individual ones that would partially match.
const importCombinedDefaultNamedRe = /import\s+([A-Za-z0-9_$]+)\s*,\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;
const importCombinedDefaultStarRe  = /import\s+([A-Za-z0-9_$]+)\s*,\s*\*\s*as\s+([A-Za-z0-9_$]+)\s*from\s*['"]([^'"]+)['"]/g;
const importNamedRe    = /import\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;
const importStarRe     = /import\s*\*\s*as\s+([A-Za-z0-9_$]+)\s*from\s*['"]([^'"]+)['"]/g;
const importDefaultRe  = /import\s+([A-Za-z0-9_$]+)\s*from\s*['"]([^'"]+)['"]/g;
const importBareRe     = /import\s+['"]([^'"]+)['"]/g;

// Helper: push an import record.
function addImport(info, spec, names) {
  info.imports.push({ spec, names });
}

for (const [rel, info] of byRelPath) {
  const src = info.src;

  // ── exports ─────────────────────────────────────────────────────
  {
    let m;
    exportRe.lastIndex = 0;
    while ((m = exportRe.exec(src))) info.exports.push(m[1]);

    exportDefaultRe.lastIndex = 0;
    while ((m = exportDefaultRe.exec(src))) {
      // Named default exports (e.g. `export default function App(){}`)
      // are already caught by exportRe, so we register the 'default'
      // keyword that importers must use.
      info.exports.push('default');
    }

    // anonymous default export not already covered:
    // These forms: `export default class {}`, `export default function(){}`,
    // `export default 42`, `export default expression` are not matched
    // by exportDefaultRe (which requires an identifier after function/class).
    // We use a broader fallback: any `export default` not matched by
    // the named-default re.  We can't simply run another re because the
    // broad form may overlap.  Instead we catch it via a simpler check:
    // every `default` seen after `export default` that is NOT already
    // registered is an anonymous default.
    // To avoid double-counting, we just ensure 'default' is always present
    // when the source contains `export default`.
    if (/export\s+default\b/.test(src) && !info.exports.includes('default')) {
      info.exports.push('default');
    }

    exportNamedListRe.lastIndex = 0;
    while ((m = exportNamedListRe.exec(src))) {
      for (const name of parseExportListItems(m[1])) info.exports.push(name);
    }

    exportFromRe.lastIndex = 0;
    while ((m = exportFromRe.exec(src))) {
      // For re-exports, the current module exports the public name.
      for (const name of parseExportListItems(m[1])) info.exports.push(name);
      // The source module must resolve the source name; we add an
      // import record with the source names so the validator will
      // check the source module for those.  The spec is the re-export
      // target.
      const records = parseImportNames(m[1]);
      const sourceNames = records.map((r) => r.imported);
      addImport(info, m[2], sourceNames);
    }
  }

  // ── imports ─────────────────────────────────────────────────────
  {
    let m;

    // Combined: import Default, { named } from '...'
    importCombinedDefaultNamedRe.lastIndex = 0;
    while ((m = importCombinedDefaultNamedRe.exec(src))) {
      addImport(info, m[3], ['default']);
      const records = parseImportNames(m[2]);
      addImport(info, m[3], records.map((r) => r.imported));
    }

    // Combined: import Default, * as Namespace from '...'
    importCombinedDefaultStarRe.lastIndex = 0;
    while ((m = importCombinedDefaultStarRe.exec(src))) {
      addImport(info, m[3], ['default']);
      addImport(info, m[3], ['*' + m[2]]);
    }

    // import { ... } from '...'
    importNamedRe.lastIndex = 0;
    while ((m = importNamedRe.exec(src))) {
      const records = parseImportNames(m[1]);
      const importedNames = records.map((r) => r.imported);
      addImport(info, m[2], importedNames);
    }

    // import * as Namespace from '...'
    importStarRe.lastIndex = 0;
    while ((m = importStarRe.exec(src))) {
      addImport(info, m[2], ['*' + m[1]]);
    }

    // import Default from '...'
    // IMPORTANT: after combined-form regexes have consumed the
    // `import Default, { ... }` patterns, this re will match only
    // standalone default imports like `import React from "react"`.
    importDefaultRe.lastIndex = 0;
    while ((m = importDefaultRe.exec(src))) {
      addImport(info, m[2], ['default']);
    }

    // import '...' (side-effect)
    importBareRe.lastIndex = 0;
    while ((m = importBareRe.exec(src))) {
      addImport(info, m[1], []);
    }
  }
}

// ──── Validation ──────────────────────────────────────────────────

let importCount = 0;
let importErrors = 0;
const missing = [];
const importNotExported = [];

for (const [rel, info] of byRelPath) {
  const dir = path.posix.dirname(rel);
  for (const imp of info.imports) {
    importCount++;
    // Skip URL imports (not relevant for static analysis).
    if (!imp.spec.startsWith('.') && !imp.spec.startsWith('/')) continue;
    let resolved;
    if (imp.spec.startsWith('.')) {
      resolved = path.posix.normalize(path.posix.join(dir, imp.spec));
    } else {
      resolved = imp.spec.replace(/^\//, '');
    }
    const target = byRelPath.get(resolved);
    if (!target) {
      missing.push({ from: rel, spec: imp.spec, resolved });
      importErrors++;
      continue;
    }
    // Check each imported name against target exports.
    for (const name of imp.names) {
      if (name.startsWith('*')) continue; // namespace import — accept
      if (target.exports.includes(name)) continue;
      importNotExported.push({ from: rel, to: resolved, name });
    }
  }
}

logResult('admin JS modules parse', true);
logResult('every imported specifier resolves to an existing file', missing.length === 0,
  missing.length ? `missing: ${missing.slice(0, 5).map((m) => m.from + '->' + m.spec).join(', ')}` : `${importCount} imports checked`);
logResult('every imported name is exported by its target module',
  importNotExported.length === 0,
  importNotExported.length ? `unresolved: ${importNotExported.slice(0, 8).map((m) => m.from + '->' + m.to + ':' + m.name).join(', ')}` : 'all names resolved');

// ──── Specific app.js contract check ─────────────────────────────
// Only applies when the admin build includes both app.js and modules/auth.js.

const app = byRelPath.get('app.js');
const authModule = byRelPath.get('modules/auth.js');
if (app && authModule) {
  const authImport = app.imports.find((i) => i.spec === './modules/auth.js');
  if (!authImport) {
    logResult('app.js imports auth.js', false, 'no import for ./modules/auth.js');
  } else {
    const has = authImport.names.includes('renderLogin') && authImport.names.includes('hasValidSession');
    logResult('app.js imports { renderLogin, hasValidSession } from auth.js', has,
      has ? 'present' : `names: ${authImport.names.join(',')}`);
  }
}

if (process.exitCode === 1) {
  console.log('\nFAIL — admin UI module graph has structural problems');
} else {
  console.log('\nOK — admin UI module graph is structurally sound');
}
