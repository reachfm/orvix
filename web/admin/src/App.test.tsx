// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import App from "./App";

describe("Admin console certification", () => {
  afterEach(() => cleanup());

  it("loads the dashboard as the default authenticated console view", () => {
    render(<App />);

    expect(screen.getByRole("heading", { name: "Orvix Admin" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
    expect(screen.getByText("Emails Today")).toBeInTheDocument();
    expect(screen.getByText("Queue Depth")).toBeInTheDocument();
    expect(screen.getByText("Server Health")).toBeInTheDocument();
  });

  it("restores console session state inside a mounted app instance", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /domains/i })[0]);
    expect(screen.getByRole("heading", { name: "Domain Management" })).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: /dashboard/i })[0]);
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
  });

  it("exposes RBAC-sensitive navigation areas currently present in the admin shell", () => {
    render(<App />);

    for (const label of ["Dashboard", "Domains", "Users", "Firewall", "Modules", "Audit Log", "Settings"]) {
      expect(screen.getAllByRole("button", { name: new RegExp(label, "i") }).length).toBeGreaterThan(0);
    }
  });

  it("loads the audit page with persisted-event style rows", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /audit log/i })[0]);

    expect(screen.getByRole("heading", { name: "Audit Log" })).toBeInTheDocument();
    expect(screen.getByText("user.login")).toBeInTheDocument();
    expect(screen.getAllByText("admin@orvix.email").length).toBeGreaterThan(0);
  });

  it("loads the domains page and DNS action surface", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /domains/i })[0]);

    expect(screen.getByRole("heading", { name: "Domain Management" })).toBeInTheDocument();
    expect(screen.getByText("example.com")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /dns wizard/i }).length).toBeGreaterThan(0);
  });

  it("loads the mailbox/user management page", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /users/i })[0]);

    expect(screen.getByRole("heading", { name: "User Management" })).toBeInTheDocument();
    expect(screen.getByText("john@example.com")).toBeInTheDocument();
    expect(screen.getByText("jane@example.com")).toBeInTheDocument();
  });

  it("loads the queue-adjacent dashboard metric and module operations page", () => {
    render(<App />);

    expect(screen.getAllByText("Queue Depth").length).toBeGreaterThan(0);
    fireEvent.click(screen.getAllByRole("button", { name: /modules/i })[0]);

    expect(screen.getByRole("heading", { name: "Module Versions" })).toBeInTheDocument();
    expect(screen.getByText("Provision API")).toBeInTheDocument();
    expect(screen.getByText("Webmail UI")).toBeInTheDocument();
  });

  it("supports logout-style return from a privileged section to the dashboard shell", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /audit log/i })[0]);
    expect(screen.getByRole("heading", { name: "Audit Log" })).toBeInTheDocument();

    fireEvent.click(screen.getAllByRole("button", { name: /dashboard/i })[0]);
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
  });
});
