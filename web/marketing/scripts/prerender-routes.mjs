#!/usr/bin/env node
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..");
const dist = resolve(root, "dist");
const data = JSON.parse(await readFile(resolve(root, "src/lib/seo-data.json"), "utf8"));
const template = await readFile(resolve(dist, "index.html"), "utf8");

function escape(value) {
  return value.replaceAll("&", "&amp;").replaceAll('"', "&quot;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

function replaceMeta(html, selector, value) {
  const pattern = new RegExp(`(<meta\\s+${selector}\\s+content=")[^"]*("\\s*/?>)`, "i");
  if (!pattern.test(html)) throw new Error(`template is missing ${selector}`);
  return html.replace(pattern, `$1${escape(value)}$2`);
}

function render(path, meta, noindex = false) {
  const canonical = `${data.siteBaseUrl}${path === "/404" ? "/404" : path}`;
  let html = template
    .replace(/<title>[^<]*<\/title>/i, `<title>${escape(meta.title)}</title>`)
    .replace(/(<link\s+rel="canonical"\s+href=")[^"]*("\s*\/?>)/i, `$1${canonical}$2`);
  html = replaceMeta(html, 'name="description"', meta.description);
  html = replaceMeta(html, 'name="robots"', noindex ? "noindex,nofollow" : "index,follow");
  html = replaceMeta(html, 'property="og:title"', meta.title);
  html = replaceMeta(html, 'property="og:description"', meta.description);
  html = replaceMeta(html, 'property="og:url"', canonical);
  html = replaceMeta(html, 'name="twitter:title"', meta.title);
  html = replaceMeta(html, 'name="twitter:description"', meta.description);
  const structured = JSON.stringify({
    "@context": "https://schema.org",
    "@type": "WebPage",
    name: meta.title,
    description: meta.description,
    url: canonical,
    isPartOf: { "@type": "WebSite", name: data.siteName, url: data.siteBaseUrl },
  }).replaceAll("<", "\\u003c");
  html = html.replace("</head>", `    <script type="application/ld+json" data-orvix-prerender>${structured}</script>\n  </head>`);
  html = html.replace(/<noscript>[\s\S]*?<\/noscript>/i, `<noscript><main><h1>${escape(meta.title)}</h1><p>${escape(meta.description)}</p></main></noscript>`);
  return html;
}

for (const [path, meta] of Object.entries(data.routes)) {
  const target = path === "/" ? resolve(dist, "index.html") : resolve(dist, path.slice(1), "index.html");
  await mkdir(dirname(target), { recursive: true });
  await writeFile(target, render(path, meta), "utf8");
}

await writeFile(resolve(dist, "404.html"), render("/404", {
  title: "Page not found — Orvix",
  description: "The requested Orvix marketing page does not exist.",
}, true), "utf8");

console.log(`[prerender] wrote ${Object.keys(data.routes).length} route documents plus 404.html`);
