#!/usr/bin/env node
import http from "http";
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";
import { execSync } from "child_process";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(__dirname, "..", "release", "admin");
const ASSETS = path.join(ROOT, "assets");
const PORT = 9877;

const MIME = {
  ".html": "text/html",
  ".js": "application/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
};

const server = http.createServer((req, res) => {
  const filePath = path.join(ROOT, req.url === "/" ? "/index.html" : req.url);
  const ext = path.extname(filePath);
  try {
    res.writeHead(200, { "Content-Type": MIME[ext] || "application/octet-stream" });
    res.end(fs.readFileSync(filePath));
  } catch {
    res.writeHead(404);
    res.end("Not found");
  }
});

server.listen(PORT, async () => {
  try {
    const res = await fetch(`http://localhost:${PORT}/`);
    if (res.status !== 200) {
      console.log(`FAIL index.html returned HTTP ${res.status}`);
      process.exit(1);
    }
    const html = await res.text();
    console.log("PASS admin index.html loads");

    const chunks = [];
    const re = /(?:src|href)\s*=\s*["'](?:\/admin\/)?assets\/([^"']+)["']/g;
    for (const m of html.matchAll(re)) {
      chunks.push(m[1]);
    }
    console.log(`PASS located ${chunks.length} asset references in index.html`);

    const jsFiles = chunks.filter(c => c.endsWith(".js"));
    console.log(`PASS admin JS files discovered — ${jsFiles.length} files`);

    for (const chunk of chunks) {
      const cres = await fetch(`http://localhost:${PORT}/assets/${chunk}`);
      if (cres.status !== 200) {
        console.log(`FAIL ${chunk} returned HTTP ${cres.status}`);
        process.exit(1);
      }
    }
    console.log("PASS all admin assets serve successfully");

    for (const chunk of jsFiles) {
      const chunkPath = path.join(ASSETS, chunk);
      try {
        execSync(`node --check "${chunkPath}"`, { stdio: "pipe" });
        console.log(`PASS ${chunk} parses (node --check)`);
      } catch (e) {
        console.log(`FAIL ${chunk} parse error`);
        process.exit(1);
      }
    }

    console.log("PASS smoke-admin-browser");
    process.exit(0);
  } catch (e) {
    console.log(`FAIL browser smoke: ${e.message}`);
    process.exit(1);
  } finally {
    server.close();
  }
});
