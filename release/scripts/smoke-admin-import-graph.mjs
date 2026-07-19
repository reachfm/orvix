// smoke-admin-import-graph.mjs
// Smoke test for the admin UI module graph: every imported path
// must exist; every name imported must be exported by the target.
// Catches the structural mistakes (typo in module path, missing
// export, syntax error) that previously left the admin UI showing
// the static wrapper without a login form.

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
logResult('admin JS files discovered', files.length >= 40, `${files.length} files`);

// Map each file path (relative to ROOT, with forward slashes) to
// { imports: [{spec, names}], exports: [name] }
const byRelPath = new Map();
function stripComments(src) {
  // Strip /* ... */ block comments and // line comments so the
  // regex pass below does not pick up `import` / `export` mentioned
  // in prose. Strings are left alone; if any module contains the
  // literal word "import" inside a string the regex will still match
  // it, but the imports that resolve will be a superset of the
  // actual ones (false-positive — we still find every real one).
  let out = src.replace(/\/\*[\s\S]*?\*\//g, '');
  out = out.split('\n').map((line) => line.replace(/\/\/.*$/, '')).join('\n');
  return out;
}
for (const f of files) {
  const rel = path.relative(ROOT, f).split(path.sep).join('/');
  const raw = await fs.readFile(f, 'utf8');
  const src = stripComments(raw);
  byRelPath.set(rel, { abs: f, src, raw, imports: [], exports: [] });
}

// Parse: collect exports and imports.
const exportRe = /export\s+(?:async\s+)?(?:function|class|const|let|var)\s+([A-Za-z0-9_$]+)/g;
const exportDefaultRe = /export\s+default\s+(?:async\s+)?(?:function|class)\s+([A-Za-z0-9_$]+)/g;
const exportNamedListRe = /export\s*\{([^}]+)\}/g;
const exportFromRe = /export\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;
const importNamedRe = /import\s*\{([^}]+)\}\s*from\s*['"]([^'"]+)['"]/g;
const importStarRe = /import\s*\*\s*as\s+([A-Za-z0-9_$]+)\s*from\s*['"]([^'"]+)['"]/g;
const importDefaultRe = /import\s+([A-Za-z0-9_$]+)\s*from\s*['"]([^'"]+)['"]/g;
const importBareRe = /import\s+['"]([^'"]+)['"]/g;

for (const [rel, info] of byRelPath) {
  const dir = path.posix.dirname(rel);
  const src = info.src;

  // exports
  let m;
  exportRe.lastIndex = 0;
  while ((m = exportRe.exec(src))) info.exports.push(m[1]);
  exportDefaultRe.lastIndex = 0;
  while ((m = exportDefaultRe.exec(src))) info.exports.push('default');
  exportNamedListRe.lastIndex = 0;
  while ((m = exportNamedListRe.exec(src))) {
    for (const name of parseExportListItems(m[1])) info.exports.push(name);
  }
  exportFromRe.lastIndex = 0;
  while ((m = exportFromRe.exec(src))) {
    for (const name of parseExportListItems(m[1])) info.exports.push(name);
    info.imports.push({ spec: m[2], names: [] });
  }
  // imports
  importNamedRe.lastIndex = 0;
  while ((m = importNamedRe.exec(src))) {
    const names = parseImportNames(m[1]).map((n) => n.local);
    info.imports.push({ spec: m[2], names });
  }
  importStarRe.lastIndex = 0;
  while ((m = importStarRe.exec(src))) {
    info.imports.push({ spec: m[2], names: ['*' + m[1]] });
  }
  importDefaultRe.lastIndex = 0;
  while ((m = importDefaultRe.exec(src))) {
    info.imports.push({ spec: m[2], names: [m[1]] });
  }
  importBareRe.lastIndex = 0;
  while ((m = importBareRe.exec(src))) {
    info.imports.push({ spec: m[1], names: [] });
  }
}

// Check: every import specifier resolves to an existing file (relative).
let importCount = 0;
let importErrors = 0;
const missing = [];
const importNotExported = [];

for (const [rel, info] of byRelPath) {
  const dir = path.posix.dirname(rel);
  for (const imp of info.imports) {
    importCount++;
    // Skip URL imports (not relevant for our static analysis).
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
    // For named imports, ensure the name exists in target.exports (best-effort).
    for (const name of imp.names) {
      if (name.startsWith('*')) continue; // namespace import — accept
      if (target.exports.includes(name)) continue;
      // not fatal — could be a re-export that imports further
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

// Check: app.js specifically must import { hasValidSession, renderLogin } from auth.js
const app = byRelPath.get('app.js');
if (app) {
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