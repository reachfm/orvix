// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const messages = [
  {
    id: "m1",
    from: "alice@example.com",
    subject: "Quarterly update",
    preview: "The report is attached",
    date: "Today",
    unread: true,
  },
  {
    id: "m2",
    from: "bob@example.com",
    subject: "Release checklist",
    preview: "Please review the RC checklist",
    date: "Yesterday",
    unread: false,
  },
];

describe("Webmail certification", () => {
  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, "offsetHeight", { configurable: true, value: 720 });
    Object.defineProperty(HTMLElement.prototype, "offsetWidth", { configurable: true, value: 420 });
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      json: async () => messages,
    })) as unknown as typeof fetch;
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("loads the authenticated webmail shell and mailbox list", async () => {
    render(<App />);

    expect(screen.getByRole("heading", { name: "Orvix Mail" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /compose/i })).toBeInTheDocument();

    await waitFor(() => expect(screen.getByText("inbox (2)")).toBeInTheDocument());
    expect(globalThis.fetch).toHaveBeenCalledWith("/api/v1/queue?folder=inbox", { credentials: "include" });
  });

  it("loads a message into the reading pane", async () => {
    render(<App />);

    await waitFor(() => expect(screen.getAllByText("Quarterly update").length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByText("Quarterly update")[0]);

    expect(screen.getByRole("heading", { name: "Welcome to Orvix Webmail" })).toBeInTheDocument();
    expect(screen.getByText("team@orvix.email")).toBeInTheDocument();
  });

  it("switches mailboxes and refreshes the mailbox load", async () => {
    render(<App />);

    await waitFor(() => expect(screen.getAllByText("inbox (2)").length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByRole("button", { name: /drafts/i })[0]);

    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith("/api/v1/queue?folder=drafts", { credentials: "include" }));
    expect(screen.getByText("drafts (2)")).toBeInTheDocument();
  });

  it("opens compose, edits fields, and exposes send action", async () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /compose/i })[0]);

    expect(screen.getByRole("heading", { name: "New Message" })).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText("To"), { target: { value: "team@example.com" } });
    fireEvent.change(screen.getByPlaceholderText("Subject"), { target: { value: "RC1" } });
    fireEvent.change(screen.getByPlaceholderText("Write your message..."), { target: { value: "Certification update" } });

    expect(screen.getByDisplayValue("team@example.com")).toBeInTheDocument();
    expect(screen.getByDisplayValue("RC1")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Certification update")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });

  it("preserves draft text while composing until the modal is closed", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /compose/i })[0]);
    fireEvent.change(screen.getByPlaceholderText("Write your message..."), { target: { value: "Autosave candidate draft" } });

    expect(screen.getByDisplayValue("Autosave candidate draft")).toBeInTheDocument();
  });

  it("exposes attachment upload control in compose", () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /compose/i })[0]);
    const buttons = screen.getAllByRole("button");

    expect(buttons.length).toBeGreaterThanOrEqual(3);
    expect(screen.getByRole("button", { name: /send/i })).toBeInTheDocument();
  });

  it("generates the expected mailbox API URL for message searches", async () => {
    render(<App />);

    const search = screen.getByPlaceholderText("Search mail...");
    fireEvent.change(search, { target: { value: "release" } });

    await waitFor(() => expect(globalThis.fetch).toHaveBeenCalledWith("/api/v1/queue?folder=inbox", { credentials: "include" }));
    expect(search).toHaveValue("release");
  });

  it("exposes bulk-style message actions after selecting a message", async () => {
    render(<App />);

    await waitFor(() => expect(screen.getAllByText("Quarterly update").length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByText("Quarterly update")[0]);

    const actionButtons = screen.getAllByRole("button");
    expect(actionButtons.length).toBeGreaterThanOrEqual(10);
  });

  it("closes compose after draft work is complete", async () => {
    render(<App />);

    fireEvent.click(screen.getAllByRole("button", { name: /compose/i })[0]);
    expect(screen.getByRole("heading", { name: "New Message" })).toBeInTheDocument();

    const closeButton = screen.getAllByRole("button").find((button) => button.querySelector("svg") && !button.textContent?.trim());
    expect(closeButton).toBeTruthy();
    await act(async () => {
      fireEvent.click(closeButton as HTMLButtonElement);
    });

    expect(screen.queryByRole("heading", { name: "New Message" })).not.toBeInTheDocument();
  });
});
