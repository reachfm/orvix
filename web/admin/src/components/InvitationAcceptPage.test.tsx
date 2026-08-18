import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import InvitationAcceptPage from "./InvitationAcceptPage";
import { api, ApiError } from "../api";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><InvitationAcceptPage /></QueryClientProvider>);
}

describe("InvitationAcceptPage (public POST /auth/invitations/accept)", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("accepts a valid token+password and redirects to login after confirmation", async () => {
    const acceptSpy = vi.spyOn(api, "acceptInvitation").mockResolvedValue({
      status: "accepted", user_id: 5, organization_id: 3, email: "owner@acme.example",
      role: "tenant_admin", organization_active: true,
    });
    renderPage();
    fireEvent.change(screen.getByLabelText(/invitation token/i), { target: { value: "tok123" } });
    fireEvent.change(screen.getByLabelText(/^password/i), { target: { value: "longenoughpw" } });
    fireEvent.click(screen.getByRole("button", { name: /accept invitation/i }));

    await waitFor(() => expect(screen.getByText(/invitation accepted/i)).toBeInTheDocument());
    expect(acceptSpy).toHaveBeenCalledWith({ token: "tok123", password: "longenoughpw", name: undefined });
    // The email comes from the server, never from this form.
    expect(screen.getByText(/redirecting to sign in/i)).toBeInTheDocument();
  });

  it("maps 404 NOT_FOUND to an actionable invitation-not-found message", async () => {
    vi.spyOn(api, "acceptInvitation").mockRejectedValue(new ApiError("NOT_FOUND", "invitation not found", 404));
    renderPage();
    fireEvent.change(screen.getByLabelText(/invitation token/i), { target: { value: "bad" } });
    fireEvent.change(screen.getByLabelText(/^password/i), { target: { value: "longenoughpw" } });
    fireEvent.click(screen.getByRole("button", { name: /accept invitation/i }));
    await waitFor(() => expect(screen.getByText(/could not be found/i)).toBeInTheDocument());
  });

  it("maps 409 INVALID_STATE_TRANSITION to revoked/expired/already-used copy", async () => {
    vi.spyOn(api, "acceptInvitation").mockRejectedValue(new ApiError("INVALID_STATE_TRANSITION", "invitation already accepted", 409));
    renderPage();
    fireEvent.change(screen.getByLabelText(/invitation token/i), { target: { value: "used" } });
    fireEvent.change(screen.getByLabelText(/^password/i), { target: { value: "longenoughpw" } });
    fireEvent.click(screen.getByRole("button", { name: /accept invitation/i }));
    await waitFor(() => expect(screen.getByText(/no longer usable/i)).toBeInTheDocument());
  });

  it("maps 409 CONFLICT to existing-account copy", async () => {
    vi.spyOn(api, "acceptInvitation").mockRejectedValue(new ApiError("CONFLICT", "an account with this email already exists", 409));
    renderPage();
    fireEvent.change(screen.getByLabelText(/invitation token/i), { target: { value: "x" } });
    fireEvent.change(screen.getByLabelText(/^password/i), { target: { value: "longenoughpw" } });
    fireEvent.click(screen.getByRole("button", { name: /accept invitation/i }));
    await waitFor(() => expect(screen.getByText(/already exists for the invited email/i)).toBeInTheDocument());
  });

  it("pre-fills the token from the URL query string", () => {
    const original = window.location.search;
    Object.defineProperty(window, "location", { value: { ...window.location, search: "?token=urlsecret" }, writable: true });
    renderPage();
    expect(screen.getByLabelText(/invitation token/i)).toHaveValue("urlsecret");
    Object.defineProperty(window, "location", { value: { ...window.location, search: original }, writable: true });
  });
});
