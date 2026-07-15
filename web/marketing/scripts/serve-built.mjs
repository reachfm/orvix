import { createReadStream, existsSync, statSync } from "node:fs";
import { createServer } from "node:http";
import { extname, join, normalize, resolve } from "node:path";

const root = resolve("dist");
const port = Number(process.env.PORT || 4174);
const types = { ".css": "text/css", ".html": "text/html; charset=utf-8", ".ico": "image/x-icon", ".js": "text/javascript", ".json": "application/json", ".svg": "image/svg+xml", ".txt": "text/plain", ".xml": "application/xml" };

if (!existsSync(join(root, "index.html"))) throw new Error("dist/index.html is missing; run npm run build first");

createServer((request, response) => {
  const pathname = decodeURIComponent(new URL(request.url || "/", "http://localhost").pathname);
  if (pathname === "/api/v1/health") {
    response.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" });
    response.end('{"status":"ok"}');
    return;
  }
  const relative = normalize(pathname).replace(/^([/\\])+/, "");
  let candidate = resolve(root, relative);
  if (!candidate.startsWith(root)) {
    response.writeHead(400).end("Bad request");
    return;
  }
  if (existsSync(candidate) && statSync(candidate).isDirectory()) candidate = join(candidate, "index.html");
  if (!existsSync(candidate) || !statSync(candidate).isFile()) {
    candidate = join(root, "404.html");
    response.statusCode = 404;
  }
  response.setHeader("Content-Type", types[extname(candidate)] || "application/octet-stream");
  response.setHeader("Cache-Control", "no-store");
  createReadStream(candidate).pipe(response);
}).listen(port, "127.0.0.1", () => process.stdout.write(`marketing test server listening on ${port}\n`));
