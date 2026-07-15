#!/usr/bin/env node
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const data = JSON.parse(await readFile(resolve(root, "src/lib/seo-data.json"), "utf8"));
const urls = Object.keys(data.routes).map((path) => `  <url><loc>${data.siteBaseUrl}${path}</loc></url>`).join("\n");
const xml = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`;
const dist = resolve(root, "dist");
await mkdir(dist, { recursive: true });
await writeFile(resolve(dist, "sitemap.xml"), xml, "utf8");
console.log(`[sitemap] wrote ${Object.keys(data.routes).length} deterministic URLs`);
