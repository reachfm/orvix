// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import MailboxList from "../MailboxList";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockMailboxes() {
  return [
    { id: 1, email: "alice@example.com", domain: "example.com", status: "active", is_admin: true, quota_mb: 1024, used_mb: 340, created_at: "2026-01-15T00:00:00Z" },
    { id: 2, email: "bob@test.org", domain: "test.org", status: "suspended", is_admin: false, quota_mb: 512, used_mb: 480, created_at: "2026-03-20T00:00:00Z" },
  ];
}

describe("MailboxList", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders mailbox list from mock API", async () => {
    vi.spyOn(apiModule.api, "listPlatformMailboxes").mockResolvedValue(mockMailboxes());
    render(<Wrapper><MailboxList /></Wrapper>);
    await waitFor(() => expect(screen.getByText("alice@example.com")).toBeInTheDocument());
    expect(screen.getByText("bob@test.org")).toBeInTheDocument();
  });

  it("shows admin badge for admin mailboxes", async () => {
    vi.spyOn(apiModule.api, "listPlatformMailboxes").mockResolvedValue(mockMailboxes());
    render(<Wrapper><MailboxList /></Wrapper>);
    await waitFor(() => expect(screen.getAllByText("Admin").length).toBeGreaterThan(0));
  });

  it("opens detail drawer on row click", async () => {
    vi.spyOn(apiModule.api, "listPlatformMailboxes").mockResolvedValue(mockMailboxes());
    render(<Wrapper><MailboxList /></Wrapper>);
    await waitFor(() => expect(screen.getByText("alice@example.com")).toBeInTheDocument());
    const viewBtns = screen.getAllByText("View");
    fireEvent.click(viewBtns[0]);
    await waitFor(() => expect(screen.getByText("Quota Usage")).toBeInTheDocument());
  });

  it("suspend action shows confirmation dialog", async () => {
    vi.spyOn(apiModule.api, "listPlatformMailboxes").mockResolvedValue(mockMailboxes());
    render(<Wrapper><MailboxList /></Wrapper>);
    await waitFor(() => expect(screen.getByText("alice@example.com")).toBeInTheDocument());
    const viewBtns = screen.getAllByText("View");
    fireEvent.click(viewBtns[0]);
    await waitFor(() => expect(screen.getByText("Suspend")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Suspend"));
    await waitFor(() => expect(screen.getByText(/confirm suspend/i)).toBeInTheDocument());
  });

  it("delete requires typing email to confirm", async () => {
    vi.spyOn(apiModule.api, "listPlatformMailboxes").mockResolvedValue(mockMailboxes());
    render(<Wrapper><MailboxList /></Wrapper>);
    await waitFor(() => expect(screen.getByText("alice@example.com")).toBeInTheDocument());
    fireEvent.click(screen.getAllByText("View")[0]);
    await waitFor(() => expect(screen.getByText("Delete")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(screen.getByText(/permanently delete/i)).toBeInTheDocument());
    const confirmBtn = screen.getByText("Permanently Delete");
    expect(confirmBtn).toBeDisabled();
    const input = screen.getByPlaceholderText("alice@example.com");
    fireEvent.change(input, { target: { value: "wrong@email.com" } });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(input, { target: { value: "alice@example.com" } });
    expect(confirmBtn).not.toBeDisabled();
  });
});
