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

describe("features/platform/organizations — PSA create + pending_activation", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("creates an organization and reveals the one-time invite token exactly once with a warning", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue(SUMMARY);
    const createSpy = vi.spyOn(api, "createPlatformOrganization").mockResolvedValue({
      organization: { id: 2, name: "Globex", slug: "globex", domain: "globex.example", plan: "free", active: false, created_at: "2026-02-01T00:00:00Z", updated_at: "2026-02-01T00:00:00Z" },
      invitation: {
        id: 10, organization_id: 2, inviter_id: 99, email: "owner@globex.example", role: "tenant_admin",
        status: "pending", expires_at: "2026-02-08T00:00:00Z", created_at: "2026-02-01T00:00:00Z", updated_at: "2026-02-01T00:00:00Z",
      },
      invite_token: "abc123secret",
      warning: "Save this invitation token now - it will not be shown again",
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /create organization/i }));
    const nameInput = await screen.findByPlaceholderText("Acme Corp");
    fireEvent.change(nameInput, { target: { value: "Globex" } });
    const emailInput = screen.getByPlaceholderText("owner@acme.example");
    fireEvent.change(emailInput, { target: { value: "owner@globex.example" } });
    fireEvent.click(screen.getByRole("button", { name: /create organization/i }));

    await waitFor(() => expect(screen.getByText("abc123secret")).toBeInTheDocument());
    // Pending-activation framing + one-time warning, and the request
    // carried owner_email (never a fabricated password/owner).
    expect(screen.getByText(/pending activation/i)).toBeInTheDocument();
    expect(screen.getByText(/Save this invitation token now/i)).toBeInTheDocument();
    const call = createSpy.mock.calls[0][0] as { owner_email: string; name: string };
    expect(call.owner_email).toBe("owner@globex.example");
    expect(call.name).toBe("Globex");
    // The token is the ONLY secret — no password field is ever part of
    // the create payload.
    expect("password" in call).toBe(false);
  });

  it("renders pending_activation distinctly in the detail drawer and disables manual activation", async () => {
    vi.spyOn(api, "listOrganizations").mockResolvedValue({ organizations: [{ ...SUMMARY.organizations[0], active: false }], total: 1 });
    vi.spyOn(api, "getOrganizationDetail").mockResolvedValue({
      ...DETAIL, active: false, status_label: "pending_activation",
    });
    const setActiveSpy = vi.spyOn(api, "setOrganizationActive").mockResolvedValue({ status: "ok" });
    renderPage();
    await waitFor(() => expect(screen.getByText("Acme Corp")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Acme Corp"));
    await waitFor(() => expect(screen.getByText("Pending activation")).toBeInTheDocument());
    // The drawer explains the owner-invitation activation path.
    expect(screen.getByText(/waiting for its invited owner/i)).toBeInTheDocument();
    // Manual activation is not offered while pending (the backend
    // rejects it with 409 CONFLICT anyway).
    const activateButton = screen.getByRole("button", { name: /Awaiting owner activation/i });
    expect(activateButton).toBeDisabled();
    expect(setActiveSpy).not.toHaveBeenCalled();
  });
});
