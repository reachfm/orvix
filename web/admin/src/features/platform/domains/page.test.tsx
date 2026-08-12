import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DomainsPage from "./page";

function renderWithClient(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

describe("PlatformDomainsPage", () => {
  it("renders the tenant-context-required state when no tenant is selected", () => {
    renderWithClient(<DomainsPage />);
    expect(screen.getByText(/tenant context required/i)).toBeTruthy();
    expect(screen.getByText(/select a tenant above/i)).toBeTruthy();
  });

  it("never fabricates a domain table without a tenant context", () => {
    renderWithClient(<DomainsPage />);
    expect(screen.queryByText(/mailboxes/i)).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it("explains tenant-only operations honestly", () => {
    renderWithClient(<DomainsPage />);
    expect(screen.getAllByText(/tenant-only operations/i).length).toBeGreaterThan(0);
  });
});
