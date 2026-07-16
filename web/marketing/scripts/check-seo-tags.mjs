#!/usr/bin/env node
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const data = JSON.parse(await readFile(resolve(root, "src/lib/seo-data.json"), "utf8"));
const seenTitles = new Set();
const seenDescriptions = new Set();

for (const [path, meta] of Object.entries(data.routes)) {
  const file = path === "/" ? resolve(root, "dist/index.html") : resolve(root, "dist", path.slice(1), "index.html");
  const html = await readFile(file, "utf8");
  const canonical = `${data.siteBaseUrl}${path}`;
  const required = [
    `<title>${meta.title}</title>`,
    `name="description" content="${meta.description.replaceAll("&", "&amp;").replaceAll('"', "&quot;")}"`,
    `rel="canonical" href="${canonical}"`,
    `property="og:title" content="${meta.title}`,
    `property="og:url" content="${canonical}"`,
    `data-orvix-prerender`,
  ];
  for (const token of required) {
    if (!html.includes(token)) throw new Error(`${file} is missing ${token}`);
  }
  if (seenTitles.has(meta.title)) throw new Error(`duplicate title: ${meta.title}`);
  if (seenDescriptions.has(meta.description)) throw new Error(`duplicate description: ${meta.description}`);
  seenTitles.add(meta.title);
  seenDescriptions.add(meta.description);
}

const notFound = await readFile(resolve(root, "dist/404.html"), "utf8");
if (!notFound.includes("noindex,nofollow") || !notFound.includes("Page not found — Orvix")) {
  throw new Error("404.html must be route-specific and noindex");
}
const sitemap = await readFile(resolve(root, "dist/sitemap.xml"), "utf8");
for (const path of Object.keys(data.routes)) {
  if (!sitemap.includes(`<loc>${data.siteBaseUrl}${path}</loc>`)) throw new Error(`sitemap missing ${path}`);
}
console.log(`[seo] verified unique, route-specific production HTML for ${seenTitles.size} routes plus 404`);
