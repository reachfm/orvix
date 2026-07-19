#!/usr/bin/env node
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

const utilsPath = new URL('../release/scripts/_parse-utils.mjs', import.meta.url).pathname;
const { parseExportListItems, parseImportNames } = await import(utilsPath);

describe('parseExportListItems — export-list alias resolution', () => {
  it('spaced aliased export: { ie as R, se as a }', () => {
    assert.deepEqual(parseExportListItems('ie as R, se as a'), ['R', 'a']);
  });
  it('minified aliased export without spaces: { oy, dy, c0 }', () => {
    assert.deepEqual(parseExportListItems('oy,dy,c0'), ['oy', 'dy', 'c0']);
  });
  it('minified aliased export with as (no spaces around braces): {oy as ie,dy as se,c0 as ue}', () => {
    assert.deepEqual(parseExportListItems('oy as ie,dy as se,c0 as ue'), ['ie', 'se', 'ue']);
  });
  it('underscore export: { _ as re }', () => {
    assert.deepEqual(parseExportListItems('_ as re'), ['re']);
  });
  it('numeric-suffix export: { c0 }', () => {
    assert.deepEqual(parseExportListItems('c0'), ['c0']);
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
    const result = parseExportListItems('oy as ie,dy as se,c0 as ue,_ as re,fy as Q');
    assert.deepEqual(result, ['ie', 'se', 'ue', 're', 'Q']);
  });
  it('type export: { type Foo }', () => {
    assert.deepEqual(parseExportListItems('type Foo'), ['Foo']);
  });
  it('type export with alias: { type Foo as Bar }', () => {
    assert.deepEqual(parseExportListItems('type Foo as Bar'), ['Bar']);
  });
  it('genuinely missing export still fails', () => {
    const exports = parseExportListItems('present');
    assert.ok(exports.includes('present'));
    assert.ok(!exports.includes('missing'));
  });
});

describe('parseImportNames — import name resolution', () => {
  it('import names with aliases', () => {
    const result = parseImportNames('renderLogin, hasValidSession, foo as bar');
    assert.deepEqual(result.map(n => n.local), ['renderLogin', 'hasValidSession', 'bar']);
  });
  it('import names without aliases', () => {
    const result = parseImportNames('a, b, c');
    assert.deepEqual(result.map(n => n.local), ['a', 'b', 'c']);
  });
  it('skips type markers', () => {
    const result = parseImportNames('foo, type Bar, baz as qux');
    assert.deepEqual(result.map(n => n.local), ['foo', 'qux']);
  });
});
