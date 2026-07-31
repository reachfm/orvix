#!/usr/bin/env node
// Narrow, documented, temporary audit policy for the web/marketing CI job.
//
// Scope of the exception
// ---------------------
// GHSA-qwww-vcr4-c8h2 ("React Router: RSC Mode CSRF Bypass Allows Action
// Execution Before 400 Response", https://github.com/advisories/GHSA-qwww-vcr4-c8h2)
// ONLY affects applications that use the unstable React Server Components
// (RSC) / framework-mode APIs of react-router (createBrowserRouter/RouterProvider
// loaders & actions, react-server-dom, unstable_* APIs).
//
// web/marketing is a static, client-side SPA. It imports only
// `BrowserRouter`, `Route`, `Routes`, `useLocation`, `Link`, and `NavLink`
// from react-router-dom (see src/main.tsx and src/components/*). It does NOT
// import unstable_*, react-server-dom, createBrowserRouter, RouterProvider,
// loaders, actions, or any RSC/framework-mode API. Therefore this advisory
// does not apply to web/marketing's runtime.
//
// THIS EXCEPTION IS TEMPORARY. It expires on 2026-08-31. On or after that date
// this check hard-fails and CI forces reassessment of the advisory. The
// patched release is react-router 8.3.0 (major upgrade, requires product
// review before adoption).
//
// Strict scope guarantees
// -----------------------
//   - Only the exact advisory GHSA-qwww-vcr4-c8h2 on the exact package
//     `react-router` is exempt. react-router-dom is exempt only because its
//     vulnerability is inherited from that same react-router advisory.
//   - react-router is NOT ignored broadly: any OTHER react-router advisory
//     (different GHSA URL) still fails this check.
//   - EVERY other high/critical production dependency vulnerability still
//     fails CI.
//   - This check does NOT use `npm audit --audit-level=critical` or any
//     global severity downgrade; severity is evaluated explicitly here.

import { execSync } from "node:child_process";
import { readFileSync } from "node:fs";

const ALLOWED_GHSA = "GHSA-qwww-vcr4-c8h2";
const ALLOWED_PACKAGE = "react-router";
const ALLOWED_URL = "https://github.com/advisories/GHSA-qwww-vcr4-c8h2";
const EXPIRY = new Date("2026-08-31T23:59:59Z");

// isAllowedAdvisory returns true only for the exact allowlisted react-router
// advisory. The `url` field in npm audit's `via` objects carries the GHSA URL;
// matching the full GHSA id inside it (not the package name alone) prevents a
// broad "ignore react-router" allowlist.
function isAllowedAdvisory(obj) {
  if (!obj || typeof obj !== "object") return false;
  const url = String(obj.url || "");
  const name = String(obj.name || "");
  const sourceUrl = String(obj.source_url || "");
  return (
    name === ALLOWED_PACKAGE &&
    (url === ALLOWED_URL || url.includes(ALLOWED_GHSA) || sourceUrl.includes(ALLOWED_GHSA))
  );
}

// pkgHasOnlyAllowedHighCritical returns true when every high/critical advisory
// origin of a package resolves to the allowlisted react-router advisory.
// Transitive `via` string entries (e.g. react-router-dom -> "react-router")
// are resolved through the vulnerabilities map so a package is exempt only if
// its inherited advisory is the exact allowlisted one.
function pkgHasOnlyAllowedHighCritical(pkgName, vuln, all) {
  const sev = String(vuln.severity || "");
  if (sev !== "high" && sev !== "critical") return true;
  const via = vuln.via;
  if (!Array.isArray(via) || via.length === 0) return false;
  for (const entry of via) {
    if (typeof entry === "string") {
      const source = all[entry];
      if (!source || !pkgHasOnlyAllowedHighCritical(entry, source, all)) return false;
      continue;
    }
    if (!isAllowedAdvisory(entry)) return false;
  }
  return true;
}

function main() {
  // Enforce the review deadline before anything else so CI forces a
  // reassessment on or after the expiry date.
  if (Date.now() > EXPIRY.getTime()) {
    console.error(
      `FATAL: react-router RSC advisory exception (${ALLOWED_GHSA}) has expired. ` +
        "Reassess the advisory; the fixed release is react-router 8.3.0."
    );
    process.exit(1);
  }

  let raw;
  const auditFile = process.argv[2];
  if (auditFile) {
    raw = readFileSync(auditFile, "utf8");
  } else {
    // npm exits non-zero when vulnerabilities exist but still emits the JSON
    // report on stdout; capture stdout in both the success and failure paths.
    try {
      raw = execSync("npm audit --omit=dev --json", {
        encoding: "utf8",
        maxBuffer: 64 * 1024 * 1024,
      });
    } catch (e) {
      if (e.stdout) {
        raw = e.stdout;
      } else {
        console.error("FATAL: npm audit failed without JSON output.", e.stderr || e.message);
        process.exit(1);
      }
    }
  }

  let audit;
  try {
    audit = JSON.parse(raw);
  } catch {
    console.error("FATAL: npm audit did not return valid JSON. Failing closed.");
    process.exit(1);
  }
  if (!audit || typeof audit !== "object" || typeof audit.vulnerabilities !== "object") {
    console.error("FATAL: npm audit output has no vulnerabilities object. Failing closed.");
    process.exit(1);
  }

  const failing = [];
  for (const [name, vuln] of Object.entries(audit.vulnerabilities)) {
    if (!pkgHasOnlyAllowedHighCritical(name, vuln, audit.vulnerabilities)) {
      failing.push({ name, severity: vuln.severity, via: vuln.via });
    }
  }

  if (failing.length > 0) {
    console.error(
      "npm audit (production deps) found high/critical vulnerabilities outside the scoped react-router RSC exception:"
    );
    for (const f of failing) {
      console.error(`  - ${f.name} [${f.severity}] via=${JSON.stringify(f.via)}`);
    }
    process.exit(1);
  }

  console.log(
    `Audit OK: only ${ALLOWED_GHSA} on react-router (RSC-only, not used by web/marketing) is exempt. ` +
      "All other high/critical production vulnerabilities would fail."
  );
  process.exit(0);
}

main();
