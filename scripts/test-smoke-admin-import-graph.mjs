#!/usr/bin/env node
import { describe, it, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath, pathToFileURL } from 'url';
import { dirname, join } from 'path';
import { spawnSync } from 'child_process';
import fs from 'node:fs/promises';
import { readFileSync } from 'fs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const utilsPath = pathToFileURL(join(__dirname, '..', 'release', 'scripts', '_parse-utils.mjs')).href;
const validatorPath = join(__dirname, '..', 'release', 'scripts', 'smoke-admin-import-graph.mjs');
const adminDir = join(__dirname, '..', 'release', 'admin');

const { parseExportListItems, parseImportNames } = await import(utilsPath);

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// SECTION 1 — parseExportListItems  (Defect 1 coverage)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

describe('parseExportListItems — export-list alias resolution', () => {
  it('returns exported name (right of as)', () => {
    assert.deepEqual(parseExportListItems('localName as exportedName'), ['exportedName']);
  });
  it('spaced aliased export: { ie as R, se as a }', () => {
    assert.deepEqual(parseExportListItems('ie as R, se as a'), ['R', 'a']);
  });
  it('minified export without aliases: { oy, dy, c0 }', () => {
    assert.deepEqual(parseExportListItems('oy,dy,c0'), ['oy', 'dy', 'c0']);
  });
  it('minified with as (no spaces around braces): {oy as ie,dy as se,c0 as ue}', () => {
    assert.deepEqual(parseExportListItems('oy as ie,dy as se,c0 as ue'), ['ie', 'se', 'ue']);
  });
  it('underscore export: { _ as re }', () => {
    assert.deepEqual(parseExportListItems('_ as re'), ['re']);
  });
  it('numeric-suffix export: { c0 }', () => {
    assert.deepEqual(parseExportListItems('c0'), ['c0']);
  });
  it('dollar-sign export', () => {
    assert.deepEqual(parseExportListItems('$ as $export'), ['$export']);
  });
  it('multiline export list', () => {
    assert.deepEqual(parseExportListItems('foo,\n  bar as baz,\n  qux'), ['foo', 'baz', 'qux']);
  });
  it('export { foo as default }', () => {
    assert.deepEqual(parseExportListItems('foo as default'), ['default']);
  });
  it('named declaration export: { foo }', () => {
    assert.deepEqual(parseExportListItems('foo'), ['foo']);
  });
  it('re-export list: { ie as R, se as a }', () => {
    assert.deepEqual(parseExportListItems('ie as R, se as a'), ['R', 'a']);
  });
  it('exact vendor pattern: { oy as ie, dy as se, c0 as ue, _ as re, fy as Q }', () => {
    assert.deepEqual(parseExportListItems('oy as ie,dy as se,c0 as ue,_ as re,fy as Q'), ['ie', 'se', 'ue', 're', 'Q']);
  });
  it('type export: { type Foo }', () => {
    assert.deepEqual(parseExportListItems('type Foo'), ['Foo']);
  });
  it('type export with alias: { type Foo as Bar }', () => {
    assert.deepEqual(parseExportListItems('type Foo as Bar'), ['Bar']);
  });
});

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// SECTION 2 — parseImportNames  (Defect 2, 3 coverage)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

describe('parseImportNames — import name resolution', () => {
  // Defect 2: named import must return imported (source) name for cross-module checks
  it('named import without alias: { r } → imported=r, local=r', () => {
    const result = parseImportNames('r');
    assert.equal(result.length, 1);
    assert.equal(result[0].imported, 'r');
    assert.equal(result[0].local, 'r');
    assert.equal(result[0].kind, 'named');
  });

  it('named import with alias: { r as oy } → imported=r, local=oy', () => {
    const result = parseImportNames('r as oy');
    assert.equal(result.length, 1);
    assert.equal(result[0].imported, 'r');
    assert.equal(result[0].local, 'oy');
    assert.equal(result[0].kind, 'named');
  });

  it('multiple aliased imports: { r as oy, a as dy, g as c0 }', () => {
    const result = parseImportNames('r as oy, a as dy, g as c0');
    assert.equal(result.length, 3);
    assert.deepEqual(result.map(n => n.imported), ['r', 'a', 'g']);
    assert.deepEqual(result.map(n => n.local), ['oy', 'dy', 'c0']);
  });

  it('skips type markers', () => {
    const result = parseImportNames('foo, type Bar, baz as qux');
    assert.deepEqual(result.map(n => n.local), ['foo', 'qux']);
  });

  it('source names for exports check — exact failed pattern', () => {
    // index.js: import { r as oy, a as dy, g as c0, b as _, R as fy }
    // from "./vendor-NIgv9UGi.js";
    const result = parseImportNames('r as oy, a as dy, g as c0, b as _, R as fy');
    assert.deepEqual(result.map(n => n.imported), ['r', 'a', 'g', 'b', 'R']);
    assert.deepEqual(result.map(n => n.local), ['oy', 'dy', 'c0', '_', 'fy']);
  });
});

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// SECTION 3 — Negative assertions (Defect 2 negative)
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

describe('negative assertions — local names must NOT be searched in target', () => {
  it('oy, dy, c0, _, fy NOT found in vendor export set', () => {
    const vendorExports = parseExportListItems('ie as R,se as a,ue as b,re as g,Q as r');
    // Vendor exports: R, a, b, g, r
    const localNames = ['oy', 'dy', 'c0', '_', 'fy'];
    const sourceNames = ['r', 'a', 'g', 'b', 'R'];
    for (const local of localNames) {
      assert.ok(!vendorExports.includes(local),
        `local name '${local}' must NOT be in vendor exports`);
    }
    for (const source of sourceNames) {
      assert.ok(vendorExports.includes(source),
        `source name '${source}' MUST be in vendor exports`);
    }
  });
});

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// SECTION 4 — End-to-end validator against fixtures
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

describe('end-to-end validator — fixture-based', () => {
  // Build test fixture directories and run the validator against them.

  const fixturesDir = join(__dirname, '..', 'tmp', 'test-fixtures');

  async function runValidator(fixtureName) {
    const fixturePath = join(fixturesDir, fixtureName);
    const result = spawnSync(process.execPath, [validatorPath, fixturePath], { stdio: 'pipe' });
    return { stdout: result.stdout.toString(), stderr: result.stderr.toString(), status: result.status };
  }

  async function writeFixture(fixtureName, files) {
    const fixturePath = join(fixturesDir, fixtureName);
    await fs.mkdir(fixturePath, { recursive: true });
    for (const [relPath, content] of Object.entries(files)) {
      const fullPath = join(fixturePath, relPath);
      await fs.mkdir(dirname(fullPath), { recursive: true });
      await fs.writeFile(fullPath, content);
    }
  }

  // Set up fixtures before all tests
  before(async () => {
    await fs.rm(fixturesDir, { recursive: true, force: true }).catch(() => {});

  // Fixture 1: basic named import/export
  await writeFixture('basic-named', {
    'app.js': `import { foo } from './lib.js';`,
    'lib.js': `export function foo() {}`,
  });

  // Fixture 2: aliased import (Defect 2 test)
  await writeFixture('aliased-import', {
    'index.js': `import { r as oy, a as dy } from './vendor.js';`,
    'vendor.js': `export { ie as r, se as a };`,
  });

  // Fixture 3: default import (Defect 3 test)
  await writeFixture('default-import', {
    'app.js': `import React from './react.js';`,
    'react.js': `export default function React() {}`,
  });

  // Fixture 4: default export alias
  await writeFixture('default-alias', {
    'app.js': `import { default as App } from './components.js';`,
    'components.js': `export default function App() {}`,
  });

  // Fixture 5: namespace import
  await writeFixture('namespace-import', {
    'app.js': `import * as Vendor from './vendor.js';`,
    'vendor.js': `export const x = 1;`,
  });

  // Fixture 6: bare/side-effect import
  await writeFixture('bare-import', {
    'app.js': `import './polyfill.js';`,
    'polyfill.js': `Array.prototype.foo = function() {};`,
  });

  // Fixture 7: re-export
  await writeFixture('reexport', {
    'index.js': `export { foo as bar } from './lib.js';`,
    'lib.js': `export function foo() {}`,
  });

  // Fixture 8: exact failed production pattern
  await writeFixture('exact-failed', {
    'index.js': `import { r as oy, a as dy, g as c0, b as _, R as fy } from './vendor-NIgv9UGi.js';`,
    'vendor-NIgv9UGi.js': `export { Q as r, se as a, re as g, ue as b, ie as R };`,
  });

  // Fixture 9: missing export
  await writeFixture('missing-export', {
    'app.js': `import { missing } from './lib.js';`,
    'lib.js': `export { present };`,
  });

  // Fixture 10: missing default export
  await writeFixture('missing-default', {
    'app.js': `import React from './react.js';`,
    'react.js': `export const React = {};`,
  });

  // Fixture 11: combined default + named import
  await writeFixture('combined-import', {
    'app.js': `import React, { useState } from './react.js';`,
    'react.js': `export default function React() {}; export function useState() {};`,
  });

  // Fixture 12: minified with no optional spaces
  await writeFixture('minified-nospace', {
    'index.js': `import{r as oy,a as dy}from'./vendor.js';`,
    'vendor.js': `export{ie as r,se as a};`,
  });

  // Fixture 13: multiline import
  await writeFixture('multiline', {
    'app.js': `import {\n  foo,\n  bar as baz\n} from './lib.js';`,
    'lib.js': `export function foo() {}; export function bar() {};`,
  });
  });

  it('basic named import passes', async () => {
    const result = await runValidator('basic-named');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('aliased import uses source name (Defect 2 fix)', async () => {
    const result = await runValidator('aliased-import');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
    // Negative: should NOT search for local names oy, dy
    assert.ok(!result.stdout.includes('unresolved'));
  });

  it('default import checks for "default" (Defect 3 fix)', async () => {
    const result = await runValidator('default-import');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('default alias import', async () => {
    const result = await runValidator('default-alias');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('namespace import passes', async () => {
    const result = await runValidator('namespace-import');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('bare import passes', async () => {
    const result = await runValidator('bare-import');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('re-export passes', async () => {
    const result = await runValidator('reexport');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('exact failed production pattern PASSES', async () => {
    const result = await runValidator('exact-failed');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
    // Negative: should NOT find unresolved
    assert.ok(!result.stdout.includes('unresolved'), 'should not have unresolved imports');
  });

  it('genuinely missing named export FAILS', async () => {
    const result = await runValidator('missing-export');
    assert.notEqual(result.status, 0, 'should fail with exit code 1');
    assert.ok(result.stdout.includes('unresolved'));
    assert.ok(result.stdout.includes('missing'));
  });

  it('genuinely missing default FAILS', async () => {
    const result = await runValidator('missing-default');
    assert.notEqual(result.status, 0, 'should fail with exit code 1');
    assert.ok(result.stdout.includes('unresolved'));
    assert.ok(result.stdout.includes('default'));
  });

  it('combined default + named import', async () => {
    const result = await runValidator('combined-import');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('minified with no optional spaces', async () => {
    const result = await runValidator('minified-nospace');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

  it('multiline import/export', async () => {
    const result = await runValidator('multiline');
    assert.equal(result.status, 0, `stdout: ${result.stdout}\nstderr: ${result.stderr}`);
    assert.ok(result.stdout.includes('all names resolved'));
  });

after(async () => {
  await fs.rm(fixturesDir, { recursive: true, force: true }).catch(() => {});
});
});

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// SECTION 5 — Drift guard: scripts/ must delegate to release/
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

describe('drift guard — duplicate parser detection', () => {
  it('scripts/smoke-admin-import-graph.mjs delegates to release copy', () => {
    const wrapperPath = join(__dirname, '..', 'scripts', 'smoke-admin-import-graph.mjs');
    const content = readFileSync(wrapperPath, 'utf8');
    // Must reference the release copy; must not contain its own parser logic
    // (Either literal string or path.join with individual segments is acceptable)
    const refsRelease = content.includes('release/scripts/smoke-admin-import-graph.mjs') ||
      content.includes("'release', 'scripts', 'smoke-admin-import-graph.mjs'");
    assert.ok(refsRelease, 'scripts copy must delegate to release/scripts copy');
    // Must not have its own parseExports or parseNames function
    assert.ok(!content.includes('function parseExports'),
      'scripts copy must not contain its own parseExports');
    assert.ok(!content.includes('function parseNames'),
      'scripts copy must not contain its own parseNames');
  });

  it('release/scripts/_parse-utils.mjs is the only parser implementation', () => {
    // Verify that only the canonical files contain the parser implementation
    const releaseUtilsPath = join(__dirname, '..', 'release', 'scripts', '_parse-utils.mjs');
    const content = readFileSync(releaseUtilsPath, 'utf8');
    assert.ok(content.includes('parseExportListItems'));
    assert.ok(content.includes('parseImportNames'));
  });
});
