#!/usr/bin/env node
import { readFileSync } from "fs";
import { join, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));

function parseExports(body) {
  const exports = new Set();
  const exportListRe = /export\s*\{([^}]+)\}/g;
  for (const m of body.matchAll(exportListRe)) {
    for (const spec of m[1].split(",")) {
      const s = spec.trim();
      if (!s) continue;
      const exportedName = s.includes(" as ") ? s.split(" as ")[1].trim() : s;
      exports.add(exportedName);
    }
  }
  const namedRe = /export\s+(?:const|let|var|function|class|async\s+function)\s+(\w+)/g;
  for (const m of body.matchAll(namedRe)) exports.add(m[1]);
  const defaultRe = /export\s+default\s+(?:function|class|async\s+function)\s+(\w+)/g;
  for (const m of body.matchAll(defaultRe)) exports.add(m[1]);
  const starRe = /export\s*\*\s*from\s*["']([^"']+)["']/g;
  for (const m of body.matchAll(starRe)) exports.add("*:" + m[1]);
  return exports;
}

let code = 0;

function test(name, body, expectedExports) {
  const actual = parseExports(body);
  const expected = new Set(expectedExports);
  const missing = [...expected].filter(e => !actual.has(e));
  const extra = [...actual].filter(a => !expected.has(a));
  if (missing.length || extra.length) {
    console.log(`FAIL ${name}`);
    if (missing.length) console.log(`  missing: ${missing.join(", ")}`);
    if (extra.length) console.log(`  extra: ${extra.join(", ")}`);
    code = 1;
  } else {
    console.log(`PASS ${name}`);
  }
}

// Fixture 1: aliased named exports
test("aliased named exports",
  `export { foo as bar, baz as qux };`,
  ["bar", "qux"]
);

// Fixture 2: minified one-character exports
test("minified one-character exports",
  `export{a as oy,b as dy,c as c0,d as _,e as fy};`,
  ["oy", "dy", "c0", "_", "fy"]
);

// Fixture 3: underscore export
test("underscore export",
  `export{__dirname as _};`,
  ["_"]
);

// Fixture 4: numeric suffix export
test("numeric suffix export c0",
  `export{component as c0};`,
  ["c0"]
);

// Fixture 5: multiline export lists
test("multiline export lists",
  `export {\n  foo as bar,\n  baz as qux\n};`,
  ["bar", "qux"]
);

// Fixture 6: re-exports
test("re-exports",
  `export { foo } from "./bar";`,
  ["foo"]
);

// Fixture 7: mixed named and list exports
test("mixed named and list exports",
  `export const hello = "world";\nexport { greet as salutation };`,
  ["hello", "salutation"]
);

// Fixture 8: default function export
test("default function export",
  `export default function App() {}`,
  ["App"]
);

// Fixture 9: genuinely missing export still fails
{
  const body = `import { missing } from "./target";`;
  const imports = /import\s*\{([^}]+)\}\s*from\s*["']([^"']+)["']/g;
  const m = imports.exec(body);
  const importName = m[1].trim();
  const exports = parseExports(`export { present };`);
  if (!exports.has(importName)) {
    console.log("PASS genuinely missing export correctly detected");
  } else {
    console.log("FAIL genuinely missing export not detected");
    code = 1;
  }
}

// Fixture 10: real vendor export pattern (minified aliased export list)
test("real vendor export pattern",
  `export{ie as R,se as a,ue as b,re as g,Q as r};`,
  ["R", "a", "b", "g", "r"]
);

process.exit(code);
