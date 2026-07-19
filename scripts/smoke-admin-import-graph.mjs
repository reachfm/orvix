#!/usr/bin/env node
import { readFileSync } from "fs";
import { join, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ASSETS = join(__dirname, "..", "release", "admin", "assets");

function parseImports(body) {
  const imports = {};
  const importRe = /import\s*\{([^}]+)\}\s*from\s*["']([^"']+)["']/g;
  for (const m of body.matchAll(importRe)) {
    const specList = m[1];
    const source = m[2];
    const names = specList.split(",").map(s => s.trim()).filter(Boolean);
    for (const n of names) {
      const sourceName = n.includes(" as ") ? n.split(" as ")[0].trim() : n;
      (imports[source] ??= []).push(sourceName);
    }
  }
  return imports;
}

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
  for (const m of body.matchAll(namedRe)) {
    exports.add(m[1]);
  }

  const defaultRe = /export\s+default\s+(?:function|class|async\s+function)\s+(\w+)/g;
  for (const m of body.matchAll(defaultRe)) {
    exports.add(m[1]);
  }

  const starRe = /export\s*\*\s*from\s*["']([^"']+)["']/g;
  for (const m of body.matchAll(starRe)) {
    exports.add("*:" + m[1]);
  }

  return exports;
}

let failed = false;
const files = readFileSync(join(ASSETS, "..", "index.html"), "utf-8");

const chunkRe = /(?:src|href)\s*=\s*["'](?:\.\/assets\/|\/admin\/assets\/)([^"']+)["']/g;
for (const m of files.matchAll(chunkRe)) {
  const filePath = join(ASSETS, m[1]);
  if (!filePath.endsWith(".js")) continue;
  const body = readFileSync(filePath, "utf-8");
  const imports = parseImports(body);

  for (const [source, importedNames] of Object.entries(imports)) {
    let targetBody;
    try {
      const cleanSource = source.replace("./", "");
      targetBody = readFileSync(join(ASSETS, cleanSource), "utf-8");
    } catch {
      console.log(`FAIL target not found: ${m[1]} -> ${source}`);
      failed = true;
      continue;
    }
    const exportedNames = parseExports(targetBody);

    for (const name of importedNames) {
      if (!exportedNames.has(name)) {
        console.log(`FAIL unresolved: assets/${m[1]} -> ${source}:${name}`);
        failed = true;
      }
    }
  }
}

if (!failed) {
  console.log("PASS every imported name is exported by its target module");
}
process.exit(failed ? 1 : 0);
