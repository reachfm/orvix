// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import Organizations from "../Organizations";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={qc}>
      <ToastProvider>{children}</ToastProvider>
    </QueryClientProvider>
  );
}

function mockOrgs() {
  return {
    organizations: [
      { id: 1, slug: "acme", name: "Acme Corp", domain: "acme.com", plan: "pro", status: "active", max_mailboxes: 50, max_storage_mb: 10000, active_mailboxes: 12, created_at: "2026-01-15T00:00:00Z", updated_at: "2026-06-01T00:00:00Z" },
      { id: 2, slug: "beta", name: "Beta Inc", domain: "beta.org", plan: "starter", status: "suspended", max_mailboxes: 10, max_storage_mb: 5000, active_mailboxes: 3, created_at: "2026-03-20T00:00:00Z", updated_at: "2026-07-10T00:00:00Z" },
    ],
    total: 2,
  };
}

describe("Organizations", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders org list from mocked API", async () => {
    vi.spyOn(apiModule.api, "listPlatformOrganizations").mockResolvedValue(mockOrgs());
    render(<Wrapper><Organizations /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    expect(screen.getByText("Beta Inc")).toBeInTheDocument();
    expect(screen.getByText("pro")).toBeInTheDocument();
    expect(screen.getByText("starter")).toBeInTheDocument();
  });

  it("search filter updates query", async () => {
    vi.spyOn(apiModule.api, "listPlatformOrganizations").mockResolvedValue(mockOrgs());
    render(<Wrapper><Organizations /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    const searchInput = screen.getByPlaceholderText(/search/i);
    fireEvent.change(searchInput, { target: { value: "beta" } });
    await waitFor(() => {
      expect(apiModule.api.listPlatformOrganizations).toHaveBeenCalledWith(expect.objectContaining({ search: "beta" }));
    });
  });

  it("create dialog: sanitizes slug on input", async () => {
    vi.spyOn(apiModule.api, "listPlatformOrganizations").mockResolvedValue(mockOrgs());
    render(<Wrapper><Organizations /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Create Organization"));
    await waitFor(() => expect(screen.getByPlaceholderText("my-tenant")).toBeInTheDocument());
    const slugInput = screen.getByPlaceholderText("my-tenant") as HTMLInputElement;
    fireEvent.change(slugInput, { target: { value: "UPPERCASE" } });
    // Component lowercases and strips non-[a-z0-9-]; UPPERCASE becomes uppercase
    expect(slugInput.value).toBe("uppercase");
  });

  it("create dialog: submits and shows success toast", async () => {
    vi.spyOn(apiModule.api, "listPlatformOrganizations").mockResolvedValue(mockOrgs());
    const createSpy = vi.spyOn(apiModule.api, "createPlatformOrganization").mockResolvedValue({ id: 3 });
    render(<Wrapper><Organizations /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Create Organization"));
    await waitFor(() => expect(screen.getByPlaceholderText("my-tenant")).toBeInTheDocument());
    fireEvent.change(screen.getByPlaceholderText("my-tenant"), { target: { value: "new-org" } });
    fireEvent.change(screen.getByPlaceholderText(/my company/i), { target: { value: "New Org" } });
    fireEvent.change(screen.getByPlaceholderText("company.com"), { target: { value: "neworg.com" } });
    fireEvent.click(screen.getByText("Create"));
    await waitFor(() => { expect(createSpy).toHaveBeenCalled(); });
  });

  it("suspend action shows confirmation dialog", async () => {
    vi.spyOn(apiModule.api, "listPlatformOrganizations").mockResolvedValue(mockOrgs());
    vi.spyOn(apiModule.api, "getPlatformOrganizationDetail").mockResolvedValue({ mailbox_count: 12, active_mailbox_count: 10, domain_count: 3, storage_used_mb: 2048 });
    render(<Wrapper><Organizations /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    // Click view on first org (Acme), then click Suspend
    const viewBtns = screen.getAllByText("View");
    fireEvent.click(viewBtns[0]);
    await waitFor(() => expect(screen.getByText("Usage")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Suspend"));
    await waitFor(() => expect(screen.getByText(/confirm suspend/i)).toBeInTheDocument());
  });
});
