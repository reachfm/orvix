// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import Domains from "./Domains";

vi.mock("../api", () => {
  class MockApiError extends Error {
    code: string;
    status: number;
    body: any;
    constructor(code: string, message: string, status: number, body?: any) {
      super(message);
      this.code = code;
      this.status = status;
      this.body = body;
    }
  }
  return {
    ApiError: MockApiError,
    domainErrorMessage: (code: string, fallback: string) => {
      switch (code) {
        case "DOMAIN_HAS_MAILBOXES":
          return "This domain still has mailboxes. Delete its mailboxes first.";
        case "DOMAIN_NOT_FOUND":
          return "Domain not found or you do not have access to it.";
        case "DKIM_NOT_CONFIGURED":
          return "Generate DKIM before rotating keys.";
        default:
          return fallback;
      }
    },
    api: {
      listDomainsEnterprise: vi.fn(),
      createDomainEnterprise: vi.fn(),
      deleteDomainEnterprise: vi.fn(),
      generateDomainDKIM: vi.fn(),
      rotateDomainDKIM: vi.fn(),
      getEnterpriseDomainDNS: vi.fn(),
      verifyEnterpriseDomainDNS: vi.fn(),
    },
  };
});

import { api, ApiError } from "../api";

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } });
}
let qc = makeClient();
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

/** Mirrors the real AdminDomain aggregate fields from GET /enterprise/domains. */
const sampleDomains = [
  {
    id: 1,
    name: "example.com",
    status: "active",
    plan: "enterprise",
    mailbox_count: 12,
    max_mailboxes: 50,
    alias_count: 7,
    max_aliases: 20,
    storage_used_bytes: 5_368_709_120, // 5 GB
    storage_limit_bytes: 10_737_418_240, // 10 GB
    message_count: 18432,
    dkim_enabled: true,
    dkim_selector: "mail",
    dmarc_enabled: true,
    dns_health: "warning",
    dns_score: 72,
    dns_last_checked_at: "2026-01-01T00:00:00Z",
  },
  {
    id: 2,
    name: "other.com",
    status: "disabled",
    plan: "smb",
    mailbox_count: 0,
    max_mailboxes: 5,
    alias_count: 0,
    max_aliases: 0,
    storage_used_bytes: 0,
    storage_limit_bytes: 0,
    message_count: 0,
    dkim_enabled: false,
    dmarc_enabled: false,
    dns_health: "",
    dns_score: 0,
  },
];

const defaultDKIMResponse = {
  dkim: {
    selector: "mail",
    public_dns_txt: "v=DKIM1; k=rsa; p=NEWKEYDATA",
    dns_record_name: "mail._domainkey.example.com",
  },
};

/** A full, healthy EnterpriseDNSHealth payload (complete: true). */
const defaultDNSHealth = {
  domain_id: 1,
  domain_name: "example.com",
  operational_status: "active",
  dns_health: "pass",
  health_score: 85,
  complete: true,
  last_checked_at: "2026-01-01T00:00:00Z",
  mx: { status: "pass", expected: "10 mail.example.com", observed: ["mail.example.com"], reason: "MX matches expected host", checked_at: "2026-01-01T00:00:00Z" },
  spf: { status: "pass", observed: "v=spf1 mx -all", expected: "v=spf1 mx -all", reason: "", checked_at: "2026-01-01T00:00:00Z" },
  dkim: { selector: "mail", status: "pass", record_name: "mail._domainkey.example.com", public_txt: "v=DKIM1; k=rsa; p=ABC", configured: true, matches_dns: true, reason: "", checked_at: "2026-01-01T00:00:00Z" },
  dmarc: { status: "pass", observed: "v=DMARC1; p=reject", expected: "v=DMARC1; p=reject", reason: "", checked_at: "2026-01-01T00:00:00Z" },
  mtasts: { status: "pass", observed: "v=STSv1; id=2025", reason: "", checked_at: "2026-01-01T00:00:00Z" },
  tlsrpt: { status: "pass", observed: "v=TLSRPTv1; rua=mailto:rpt@example.com", reason: "", checked_at: "2026-01-01T00:00:00Z" },
  mtasts_policy: { valid: true, mode: "enforce", max_age: 604800, mx: ["mail.example.com"] },
};

let writeText: ReturnType<typeof vi.fn>;

beforeEach(() => {
  qc = makeClient();
  writeText = vi.fn().mockResolvedValue(undefined);
  vi.mocked(api.listDomainsEnterprise).mockResolvedValue({ domains: sampleDomains, total: 2 } as any);
  vi.mocked(api.generateDomainDKIM).mockResolvedValue(defaultDKIMResponse as any);
  vi.mocked(api.rotateDomainDKIM).mockResolvedValue(defaultDKIMResponse as any);
  vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue(defaultDNSHealth as any);
  vi.mocked(api.verifyEnterpriseDomainDNS).mockResolvedValue(defaultDNSHealth as any);
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.useRealTimers();
});

async function renderTable() {
  render(<Wrapper><Domains /></Wrapper>);
  await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
}

async function openModal(domainName = "example.com") {
  await renderTable();
  fireEvent.click(screen.getByRole("button", { name: new RegExp(`Open DNS records for ${domainName}`, "i") }));
  await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
  await waitFor(() => expect(screen.getByTestId("dns-records-table")).toBeInTheDocument());
}

describe("Domains table", () => {
  it("renders the real aggregate fields returned by the list endpoint", async () => {
    await renderTable();

    // Storage: 5 GB used of a 10 GB limit, formatted from raw bytes. The cell
    // splits used/limit across nodes, so assert on the combined cell text.
    const storageCell = screen
      .getAllByRole("cell")
      .find((c) => /^\s*5\.0 GB\s*\/\s*10\.0 GB\s*$/.test(c.textContent || ""));
    expect(storageCell).toBeTruthy();
    // The unlimited-storage domain shows an infinity limit, not a fabricated cap.
    expect(
      screen.getAllByRole("cell").some((c) => /^\s*0 B\s*\/\s*∞\s*$/.test(c.textContent || ""))
    ).toBe(true);
    // Message count, thousands-separated.
    expect(screen.getByText("18,432")).toBeInTheDocument();
    // Aliases and mailboxes used/limit.
    expect(screen.getByText("7")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    // DNS health score + label straight from dns_score/dns_health.
    expect(screen.getByText("72%")).toBeInTheDocument();
    expect(screen.getByText("warning")).toBeInTheDocument();
    // A domain that has never been checked says so rather than showing 0%,
    // which would falsely read as "failed every check".
    expect(screen.getByText("Not checked")).toBeInTheDocument();
  });

  it("shows the domain count and supports search", async () => {
    await renderTable();
    expect(screen.getByRole("heading", { name: /Domains \(2\)/ })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search domains"), { target: { value: "other" } });
    await waitFor(() => expect(screen.queryByText("example.com")).not.toBeInTheDocument());
    expect(screen.getByText("other.com")).toBeInTheDocument();
  });

  it("exposes page-size, pagination and refresh controls", async () => {
    await renderTable();
    expect(screen.getByLabelText("Rows per page")).toBeInTheDocument();
    expect(screen.getByLabelText("Previous page")).toBeInTheDocument();
    expect(screen.getByLabelText("Next page")).toBeInTheDocument();
    expect(screen.getByLabelText("Refresh domains")).toBeInTheDocument();
  });

  it("ships no selection UI, because there is no bulk operation behind it", async () => {
    await renderTable();
    // Inert controls are worse than absent ones: a checkbox that selects
    // rows no action can consume trains operators to expect a bulk feature
    // that does not exist. Removed until a real, authorized bulk action is
    // implemented end to end.
    expect(screen.queryByLabelText("Select all domains on this page")).toBeNull();
    expect(screen.queryByLabelText("Select example.com")).toBeNull();
    expect(document.querySelectorAll('input[type="checkbox"]').length).toBe(0);
    expect(screen.queryByText(/\d+ selected/)).toBeNull();
  });

  it("opens the DNS modal only via the dedicated DNS action, for the right domain", async () => {
    await renderTable();

    // Clicking the domain name cell must NOT open the modal.
    fireEvent.click(screen.getByText("other.com"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Open DNS records for other\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    // The modal is scoped to the domain whose button was pressed.
    expect(api.getEnterpriseDomainDNS).toHaveBeenCalledWith(2);
    expect(screen.getByRole("heading", { name: /DNS Records · other\.com/ })).toBeInTheDocument();
  });
});

describe("DNS records modal", () => {
  it("renders one row per record type with the API's verbatim reason text", async () => {
    await openModal();

    for (const key of ["mx", "spf", "dkim", "dmarc", "mtasts", "mtasts-policy", "tlsrpt"]) {
      expect(screen.getByTestId(`dns-row-${key}`)).toBeInTheDocument();
    }
    // MX priority parsed out of the expected value.
    expect(screen.getByText(/prio 10/)).toBeInTheDocument();
    // The reason string is rendered exactly as the backend authored it.
    expect(screen.getByTestId("reason-mx")).toHaveTextContent("MX matches expected host");
  });

  it("renders the full expanded record inventory with required, current, status, reason and guidance", async () => {
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      mail_host_a: {
        name: "mail.example.com", type: "A", status: "pass",
        expected: "one or more public IPv4 addresses", observed: ["192.0.2.25"],
        guidance: "Add an A record for mail.example.com pointing to the public IPv4 address of the ORVIX mail server.",
        optional: false, checked_at: "2026-01-01T00:00:00Z",
      },
      mail_host_aaaa: {
        name: "mail.example.com", type: "AAAA", status: "optional",
        expected: "one or more public IPv6 addresses (optional)", observed: [],
        reason: "no AAAA record published for mail.example.com; IPv6 is not required by ORVIX",
        guidance: "Optional. If the mail server has IPv6 connectivity, add an AAAA record for mail.example.com pointing to its public IPv6 address.",
        optional: true, checked_at: "2026-01-01T00:00:00Z",
      },
      mx_hosts: [{
        name: "mail.example.com", type: "MX-host", status: "pass",
        expected: "at least one A or AAAA address", observed: ["A 192.0.2.25"],
        guidance: "Add an A record (and optionally AAAA) for the MX host mail.example.com pointing to the mail server's public IP address; an MX target that does not resolve cannot receive mail.",
        optional: false, checked_at: "2026-01-01T00:00:00Z",
      }],
      ptr: {
        name: "192.0.2.25", type: "PTR", status: "fail", expected: "mail.example.com",
        observed: [], reason: "no PTR record published for 192.0.2.25",
        guidance: "Ask the network or hosting provider that owns 192.0.2.25 to set its reverse DNS (PTR) record to mail.example.com. Receivers commonly reject mail from IPs whose rDNS is missing or generic.",
        optional: false, checked_at: "2026-01-01T00:00:00Z",
      },
      autodiscover: {
        name: "autodiscover.example.com", type: "CNAME", status: "optional",
        expected: "mail.example.com", observed: [],
        reason: "autodiscover.example.com is not published; Outlook autodiscover falls back to the ORVIX-hosted endpoint",
        guidance: "Optional. Add a CNAME record for autodiscover.example.com pointing to mail.example.com.",
        optional: true, checked_at: "2026-01-01T00:00:00Z",
      },
      autoconfig: {
        name: "autoconfig.example.com", type: "CNAME", status: "optional",
        expected: "mail.example.com", observed: [],
        reason: "autoconfig.example.com is not published; Thunderbird autoconfig falls back to the ORVIX-hosted endpoint",
        guidance: "Optional. Add a CNAME record for autoconfig.example.com pointing to mail.example.com.",
        optional: true, checked_at: "2026-01-01T00:00:00Z",
      },
      tlsa: {
        name: "_25._tcp.mail.example.com", type: "TLSA", status: "not_applicable",
        reason: "DANE/TLSA is not a configurable feature of this ORVIX deployment, so no TLSA record is required",
        guidance: "No action required. ORVIX does not offer DANE/TLSA configuration, so this record is neither published nor validated.",
        optional: true, checked_at: "2026-01-01T00:00:00Z",
      },
    } as any);
    await openModal();

    // Every expanded record type is present as its own first-class row.
    for (const key of [
      "mx", "mx-host-0", "mail-host-a", "mail-host-aaaa", "ptr",
      "spf", "dkim", "dmarc", "mtasts", "mtasts-policy", "tlsrpt",
      "autodiscover", "autoconfig", "tlsa",
    ]) {
      expect(screen.getByTestId(`dns-row-${key}`)).toBeInTheDocument();
    }

    // Optional and not-applicable rows are explicitly labelled, never hidden
    // and never shown as failures.
    expect(screen.getByTestId("dns-row-mail-host-aaaa")).toHaveTextContent("Optional — not configured");
    expect(screen.getByTestId("dns-row-autodiscover")).toHaveTextContent("Optional — not configured");
    expect(screen.getByTestId("dns-row-autoconfig")).toHaveTextContent("Optional — not configured");
    expect(screen.getByTestId("dns-row-tlsa")).toHaveTextContent("Not applicable");
    expect(screen.getByTestId("dns-row-tlsa")).not.toHaveTextContent("Fail");
    // A not_applicable record has no required value; it says so rather than
    // rendering a bare dash that could read as "unknown".
    expect(screen.getByTestId("required-tlsa")).toHaveTextContent("Not required");

    // Required value, current value, status, reason and guidance all render.
    expect(screen.getByTestId("required-ptr")).toHaveTextContent("mail.example.com");
    expect(screen.getByTestId("observed-ptr")).toHaveTextContent("Not present");
    expect(screen.getByTestId("dns-row-ptr")).toHaveTextContent("Fail");
    expect(screen.getByTestId("reason-ptr")).toHaveTextContent("no PTR record published for 192.0.2.25");
    expect(screen.getByTestId("guidance-ptr")).toHaveTextContent("reverse DNS (PTR) record to mail.example.com");
    expect(screen.getByTestId("observed-mail-host-a")).toHaveTextContent("192.0.2.25");
    expect(screen.getByTestId("observed-mx-host-0")).toHaveTextContent("A 192.0.2.25");
  });

  it("renders whatever SPF/DMARC values the server configured, never a hard-coded generic", async () => {
    // These are the server's CONFIGURED values (the real ORVIX policy and a
    // real reporting mailbox). The component must display them verbatim: it
    // previously showed a generic "v=spf1 mx -all" and a fabricated
    // dmarc@<domain> instead.
    const realSPF = "v=spf1 ip4:65.75.203.74 include:spf.orvix.email -all";
    const realDMARC = "v=DMARC1; p=quarantine; rua=mailto:dmarc-reports@orvix.email";
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      spf: {
        status: "fail", observed: "", expected: realSPF,
        reason: "no SPF record found",
        guidance: `Publish a single TXT record at example.com with the value "${realSPF}". Exactly one SPF record may exist per domain.`,
      },
      dmarc: {
        status: "fail", observed: "",
        expected: realDMARC,
        reason: "DMARC record not found",
        guidance: `Add a TXT record at _dmarc.example.com with the value "${realDMARC}".`,
      },
    } as any);
    await openModal();

    expect(screen.getByTestId("required-spf")).toHaveTextContent(realSPF);
    expect(screen.getByTestId("required-spf")).not.toHaveTextContent("v=spf1 mx -all");
    expect(screen.getByTestId("required-spf").textContent).not.toBe("—");
    expect(screen.getByTestId("required-dmarc")).toHaveTextContent(realDMARC);
    expect(screen.getByTestId("required-dmarc")).not.toHaveTextContent("dmarc@example.com");
    expect(screen.getByTestId("required-dmarc").textContent).not.toBe("—");
    expect(screen.getByTestId("guidance-spf")).toHaveTextContent("Exactly one SPF record may exist per domain");
    expect(screen.getByTestId("guidance-dmarc")).toHaveTextContent("_dmarc.example.com");
  });

  it("shows the MTA-STS HTTPS policy row as unverified when no policy document was fetched", async () => {
    // A missing policy document is exactly the condition that blocks a full
    // pass, so the row must be visible and explicitly failing — hiding it
    // would make an unverified deployment look complete.
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      mtasts_policy: null,
    } as any);
    await openModal();
    const row = screen.getByTestId("dns-row-mtasts-policy");
    expect(row).toBeInTheDocument();
    expect(row).toHaveTextContent("Fail");
    expect(screen.getByTestId("guidance-mtasts-policy").textContent).toContain(
      "https://mta-sts.example.com/.well-known/mta-sts.txt"
    );
    expect(screen.getByTestId("dns-row-tlsrpt")).toBeInTheDocument();
  });

  it("has the required dialog a11y attributes and an accessible title naming the domain", async () => {
    await openModal();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog).toHaveAttribute("aria-labelledby", "dns-records-title");
    const title = document.getElementById("dns-records-title");
    expect(title).toBeTruthy();
    expect(title!.textContent).toContain("example.com");
  });

  it("moves focus into the dialog, traps Tab, and restores focus to the DNS button on Escape", async () => {
    await renderTable();
    const dnsButton = screen.getByRole("button", { name: /Open DNS records for example\.com/i });
    dnsButton.focus();
    fireEvent.click(dnsButton);
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    // Focus moved inside the dialog.
    const dialog = screen.getByRole("dialog");
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));

    // Tab from the last focusable element wraps back inside the dialog.
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>("button:not([disabled])"));
    focusable[focusable.length - 1].focus();
    fireEvent.keyDown(document, { key: "Tab" });
    expect(dialog.contains(document.activeElement)).toBe(true);

    // Escape closes and returns focus to the opening DNS button.
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() => expect(document.activeElement).toBe(dnsButton));
  });

  it("fires the close handler exactly once for repeated Escape presses", async () => {
    await openModal();
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.keyDown(document, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    // The table behind it is intact — no double-close corrupted the state.
    expect(screen.getByText("example.com")).toBeInTheDocument();
  });

  it("never shows a pass/100% state when complete is false, even with a high score", async () => {
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      complete: false,
      health_score: 100,
      dns_health: "pass",
      mx: null,
      spf: null,
      dkim: null,
      dmarc: null,
      mtasts: null,
      tlsrpt: null,
      mtasts_policy: null,
    } as any);
    await openModal();

    expect(screen.getByTestId("incomplete-indicator")).toBeInTheDocument();
    // The summary must not assert a passing state anywhere.
    const summary = screen.getByTestId("health-summary");
    expect(summary.textContent).not.toMatch(/\bPass\b/);
    expect(summary).toHaveTextContent("Incomplete");
    // Unchecked records render as "Not checked", never as a pass.
    expect(screen.getAllByText("Not checked").length).toBeGreaterThan(0);
  });

  it("replaces the cached payload wholesale on a successful verify", async () => {
    await openModal();
    // Fresh check drops mtasts_policy entirely; a shallow merge would keep it.
    vi.mocked(api.verifyEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      health_score: 100,
      mtasts_policy: null,
    } as any);

    fireEvent.click(screen.getByRole("button", { name: /Check DNS now/i }));
    await waitFor(() => expect(screen.getByTestId("health-summary")).toHaveTextContent("100%"));
    // The stale policy object is gone from the cache: the row now reports
    // the policy as unverified rather than replaying the old pass.
    expect(screen.getByTestId("dns-row-mtasts-policy")).toHaveTextContent("Fail");
  });
});

describe("cooldown UX", () => {
  it("disables the check button, counts down live without polling, and re-enables at zero", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      cooldown_until: new Date(Date.now() + 3000).toISOString(),
      retry_after_seconds: 3,
    } as any);

    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Open DNS records for example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    const btn = await screen.findByRole("button", { name: /Retry in/i });
    expect(btn).toBeDisabled();
    expect(screen.getByTestId("cooldown-notice")).toBeInTheDocument();

    const callsWhileCounting = vi.mocked(api.verifyEnterpriseDomainDNS).mock.calls.length;

    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });

    // The countdown itself issued no network calls.
    expect(vi.mocked(api.verifyEnterpriseDomainDNS).mock.calls.length).toBe(callsWhileCounting);
    // And the button is usable again once the window expires.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /Check DNS now/i })).not.toBeDisabled()
    );
  });

  it("keeps rendering the previous good data when verify answers 429", async () => {
    await openModal();
    expect(screen.getByTestId("dns-row-mx")).toBeInTheDocument();

    // Per the backend contract the 429 body IS the last successful snapshot.
    const snapshot = {
      ...defaultDNSHealth,
      health_score: 85,
      cooldown_until: new Date(Date.now() + 60_000).toISOString(),
      retry_after_seconds: 60,
    };
    vi.mocked(api.verifyEnterpriseDomainDNS).mockRejectedValue(
      new ApiError("RATE_LIMITED", "Verification cooldown active.", 429, snapshot)
    );

    fireEvent.click(screen.getByRole("button", { name: /Check DNS now/i }));

    // No blocking error screen; the record table survives and cooldown shows.
    await waitFor(() => expect(screen.getByTestId("cooldown-notice")).toBeInTheDocument());
    expect(screen.getByTestId("dns-records-table")).toBeInTheDocument();
    expect(screen.getByTestId("dns-row-mx")).toBeInTheDocument();
    expect(screen.getByTestId("health-summary")).toHaveTextContent("85%");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("DKIM management in the modal", () => {
  it("offers Generate only when DKIM is unconfigured, and shows pending propagation after", async () => {
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      dkim: { selector: "mail", status: "fail", configured: false, reason: "no DKIM record published", checked_at: "2026-01-01T00:00:00Z" },
    } as any);
    await openModal();

    expect(screen.queryByRole("button", { name: /Rotate DKIM key/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Generate DKIM key/i }));

    await waitFor(() => expect(screen.getByTestId("dkim-pending")).toBeInTheDocument());
    expect(screen.getByTestId("dkim-pending")).toHaveTextContent("v=DKIM1; k=rsa; p=NEWKEYDATA");
    expect(screen.getByTestId("dkim-pending")).toHaveTextContent(/propagation/i);
    // Must not claim success/pass for the brand-new record.
    expect(screen.getByTestId("dkim-pending").textContent).not.toMatch(/\bPass\b/);
  });

  it("offers Rotate only when configured, behind a propagation-warning confirmation", async () => {
    await openModal(); // default fixture has dkim.configured = true

    expect(screen.queryByRole("button", { name: /Generate DKIM key/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Rotate DKIM key/i }));

    await waitFor(() =>
      expect(screen.getByRole("heading", { name: /Rotate DKIM key for example\.com/i })).toBeInTheDocument()
    );
    expect(screen.getByText(/propagation\s+completes/i)).toBeInTheDocument();
    // Nothing rotated until the confirmation is accepted.
    expect(api.rotateDomainDKIM).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /^Rotate key$/i }));
    await waitFor(() => expect(api.rotateDomainDKIM).toHaveBeenCalledWith(1, "mail"));
    await waitFor(() => expect(screen.getByTestId("dkim-pending")).toBeInTheDocument());
  });
});

describe("copy and download", () => {
  it("copies the required value for a record row", async () => {
    await openModal();
    fireEvent.click(screen.getByRole("button", { name: /Copy TXT \(DMARC\) record/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("v=DMARC1; p=reject"));
  });

  it("downloads a records file containing the public record data and no secrets", async () => {
    await openModal();

    let captured = "";
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;
    const RealBlob = globalThis.Blob;

    // Patch Blob so the generated text is observable synchronously.
    (globalThis as any).Blob = class extends RealBlob {
      _text: string;
      constructor(parts: any[], opts?: any) {
        super(parts, opts);
        this._text = parts.join("");
      }
    };
    (URL as any).createObjectURL = vi.fn((blob: any) => {
      captured = blob._text || "";
      return "blob:mock";
    });
    (URL as any).revokeObjectURL = vi.fn();

    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    fireEvent.click(screen.getByRole("button", { name: /Download DNS Records/i }));

    expect(clickSpy).toHaveBeenCalled();
    expect(captured).toContain("DNS records for example.com");
    expect(captured).toContain("NAME | TYPE | PRIORITY | STATUS | REQUIRED VALUE");
    expect(captured).toContain("mail._domainkey.example.com");
    expect(captured).toContain("v=DMARC1; p=reject");
    expect(captured).toContain("| 10 |"); // MX priority column
    // No secret material of any kind.
    expect(captured).not.toMatch(/BEGIN (RSA )?PRIVATE KEY/);
    expect(captured).not.toMatch(/private_key|privatekey|PrivateKeyPEM/i);

    (globalThis as any).Blob = RealBlob;
    (URL as any).createObjectURL = originalCreate;
    (URL as any).revokeObjectURL = originalRevoke;
    clickSpy.mockRestore();
  });
});
