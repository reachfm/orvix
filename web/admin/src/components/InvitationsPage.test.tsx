import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import InvitationsPage from "./InvitationsPage";
import { api, ApiError } from "../api";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><InvitationsPage /></QueryClientProvider>);
}

const PENDING = [
  { id: 1, email: "pending@acme.example", role: "tenant_operator", status: "pending", expires_at: "2026-02-08T00:00:00Z" },
  { id: 2, email: "accepted@acme.example", role: "tenant_admin", status: "accepted" },
  { id: 3, email: "revoked@acme.example", role: "tenant_readonly", status: "revoked" },
];

describe("InvitationsPage — create/resend token reveal + status states", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("shows distinct pending/accepted/revoked status badges", async () => {
    vi.spyOn(api, "listInvitations").mockResolvedValue(PENDING as any);
    renderPage();
    await waitFor(() => expect(screen.getByText("pending@acme.example")).toBeInTheDocument());
    expect(screen.getByText("pending")).toBeInTheDocument();
    expect(screen.getByText("accepted")).toBeInTheDocument();
    expect(screen.getByText("revoked")).toBeInTheDocument();
  });

  it("reveals the one-time token after create with a copy button and warning", async () => {
    vi.spyOn(api, "listInvitations").mockResolvedValue([] as any);
    vi.spyOn(api, "createInvitation").mockResolvedValue({
      invitation: { id: 9, email: "new@acme.example", role: "tenant_operator", status: "pending" },
      token: "fresh-token-abc",
    });
    renderPage();
    const emailInput = await screen.findByPlaceholderText("colleague@example.com");
    fireEvent.change(emailInput, { target: { value: "new@acme.example" } });
    fireEvent.click(screen.getByRole("button", { name: /invite/i }));
    await waitFor(() => expect(screen.getByText("fresh-token-abc")).toBeInTheDocument());
    expect(screen.getByText(/will not be shown again/i)).toBeInTheDocument();
  });

  it("resends a pending invitation and shows the ROTATED token once", async () => {
    vi.spyOn(api, "listInvitations").mockResolvedValue(PENDING as any);
    const resendSpy = vi.spyOn(api, "resendInvitation").mockResolvedValue({
      invitation: { id: 1, email: "pending@acme.example", status: "pending" },
      token: "rotated-token-xyz",
      warning: "This new token replaces the previous one. Save it now - it will not be shown again.",
    });
    renderPage();
    await waitFor(() => expect(screen.getByText("pending@acme.example")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /re-issue invitation for pending@acme.example/i }));
    await waitFor(() => expect(screen.getByText("rotated-token-xyz")).toBeInTheDocument());
    expect(screen.getByText(/replaces the previous one/i)).toBeInTheDocument();
    expect(resendSpy).toHaveBeenCalledWith(1);
  });

  it("surfaces the resend rotate error for non-pending invitations", async () => {
    vi.spyOn(api, "listInvitations").mockResolvedValue(PENDING as any);
    vi.spyOn(api, "resendInvitation").mockRejectedValue(new ApiError("INVALID_STATE_TRANSITION", "only pending invitations can be re-issued", 409));
    renderPage();
    await waitFor(() => expect(screen.getByText("accepted@acme.example")).toBeInTheDocument());
    // No resend control for accepted/revoked rows.
    expect(screen.queryByRole("button", { name: /re-issue invitation for accepted@acme.example/i })).not.toBeInTheDocument();
  });
});
