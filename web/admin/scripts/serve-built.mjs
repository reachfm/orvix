import { createServer } from "node:http";
import { readFileSync, existsSync, statSync } from "node:fs";
import { join, extname } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = fileURLToPath(new URL(".", import.meta.url));
const DIST = join(__dirname, "..", "dist");

const PORT = parseInt(process.env.PORT || "4175", 10);
const HOST = "127.0.0.1";

const MIME_MAP = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "application/javascript; charset=utf-8",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
};

function mimeType(filePath) {
  return MIME_MAP[extname(filePath).toLowerCase()] || "application/octet-stream";
}

function serveFile(res, filePath) {
  const data = readFileSync(filePath);
  res.writeHead(200, {
    "Content-Type": mimeType(filePath),
    "Content-Length": data.length,
  });
  res.end(data);
}

function serveNotFound(res) {
  res.writeHead(404, { "Content-Type": "text/plain" });
  res.end("404 Not Found");
}

function isPathSafe(requestPath) {
  const normalized = requestPath.replace(/\\/g, "/");
  return !normalized.includes("..") && !normalized.includes("\0");
}

const server = createServer((req, res) => {
  const url = new URL(req.url || "/", `http://${HOST}:${PORT}`);
  let pathname = url.pathname;

  // Mock API response for unauthenticated state
  if (pathname === "/api/v1/me") {
    res.writeHead(401, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ error: "unauthenticated" }));
    return;
  }

  if (!isPathSafe(pathname)) {
    serveNotFound(res);
    return;
  }

  // Admin SPA deep links — serve index.html for known auth paths
  const ADMIN_AUTH_PATHS = [
    "/admin",
    "/admin/",
    "/admin/login",
    "/admin/signup",
    "/admin/forgot-password",
    "/admin/reset-password",
    "/admin/dashboard",
  ];

  if (ADMIN_AUTH_PATHS.includes(pathname)) {
    const indexPath = join(DIST, "index.html");
    if (existsSync(indexPath)) {
      serveFile(res, indexPath);
      return;
    }
  }

  // Static asset under /admin/assets/
  if (pathname.startsWith("/admin/")) {
    let relative = pathname;
    // Rewrite /admin/assets/xxx to dist/assets/xxx
    if (pathname.startsWith("/admin/assets/")) {
      relative = "/assets/" + pathname.slice("/admin/assets/".length);
    }
    const safe = relative.replace(/\\/g, "/").replace(/^\/+/, "");
    const filePath = join(DIST, safe);
    if (!isPathSafe(filePath)) {
      serveNotFound(res);
      return;
    }
    if (existsSync(filePath) && statSync(filePath).isFile()) {
      serveFile(res, filePath);
      return;
    }
  }

  serveNotFound(res);
});

server.listen(PORT, HOST, () => {
  console.log(`Admin test server listening on http://${HOST}:${PORT}`);
});
