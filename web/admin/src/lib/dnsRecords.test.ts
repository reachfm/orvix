import { describe, expect, it } from "vitest";
import { buildDNSRecordsFile, buildRecordRows } from "./dnsRecords";
import type { DomainDNSHealth } from "../types/dns";

/**
 * These tests pin two properties the reviewer flagged:
 *
 *  1. the record inventory has NO fixed row count — it is composed from
 *     whichever checks the server actually returned, so a domain with more MX
 *     hosts simply gets more rows;
 *  2. every required value shown and exported comes from the server payload,
 *     so the configured SPF/DMARC/SRV expectations reach the UI and the
 *     downloaded file byte-for-byte identically.
 */

const REAL_SPF = "v=spf1 ip4:65.75.203.74 include:spf.orvix.email -all";
const REAL_DMARC = "v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@orvix.email";
const SRV_EXPECTED = "0 0 443 mail.example.com.";

function check(over: Record<string, unknown> = {}) {
  return {
    name: "",
    type: "",
    status: "pass",
    observed: [],
    optional: false,
    checked_at: "2026-01-01T00:00:00Z",
    ...over,
  } as never;
}

function health(over: Partial<DomainDNSHealth> = {}): DomainDNSHealth {
  return {
    domain_id: 1,
    domain_name: "example.com",
    dns_health: "warning",
    health_score: 80,
    complete: true,
    last_checked_at: "2026-01-01T00:00:00Z",
    mx: { status: "pass", expected: "10 mail.example.com", observed: ["mail.example.com:10"] },
    spf: { status: "pass", expected: REAL_SPF, observed: REAL_SPF },
    dkim: { status: "pass", expected: "v=DKIM1; k=rsa; p=KEY", selector: "mail" },
    dmarc: { status: "fail", expected: REAL_DMARC, observed: "" },
    mtasts: { status: "pass", expected: "v=STSv1; id=1", observed: "v=STSv1; id=1" },
    tlsrpt: { status: "pass", expected: "v=TLSRPTv1; rua=mailto:t@example.com", observed: "" },
    mx_hosts: [
      check({ name: "mail.example.com", type: "MX-host", expected: "at least one A or AAAA address" }),
    ],
    ...over,
  } as unknown as DomainDNSHealth;
}

describe("buildRecordRows row composition", () => {
  it("adds exactly one row per published MX host, with no fixed total", () => {
    const one = buildRecordRows(health(), "example.com", null);
    const three = buildRecordRows(
      health({
        mx_hosts: [
          check({ name: "mx1.example.com", type: "MX-host", expected: "at least one A or AAAA address" }),
          check({ name: "mx2.example.com", type: "MX-host", expected: "at least one A or AAAA address" }),
          check({ name: "mx3.example.com", type: "MX-host", expected: "at least one A or AAAA address" }),
        ],
      } as never),
      "example.com",
      null
    );

    expect(three.length).toBe(one.length + 2);
    expect(three.filter((r) => r.type === "MX-host")).toHaveLength(3);
  });

  it("omits rows the server did not return rather than padding to a fixed count", () => {
    const withSRV = buildRecordRows(
      health({ autodiscover_srv: check({ name: "_autodiscover._tcp.example.com", type: "SRV" }) } as never),
      "example.com",
      null
    );
    const withoutSRV = buildRecordRows(health(), "example.com", null);

    expect(withSRV.length).toBe(withoutSRV.length + 1);
    expect(withoutSRV.some((r) => r.key === "autodiscover-srv")).toBe(false);
  });
});

describe("autodiscover SRV row", () => {
  it("carries the server's expectation, observed answers, reason and guidance verbatim", () => {
    const rows = buildRecordRows(
      health({
        autodiscover_srv: check({
          name: "_autodiscover._tcp.example.com",
          type: "SRV",
          status: "warning",
          optional: true,
          expected: SRV_EXPECTED,
          observed: [SRV_EXPECTED, "0 0 443 stray.example.net."],
          reason: "an SRV answer matches the expected endpoint, but additional answers do not",
          guidance: "Optional. Publish an SRV record at _autodiscover._tcp.example.com.",
        }),
      } as never),
      "example.com",
      null
    );

    const srv = rows.find((r) => r.key === "autodiscover-srv");
    expect(srv).toBeDefined();
    expect(srv!.type).toBe("SRV");
    expect(srv!.required).toBe(SRV_EXPECTED);
    // Every RFC 2782 answer is shown, not just the first.
    expect(srv!.observed).toContain("stray.example.net.");
    expect(srv!.status).toBe("warning");
    expect(srv!.optional).toBe(true);
  });
});

describe("buildDNSRecordsFile", () => {
  it("exports the same configured values the rows displayed", () => {
    const rows = buildRecordRows(
      health({ autodiscover_srv: check({ name: "_autodiscover._tcp.example.com", type: "SRV", optional: true, expected: SRV_EXPECTED }) } as never),
      "example.com",
      null
    );
    const file = buildDNSRecordsFile(rows, "example.com", health());

    expect(file).toContain(REAL_SPF);
    expect(file).toContain(REAL_DMARC);
    expect(file).toContain(SRV_EXPECTED);
    // The removed hard-codes must not reappear via a client-side derivation.
    expect(file).not.toContain("v=spf1 mx -all");
    expect(file).not.toContain("dmarc@example.com");
    // And never any secret material.
    expect(file).not.toContain("PRIVATE KEY");
  });
});
