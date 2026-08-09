import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import OrganizationsPage from "./page";
import * as api from "./api";
import type { ListOrganizationsResponse, OrganizationDetail } from "./contract";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><OrganizationsPage /></QueryClientProvider>);
}

const SUMMARY: ListOrganizationsResponse = {
  organizations: [
    { id: 1, name: "Acme Corp", slug: "acme", domain: "acme.example", plan: "enterprise", active: true, mailbox_count: 42, domain_count: 3, created_at: "2026-01-01T00:00:00Z" },
  ],
  total: 1,
};

const DETAIL: OrganizationDetail = {
  id: 1, name: "Acme Corp", slug: "acme", domain: "acme.example", plan: "enterprise",
  max_domains: 10, max_mailboxes: 500, primary_color: "#000", active: true,
  created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z",
  domain_count: 3, mailbox_count: 42, admin_count: 2, quota_used_bytes: 1073741824,
  status_label: "active",
};

describe("features/platform/organizations", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders real organization rows from listOrganizations, not mock data", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    expect(screen.getByText("acme.example")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("shows a distinct empty state, not indistinguishable from an error", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue({ organizations: [], total: 0 });
    renderPage();
    await waitFor(() => expect(screen.getByText("No organizations found.")).toBeInTheDocument());
  });

  it("shows a distinct error state and never silently renders an empty-success screen on failure", async () => {
    vi.spyOn(api, "listOrganizations").mockRejectedValue(new Error("network down"));
    renderPage();
    await waitFor(() => expect(screen.getByText(/network down/i)).toBeInTheDocument());
    expect(screen.queryByText("No organizations found.")).not.toBeInTheDocument();
  });

  it("opens the detail drawer with real typed fields on row click, not a generic JSON dump", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getOrganizationDetail").mockResolvedValue(DETAIL);
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Acme Corp"));
    await waitFor(() => expect(screen.getByText("3 / 10")).toBeInTheDocument());
    expect(screen.getByText("42 / 500")).toBeInTheDocument();
    expect(screen.getByText("1.0 GB")).toBeInTheDocument();
    // Never a raw JSON.stringify dump of the response.
    expect(screen.queryByText(/"quota_used_bytes"/)).not.toBeInTheDocument();
  });

  it("requires typed confirmation before suspending an active organization, and invalidates on success", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getOrganizationDetail").mockResolvedValue(DETAIL);
    const setActiveSpy = vi.spyOn(api, "setOrganizationActive").mockResolvedValue({ status: "ok" });
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Acme Corp"));
    await waitFor(() => expect(screen.getByText("Suspend organization")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Suspend organization"));

    const confirmInput = await screen.findByRole("textbox");
    const confirmButton = screen.getByRole("button", { name: /confirm/i });
    expect(confirmButton).toBeDisabled();
    fireEvent.change(confirmInput, { target: { value: "acme" } });
    expect(confirmButton).not.toBeDisabled();
    fireEvent.click(confirmButton);

    await waitFor(() => expect(setActiveSpy).toHaveBeenCalledWith(1, { active: false }));
  });

  it("editing the organization sends only the changed fields via PATCH, never the full form", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getOrganizationDetail").mockResolvedValue(DETAIL);
    const updateSpy = vi.spyOn(api, "updateOrganization").mockResolvedValue({
      organization: { id: 1, name: "Acme Corporation", slug: "acme", domain: "acme.example", plan: "enterprise", max_domains: 10, max_mailboxes: 500, primary_color: "#000", active: true, created_at: DETAIL.created_at, updated_at: DETAIL.updated_at },
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Acme Corp"));
    await waitFor(() => expect(screen.getByText("Edit organization")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Edit organization"));

    const nameInput = await screen.findByDisplayValue("Acme Corp");
    fireEvent.change(nameInput, { target: { value: "Acme Corporation" } });
    fireEvent.click(screen.getByText("Save changes"));

    await waitFor(() => expect(updateSpy).toHaveBeenCalledWith(1, { name: "Acme Corporation" }));
  });

  it("the edit form's save button is disabled until a field actually changes", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    vi.spyOn(api, "getOrganizationDetail").mockResolvedValue(DETAIL);
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Acme Corp"));
    await waitFor(() => expect(screen.getByText("Edit organization")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Edit organization"));
    await screen.findByDisplayValue("Acme Corp");
    expect(screen.getByText("Save changes")).toBeDisabled();
  });
});
