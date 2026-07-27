// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import UsersPage from "../UsersPage";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockUsers() {
  return [
    { id: 1, email: "admin@orvix.io", role: "superadmin", status: "active", active: true, created_at: "2026-01-15T00:00:00Z" },
    { id: 2, email: "user@example.com", role: "user", status: "active", active: true, created_at: "2026-03-20T00:00:00Z" },
    { id: 3, email: "suspended@example.com", role: "admin", status: "suspended", active: false, created_at: "2026-04-10T00:00:00Z" },
  ];
}

describe("UsersPage", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders user list from mock API", async () => {
    vi.spyOn(apiModule.api, "listPlatformUsers").mockResolvedValue(mockUsers());
    render(<Wrapper><UsersPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("admin@orvix.io")).toBeInTheDocument());
    expect(screen.getByText("user@example.com")).toBeInTheDocument();
    expect(screen.getByText("suspended@example.com")).toBeInTheDocument();
  });

  it("shows role badge for superadmin", async () => {
    vi.spyOn(apiModule.api, "listPlatformUsers").mockResolvedValue(mockUsers());
    render(<Wrapper><UsersPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("superadmin")).toBeInTheDocument());
  });

  it("opens detail drawer on row click", async () => {
    vi.spyOn(apiModule.api, "listPlatformUsers").mockResolvedValue(mockUsers());
    render(<Wrapper><UsersPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("admin@orvix.io")).toBeInTheDocument());
    // Click the user row (id=2, role=user) which shows "Change role"
    const userRows = screen.getAllByText("user@example.com");
    expect(userRows.length).toBeGreaterThan(0);
    fireEvent.click(userRows[0]);
    await waitFor(() => expect(screen.getByText(/change role/i)).toBeInTheDocument());
  });

  it("suspend action shows confirmation dialog", async () => {
    vi.spyOn(apiModule.api, "listPlatformUsers").mockResolvedValue(mockUsers());
    render(<Wrapper><UsersPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("admin@orvix.io")).toBeInTheDocument());
    fireEvent.click(screen.getAllByText("View")[0]);
    await waitFor(() => expect(screen.getByText("Suspend")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Suspend"));
    await waitFor(() => expect(screen.getByText(/confirm suspend/i)).toBeInTheDocument());
  });

  it("delete requires typing email to confirm", async () => {
    vi.spyOn(apiModule.api, "listPlatformUsers").mockResolvedValue(mockUsers());
    render(<Wrapper><UsersPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("admin@orvix.io")).toBeInTheDocument());
    fireEvent.click(screen.getAllByText("View")[0]);
    await waitFor(() => expect(screen.getByText("Delete")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(screen.getByText(/permanently delete/i)).toBeInTheDocument());
    const confirmBtn = screen.getByText("Permanently Delete");
    expect(confirmBtn).toBeDisabled();
    const input = screen.getByPlaceholderText("admin@orvix.io");
    fireEvent.change(input, { target: { value: "admin@orvix.io" } });
    expect(confirmBtn).not.toBeDisabled();
  });
});
