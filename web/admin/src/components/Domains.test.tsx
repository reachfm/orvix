// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import Domains from "./Domains";
import { ApiError } from "../api";

vi.mock("../api", () => {
  class MockApiError extends Error {
    code: string;
    status: number;
    constructor(code: string, message: string, status: number) {
      super(message);
      this.code = code;
      this.status = status;
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

import { api } from "../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

const sampleDomains = [
  { id: 1, name: "example.com", status: "active", plan: "enterprise", mailbox_count: 2, dkim_enabled: true },
  { id: 2, name: "other.com", status: "disabled", plan: "smb", mailbox_count: 0, dkim_enabled: false },
];

const defaultDKIMResponse = { dkim: { selector: "mail", public_dns_txt: "v=DKIM1; p=TEST", dns_record_name: "mail._domainkey.example.com" } };

const defaultDNSHealth = {
  domain_id: 1,
  domain_name: "example.com",
  operational_status: "active",
  dns_health: "pass",
  health_score: 85,
  last_checked_at: "2026-01-01T00:00:00Z",
  mx: { status: "pass", expected: "mx1.example.com, mx2.example.com", observed: ["mx1.example.com:10", "mx2.example.com:20"], reason: "", checked_at: "2026-01-01T00:00:00Z" },
  spf: { status: "pass", observed: "v=spf1 mx -all", expected: "", reason: "", checked_at: "2026-01-01T00:00:00Z" },
  dkim: { selector: "mail", status: "pass", record_name: "mail._domainkey.example.com", public_txt: "v=DKIM1; k=rsa; p=...", configured: true, matches_dns: true, checked_at: "2026-01-01T00:00:00Z" },
  dmarc: { status: "pass", observed: "v=DMARC1; p=reject", checked_at: "2026-01-01T00:00:00Z" },
  mtasts: { status: "pass", observed: "v=STSv1; id=2025", checked_at: "2026-01-01T00:00:00Z" },
  tlsrpt: { status: "pass", observed: "v=TLSRPTv1; rua=mailto:rpt@example.com", checked_at: "2026-01-01T00:00:00Z" },
};

beforeEach(() => {
  qc.clear();
  vi.mocked(api.listDomainsEnterprise).mockResolvedValue({ domains: sampleDomains, total: 2 } as any);
  vi.mocked(api.generateDomainDKIM).mockResolvedValue(defaultDKIMResponse);
  vi.mocked(api.rotateDomainDKIM).mockResolvedValue(defaultDKIMResponse);
  vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue(defaultDNSHealth);
  Object.assign(navigator, { clipboard: { writeText: vi.fn() } });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("Domains management", () => {
  it("renders domain rows from the enterprise endpoint", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());
    expect(screen.getByText("other.com")).toBeInTheDocument();
    expect(api.listDomainsEnterprise).toHaveBeenCalled();
  });

  it("opens a delete confirmation modal and explains mailbox blocking", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    const deleteButtons = screen.getAllByText("Delete");
    fireEvent.click(deleteButtons[0]);

    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    expect(screen.getByText(/still has mailboxes/i)).toBeInTheDocument();
  });

  it("displays the typed DOMAIN_HAS_MAILBOXES error when deletion is rejected", async () => {
    vi.mocked(api.deleteDomainEnterprise).mockRejectedValue(
      new ApiError("DOMAIN_HAS_MAILBOXES", "Domain has mailboxes and cannot be deleted.", 409)
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getAllByText("Delete")[0]);
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Delete Domain/ }));

    await waitFor(() => expect(screen.getByText(/Delete its mailboxes first/i)).toBeInTheDocument());
  });

  it("prevents double submission while a delete is pending", async () => {
    let resolveFn: (v: unknown) => void = () => {};
    vi.mocked(api.deleteDomainEnterprise).mockImplementation(
      () => new Promise((resolve) => { resolveFn = resolve; })
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getAllByText("Delete")[0]);
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    const confirmBtn = screen.getByRole("button", { name: /Delete Domain/ });
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(confirmBtn).toBeDisabled());
    expect(api.deleteDomainEnterprise).toHaveBeenCalledTimes(1);

    resolveFn({ ok: true });
  });

  it("creates a domain and refreshes the list", async () => {
    vi.mocked(api.createDomainEnterprise).mockResolvedValue({ domain: sampleDomains[0] } as any);
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /Add Domain/ }));
    const input = screen.getByLabelText(/new domain name/i);
    fireEvent.change(input, { target: { value: "new.example" } });
    fireEvent.submit(input.closest("form")!);

    await waitFor(() => expect(api.createDomainEnterprise).toHaveBeenCalledWith({ name: "new.example" }));
  });

  // ── DKIM generate / rotate wiring ──

  it("Generate button calls generateDomainDKIM", async () => {
    vi.mocked(api.generateDomainDKIM).mockResolvedValue({ dkim: { selector: "mail", public_dns_txt: "v=DKIM1;...", dns_record_name: "mail._domainkey.other.com" } });
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("other.com")).toBeInTheDocument());

    const genBtn = screen.getByRole("button", { name: "Generate" });
    fireEvent.click(genBtn);
    await waitFor(() => {
      expect(api.generateDomainDKIM).toHaveBeenCalledWith(2, "mail");
      expect(api.rotateDomainDKIM).not.toHaveBeenCalled();
    });
  });

  it("Rotate button opens a confirmation modal", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    expect(screen.getByText(/Rotate DKIM Key/i)).toBeInTheDocument();
    expect(screen.getByText(/DNS TXT record/i)).toBeInTheDocument();
  });

  it("cancelling the rotate modal performs no API request", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Cancel/ }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(api.rotateDomainDKIM).not.toHaveBeenCalled();
    expect(api.generateDomainDKIM).not.toHaveBeenCalled();
  });

  it("confirming rotate calls rotateDomainDKIM, not generateDomainDKIM", async () => {
    vi.mocked(api.rotateDomainDKIM).mockResolvedValue({ dkim: { selector: "mail", public_dns_txt: "v=DKIM1; p=new", dns_record_name: "mail._domainkey.example.com" } });
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Rotate Key/ }));

    await waitFor(() => expect(api.rotateDomainDKIM).toHaveBeenCalledWith(1, "mail"));
    expect(api.generateDomainDKIM).not.toHaveBeenCalled();
  });

  it("rotate typed error renders correctly", async () => {
    vi.mocked(api.rotateDomainDKIM).mockRejectedValue(
      new ApiError("DKIM_NOT_CONFIGURED", "DKIM is not configured.", 409)
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Rotate Key/ }));

    await waitFor(() => expect(screen.getAllByText(/Generate DKIM before rotating/i).length).toBeGreaterThanOrEqual(1));
  });

  it("successful rotation displays DNS instructions", async () => {
    vi.mocked(api.rotateDomainDKIM).mockResolvedValue({
      dkim: { selector: "mail", public_dns_txt: "v=DKIM1; k=rsa; p=TEST", dns_record_name: "mail._domainkey.example.com" },
    });
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Rotate Key/ }));

    await waitFor(() => expect(screen.getByText(/DNS TXT record/i)).toBeInTheDocument());
    expect(screen.getByText(/mail\._domainkey\.example\.com/)).toBeInTheDocument();
    expect(screen.getByText(/v=DKIM1; k=rsa; p=TEST/)).toBeInTheDocument();
  });

  it("double-click cannot submit rotate twice", async () => {
    let resolveFn: (v: unknown) => void = () => {};
    vi.mocked(api.rotateDomainDKIM).mockImplementation(
      () => new Promise((resolve) => { resolveFn = resolve; })
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByText("Rotate"));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    const rotateBtn = screen.getByRole("button", { name: /Rotate Key/ });
    fireEvent.click(rotateBtn);

    // The button must become disabled after one submission
    // (prevents any double-click even in the UI).
    await waitFor(() => expect(rotateBtn).toBeDisabled());
    expect(api.rotateDomainDKIM).toHaveBeenCalledTimes(1);

    resolveFn({ dkim: { selector: "mail", public_dns_txt: "", dns_record_name: "" } });
  });

  // ── DNS Health drawer ──

  it("clicking a domain name opens the DNS Health drawer", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));

    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent(/DNS Health: example\.com/i);
    await waitFor(() => expect(screen.getByText("85%")).toBeInTheDocument());
  });

  it("Escape key closes the DNS Health drawer", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    fireEvent.keyDown(document, { key: "Escape" });

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("close button in the drawer dismisses the panel", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Close" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("DNS Health drawer shows all six record types", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    const dialog = screen.getByRole("dialog");
    await waitFor(() => expect(within(dialog).getByText("MX")).toBeInTheDocument());
    expect(within(dialog).getByText("SPF")).toBeInTheDocument();
    expect(within(dialog).getByText("DKIM")).toBeInTheDocument();
    expect(within(dialog).getByText("DMARC")).toBeInTheDocument();
    expect(within(dialog).getByText("MTA-STS")).toBeInTheDocument();
    expect(within(dialog).getByText("TLS-RPT")).toBeInTheDocument();

    const badges = dialog.querySelectorAll('[class*="inline-flex items-center gap-1.5 text-xs"]');
    expect(badges.length).toBeGreaterThanOrEqual(6);
  });

  it("Check DNS Now button is disabled while verification is in progress", async () => {
    let resolveVerify: (v: unknown) => void = () => {};
    vi.mocked(api.verifyEnterpriseDomainDNS).mockImplementation(
      () => new Promise((resolve) => { resolveVerify = resolve; })
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    await waitFor(() => expect(screen.getByRole("button", { name: /Checking DNS\.\.\./i })).toBeDisabled());
    expect(api.verifyEnterpriseDomainDNS).toHaveBeenCalledTimes(1);

    resolveVerify(undefined);
  });

  it("verify button triggers the verification endpoint", async () => {
    vi.mocked(api.getEnterpriseDomainDNS).mockResolvedValue({
      ...defaultDNSHealth,
      last_checked_at: new Date().toISOString(),
    });
    vi.mocked(api.verifyEnterpriseDomainDNS).mockResolvedValue(undefined as any);
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /Check DNS Now/ }));
    await waitFor(() => expect(api.verifyEnterpriseDomainDNS).toHaveBeenCalledWith(1));
  });

  it("opens drawer with keyboard Enter on domain row", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    const domainButton = screen.getByRole("button", { name: /example\.com/i });
    domainButton.focus();
    fireEvent.keyDown(domainButton, { key: "Enter" });

    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());
    expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent(/DNS Health: example\.com/i);
  });

  it("shows loading state when DNS data is not yet loaded", async () => {
    let resolveDNS: (v: unknown) => void = () => {};
    vi.mocked(api.getEnterpriseDomainDNS).mockImplementation(
      () => new Promise((resolve) => { resolveDNS = resolve; })
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    expect(screen.getByText(/Checking DNS\.\.\./i)).toBeInTheDocument();

    resolveDNS(defaultDNSHealth);
  });

  it("shows error state when DNS check fails", async () => {
    let resolveVerify: (v: unknown) => void = () => {};
    vi.mocked(api.getEnterpriseDomainDNS).mockRejectedValue(new Error("Failed to load DNS data."));
    vi.mocked(api.verifyEnterpriseDomainDNS).mockImplementation(
      () => new Promise((resolve) => { resolveVerify = resolve; })
    );
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    await waitFor(() => expect(screen.getByText("Failed to load DNS data.")).toBeInTheDocument());

    resolveVerify(undefined);
  });

  it("copy button works for DKIM public TXT", async () => {
    render(<Wrapper><Domains /></Wrapper>);
    await waitFor(() => expect(screen.getByText("example.com")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /example\.com/i }));
    await waitFor(() => expect(screen.getByRole("dialog")).toBeInTheDocument());

    await waitFor(() => expect(screen.getByRole("button", { name: /Copy DKIM TXT record/ })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Copy DKIM TXT record/ }));
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith("v=DKIM1; k=rsa; p=..."));
  });
});
