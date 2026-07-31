// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
        default:
          return fallback;
      }
    },
    api: {
      listDomainsEnterprise: vi.fn(),
      createDomainEnterprise: vi.fn(),
      deleteDomainEnterprise: vi.fn(),
      generateDomainDKIM: vi.fn(),
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

beforeEach(() => {
  vi.mocked(api.listDomainsEnterprise).mockResolvedValue({ domains: sampleDomains, total: 2 } as any);
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

    // Resolve so the component can settle.
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
});
