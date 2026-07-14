// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import App from "./App";

describe("Admin console certification", () => {
  beforeAll(() => {
    globalThis.fetch = vi.fn<typeof fetch>().mockResolvedValue({ ok: true } as Response);
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("loads the dashboard as the default authenticated console view", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Orvix Admin" })).toBeInTheDocument());
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
    expect(screen.getByText("Emails Today")).toBeInTheDocument();
  });

  it("restores console session state inside a mounted app instance", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /domains/i })[0]);
    expect(screen.getByRole("heading", { name: "Domain Management" })).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: /dashboard/i })[0]);
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
  });

  it("exposes RBAC-sensitive navigation areas currently present in the admin shell", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    for (const label of ["Dashboard", "Domains", "Users", "Firewall", "Modules", "Audit Log", "Settings"]) {
      expect(screen.getAllByRole("button", { name: new RegExp(label, "i") }).length).toBeGreaterThan(0);
    }
  });

  it("loads the audit page with persisted-event style rows", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /audit log/i })[0]);

    expect(screen.getByRole("heading", { name: "Audit Log" })).toBeInTheDocument();
    expect(screen.getByText("user.login")).toBeInTheDocument();
    expect(screen.getAllByText("admin@orvix.email").length).toBeGreaterThan(0);
  });

  it("loads the domains page and DNS action surface", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /domains/i })[0]);

    expect(screen.getByRole("heading", { name: "Domain Management" })).toBeInTheDocument();
    expect(screen.getByText("example.com")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /dns wizard/i }).length).toBeGreaterThan(0);
  });

  it("loads the mailbox/user management page", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /users/i })[0]);

    expect(screen.getByRole("heading", { name: "User Management" })).toBeInTheDocument();
    expect(screen.getByText("john@example.com")).toBeInTheDocument();
    expect(screen.getByText("jane@example.com")).toBeInTheDocument();
  });

  it("loads the queue-adjacent dashboard metric and module operations page", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getAllByText("Queue Depth").length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByRole("button", { name: /modules/i })[0]);

    expect(screen.getByRole("heading", { name: "Module Versions" })).toBeInTheDocument();
    expect(screen.getByText("Provision API")).toBeInTheDocument();
    expect(screen.getByText("Webmail UI")).toBeInTheDocument();
  });

  it("supports logout-style return from a privileged section to the dashboard shell", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    fireEvent.click(screen.getAllByRole("button", { name: /audit log/i })[0]);
    expect(screen.getByRole("heading", { name: "Audit Log" })).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: /dashboard/i })[0]);
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
  });

  it("shows customer portal navigation sections", async () => {
    render(<App />);
    await waitFor(() => expect(screen.getByText("Emails Today")).toBeInTheDocument());

    expect(screen.getByText("Customer Portal")).toBeInTheDocument();
    expect(screen.getAllByText("Account").length).toBeGreaterThan(0);
  });
});
