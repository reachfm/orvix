import type {
  DomainDNSHealth, DNSHealthCheck, DNSRecordRow, DKIMResult,
} from "../types/dns";

/** Shared React Query cache key for a domain's DNS health payload. */
export function dnsQueryKey(domainID: number): readonly unknown[] {
  return ["enterprise-dns", domainID] as const;
}

/** MXCheck.observed is a []string; every other check's is a plain string. */
function observedText(check: DNSHealthCheck | null | undefined): string {
  if (!check) return "";
  const o = check.observed;
  if (Array.isArray(o)) return o.join(", ");
  return o || "";
}

/**
 * Splits a leading MX preference off an expected MX value. The backend stores
 * the expected MX as a hostname (optionally "10 mail.example.com"); when no
 * preference is present we surface the conventional single-host value of 10,
 * which is what the generated zone file uses.
 */
function splitMXPriority(expected: string): { host: string; priority: number } {
  const m = /^\s*(\d{1,5})\s+(\S+)\s*$/.exec(expected || "");
  if (m) return { host: m[2], priority: Number(m[1]) };
  return { host: (expected || "").trim(), priority: 10 };
}

/**
 * A record that the backend did not check at all comes back as null. It must
 * render as "unknown"/Not checked — never as a pass — which is why status is
 * taken verbatim from the check and defaults to "unknown" when absent.
 */
function statusOf(check: DNSHealthCheck | null | undefined): string {
  return check?.status || "unknown";
}

/**
 * Builds the modal's record inventory from a single EnterpriseDNSHealth object.
 *
 * Every row's `status` and `reason` come straight from the server payload —
 * this function never infers pass/fail. `dkimPending` is the result of a
 * just-completed generate/rotate: its freshly published key has definitionally
 * not propagated yet, so that row is forced to "pending" rather than reusing
 * the pre-rotation check result, which would be misleading.
 */
export function buildRecordRows(
  health: DomainDNSHealth | undefined,
  domainName: string,
  dkimPending?: DKIMResult | null
): DNSRecordRow[] {
  if (!health) return [];
  const rows: DNSRecordRow[] = [];
  const root = domainName || health.domain_name || "";

  // ── MX ──
  const mx = health.mx;
  const { host, priority } = splitMXPriority(mx?.expected || "");
  rows.push({
    key: "mx",
    name: root || "@",
    type: "MX",
    required: host,
    observed: observedText(mx),
    status: statusOf(mx),
    reason: mx?.reason || "",
    priority,
  });

  // ── SPF ──
  rows.push({
    key: "spf",
    name: root || "@",
    type: "TXT (SPF)",
    required: health.spf?.expected || "",
    observed: observedText(health.spf),
    status: statusOf(health.spf),
    reason: health.spf?.reason || "",
  });

  // ── DKIM ──
  const dkim = health.dkim;
  const dkimName =
    dkimPending?.dns_record_name ||
    dkim?.record_name ||
    (dkim?.selector ? `${dkim.selector}._domainkey.${root}` : `_domainkey.${root}`);
  rows.push({
    key: "dkim",
    name: dkimName,
    type: "TXT (DKIM)",
    required: dkimPending?.public_dns_txt || dkim?.public_txt || dkim?.expected || "",
    observed: dkimPending ? "" : observedText(dkim),
    status: dkimPending ? "pending" : statusOf(dkim),
    reason: dkimPending
      ? "New key generated — publish this record; propagation can take up to 48 hours."
      : dkim?.reason || "",
  });

  // ── DMARC ──
  rows.push({
    key: "dmarc",
    name: `_dmarc.${root}`,
    type: "TXT (DMARC)",
    required: health.dmarc?.expected || "",
    observed: observedText(health.dmarc),
    status: statusOf(health.dmarc),
    reason: health.dmarc?.reason || "",
  });

  // ── MTA-STS TXT ──
  rows.push({
    key: "mtasts",
    name: `_mta-sts.${root}`,
    type: "TXT (MTA-STS)",
    required: health.mtasts?.expected || "v=STSv1; id=<policy-id>",
    observed: observedText(health.mtasts),
    status: statusOf(health.mtasts),
    reason: health.mtasts?.reason || "",
  });

  // ── MTA-STS HTTPS policy document (only when the backend fetched one) ──
  const policy = health.mtasts_policy;
  if (policy) {
    const detail = [
      policy.mode ? `mode: ${policy.mode}` : "",
      policy.max_age ? `max_age: ${policy.max_age}` : "",
      policy.mx && policy.mx.length ? `mx: ${policy.mx.join(", ")}` : "",
    ]
      .filter(Boolean)
      .join("; ");
    rows.push({
      key: "mtasts-policy",
      name: `https://mta-sts.${root}/.well-known/mta-sts.txt`,
      type: "HTTPS policy",
      required: "version: STSv1 (policy document served over HTTPS)",
      observed: detail || policy.raw || "",
      status: policy.valid ? "pass" : "fail",
      reason: policy.error || "",
    });
  }

  // ── TLS-RPT ──
  rows.push({
    key: "tlsrpt",
    name: `_smtp._tls.${root}`,
    type: "TXT (TLS-RPT)",
    required: health.tlsrpt?.expected || "v=TLSRPTv1; rua=mailto:<reporting-address>",
    observed: observedText(health.tlsrpt),
    status: statusOf(health.tlsrpt),
    reason: health.tlsrpt?.reason || "",
  });

  return rows;
}

/**
 * Renders the modal's already-loaded rows as a plain-text zone summary.
 *
 * Generated entirely client-side from state already on screen — no extra API
 * call. It emits ONLY record name, type, priority and the required public
 * value. It must never contain DKIM private key material (which the API never
 * returns in the first place), session tokens, or internal database IDs.
 */
export function buildDNSRecordsFile(
  rows: DNSRecordRow[],
  domainName: string,
  health?: DomainDNSHealth
): string {
  const lines: string[] = [];
  lines.push(`DNS records for ${domainName}`);
  lines.push(`Generated ${new Date().toISOString()}`);
  if (health) {
    lines.push(
      `Last check: ${health.last_checked_at || "never"} | health: ${health.dns_health} | score: ${health.health_score}% | complete: ${health.complete}`
    );
    if (!health.complete) {
      lines.push(
        "WARNING: the last check was incomplete — not every record was verified."
      );
    }
  }
  lines.push("");
  lines.push("NAME | TYPE | PRIORITY | REQUIRED VALUE");
  lines.push("-".repeat(72));
  for (const r of rows) {
    lines.push(
      [r.name, r.type, r.priority != null ? String(r.priority) : "-", r.required || "-"].join(" | ")
    );
  }
  lines.push("");
  lines.push(
    "This file contains public DNS record data only. DKIM private keys are never exported."
  );
  return lines.join("\n");
}
