// scripts/smoke-admin-runtime.mjs
// Dynamic-import smoke for the admin UI. Walks every .js under
// release/admin/, stubs minimal browser globals (window, document,
// fetch, sessionStorage, ...), and tries to dynamic-import each
// module. A module that throws at evaluation time (e.g. missing
// re-export, undefined variable, etc.) is reported. Used to catch
// the BLOCKER 1 "static HTML / no form" failure mode where the
// bootstrapper's import graph broke at module-evaluation time and
// boot() never ran.
import { promises as fs } from 'node:fs';
import path from 'node:path';

const ROOT = path.resolve(process.argv[2] || 'release/admin');

// Stub minimal browser globals BEFORE any module import.
globalThis.window = globalThis;
globalThis.__ORVIX_BUILD__ = { tag: 'v1.0.0-rc-test' };
function makeNode() {
  return new Proxy({ style: {}, classList: { add(){}, remove(){}, contains(){return false;} }, dataset: {}, children: [], childNodes: [], attributes: [] }, {
    get(t, k) {
      if (k in t) return t[k];
      if (k === 'tagName') return 'DIV';
      if (k === 'nodeType') return 1;
      // Methods that return the node itself for chaining.
      if (k === 'setAttribute' || k === 'removeAttribute' || k === 'appendChild' || k === 'removeChild' || k === 'addEventListener' || k === 'removeEventListener' || k === 'dispatchEvent' || k === 'insertBefore' || k === 'replaceChild') {
        return () => t;
      }
      if (k === 'matches') return () => false;
      if (k === 'querySelector') return () => makeNode();
      if (k === 'querySelectorAll') return () => [];
      if (k === 'focus' || k === 'click' || k === 'select' || k === 'getBoundingClientRect') return () => undefined;
      if (k === 'cloneNode') return () => makeNode();
      if (k === 'remove') return () => undefined;
      return undefined;
    },
    set(t, k, v) { t[k] = v; return true; },
  });
}
globalThis.document = {
  documentElement: makeNode(),
  body: makeNode(),
  head: makeNode(),
  addEventListener(){},
  removeEventListener(){},
  dispatchEvent(){ return true; },
  getElementById: () => null,
  createElement: () => makeNode(),
  createTextNode: () => ({}),
  createDocumentFragment: () => makeNode(),
  querySelector: () => null,
  querySelectorAll: () => [],
};
function defineGlobal(name, value) {
  try { Object.defineProperty(globalThis, name, { value, configurable: true, writable: true }); }
  catch (_) { try { globalThis[name] = value; } catch (_) { /* read-only host-provided global; skip */ } }
}

defineGlobal('location', { hash: '', search: '', protocol: 'https:', href: 'https://admin.example.test/admin', pathname: '/admin' });
defineGlobal('localStorage', { getItem:()=>null, setItem(){}, removeItem(){} });
defineGlobal('sessionStorage', { getItem:()=>null, setItem(){}, removeItem(){} });
defineGlobal('navigator', { language: 'en', clipboard: null });
defineGlobal('URLSearchParams', class { constructor(s){ this.s=String(s||''); } get(k){ const m=this.s.match(new RegExp('[?&]'+k+'=([^&]*)')); return m?decodeURIComponent(m[1]):null; } });
defineGlobal('fetch', async () => ({ ok:false, status:401, statusText:'Unauthorized', json: async()=>({code:'unauthorized'}), text: async()=>'', headers: { get(){return '';} } }));
defineGlobal('requestAnimationFrame', (cb)=>setTimeout(cb,0));
defineGlobal('cancelAnimationFrame', clearTimeout);
// Node is a real constructor in browsers. Make it a no-op function
// whose `instanceof` returns false so the el() helper's
// `c instanceof Node` check does not throw under our stub.
function StubNode() { return makeNode(); }
StubNode.ELEMENT_NODE = 1;
StubNode.TEXT_NODE = 3;
StubNode.DOCUMENT_FRAGMENT_NODE = 11;
defineGlobal('Node', StubNode);
defineGlobal('NodeFilter', { SHOW_ELEMENT:1, FILTER_ACCEPT:1 });
defineGlobal('CustomEvent', class { constructor(t,d){ this.type=t; this.detail=d?.detail; } });
defineGlobal('Event', globalThis.CustomEvent);

async function walk(dir) {
  const out = [];
  for (const e of await fs.readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) {
      // Skip assets/ — it contains minified/built output that requires
      // browser APIs not available in the stubbed environment.
      if (e.name === 'assets') continue;
      out.push(...await walk(p));
    } else if (e.name.endsWith('.js')) out.push(p);
  }
  return out;
}

function urlFromPath(p) {
  const abs = path.resolve(p).split(path.sep).join('/');
  return 'file:///' + abs.replace(/^\/+/, '');
}

const files = await walk(ROOT);
let ok = 0;
let fail = 0;
const failures = [];
for (const f of files) {
  try {
    await import(urlFromPath(f));
    ok++;
  } catch (e) {
    fail++;
    failures.push({ file: path.relative(ROOT, f), err: (e?.message || String(e)).split('\n')[0] });
  }
}

const GREEN = '\x1b[32m';
const RED = '\x1b[31m';
const RESET = '\x1b[0m';
console.log(`${GREEN}PASS${RESET} runtime-imported admin modules: ${ok}/${files.length}`);
if (fail > 0) {
  console.log(`${RED}FAIL${RESET} ${fail} modules failed to dynamic-import under stubbed globals:`);
  for (const f of failures) console.log(`  - ${f.file}: ${f.err}`);
  process.exit(1);
} else {
  console.log(`${GREEN}OK${RESET} admin module graph loads without runtime errors under stubbed browser globals`);
}