import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ProtocolSettingsPanel from "./ProtocolSettingsPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><ProtocolSettingsPanel /></QueryClientProvider>);
}

const RESPONSE = {
  protocol: "smtp_recv",
  title: "SMTP receiving",
  description: "Inbound SMTP listener configuration.",
  keys: [
    { key: "coremail.smtp_port", label: "SMTP port", description: "", type: "int" as const, restart_required: true, default: 25, value: 25, persisted: false },
    { key: "coremail.require_tls_for_auth", label: "Require TLS for AUTH", description: "", type: "bool" as const, restart_required: false, default: true, value: true, persisted: false },
    { key: "coremail.smtp_host", label: "SMTP bind host", description: "", type: "string" as const, restart_required: true, default: "0.0.0.0", value: "0.0.0.0", persisted: false },
  ],
};

const RESPONSE_WITH_READONLY = {
  protocol: "remote_pop",
  title: "Remote POP",
  description: "Remote POP fetch settings.",
  keys: [
    { key: "coremail.imap_idle_enabled", label: "IMAP IDLE push", description: "", type: "bool" as const, restart_required: false, default: false, value: false, persisted: false, read_only: "no corresponding field exists on the live CoreMail config" },
  ],
};

async function confirmPending() {
  const confirmBtn = await screen.findByRole("button", { name: /confirm/i });
  fireEvent.click(confirmBtn);
}

describe("features/platform/configuration > ProtocolSettingsPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("GET parsing: renders real typed fields for the selected protocol", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    renderPanel();
    await waitFor(() => expect(screen.getByDisplayValue("25")).toBeInTheDocument());
    expect(screen.getByDisplayValue("0.0.0.0")).toBeInTheDocument();
    expect(screen.getByText("Inbound SMTP listener configuration.")).toBeInTheDocument();
  });

  it("requires confirmation before applying, showing a diff preview of current -> proposed", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ applied: [{ key: "coremail.smtp_port", value: 2525, restart_required: true, updated_at: "" }], hot_applied: [], pending_restart: ["coremail.smtp_port"] });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "2525" } });
    fireEvent.click(screen.getByText(/save changes/i));

    // Confirmation dialog shows the diff before any request fires.
    await waitFor(() => expect(screen.getByText(/25 → 2525/)).toBeInTheDocument());
    expect(patchSpy).not.toHaveBeenCalled();

    await confirmPending();
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith("smtp_recv", { "coremail.smtp_port": 2525 }));
  });

  it("PATCH body: sends only the changed key, in the flat (not nested) shape this endpoint requires", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ applied: [{ key: "coremail.smtp_port", value: 2525, restart_required: true, updated_at: "" }], hot_applied: [], pending_restart: ["coremail.smtp_port"] });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "2525" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await confirmPending();
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith("smtp_recv", { "coremail.smtp_port": 2525 }));
  });

  it("type preservation: a bool field round-trips as a real boolean, not the string \"true\"", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ applied: [{ key: "coremail.require_tls_for_auth", value: false, restart_required: false, updated_at: "" }], hot_applied: [], pending_restart: [] });
    renderPanel();
    const boolSelect = await screen.findByDisplayValue("true");
    fireEvent.change(boolSelect, { target: { value: "false" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await confirmPending();
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith("smtp_recv", { "coremail.require_tls_for_auth": false }));
  });

  it("an empty numeric input is never sent as 0 — the Save button stays disabled with no real change", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings");
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "" } });
    // Clearing the field alone must not enable Save with a 0 value.
    expect(screen.getByText(/save changes/i)).toBeDisabled();
    expect(patchSpy).not.toHaveBeenCalled();
  });

  it("shows rejected fields distinctly from a successful save", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ rejected: [{ key: "coremail.smtp_port", reason: "invalid int" }] });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "99999" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await confirmPending();
    await waitFor(() => expect(screen.getByText(/invalid int/i)).toBeInTheDocument());
  });

  it("shows the hot-applied result distinctly from the restart-required banner", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({
      applied: [{ key: "coremail.smtp_port", value: 2525, restart_required: true, updated_at: "" }],
      hot_applied: [],
      pending_restart: ["coremail.smtp_port"],
      rejected: [],
    });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "2525" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await confirmPending();
    await waitFor(() => expect(screen.getByText(/restart required to take effect/i)).toBeInTheDocument());
    expect(screen.queryByText(/applied immediately/i)).not.toBeInTheDocument();
  });

  it("read-only keys render disabled with their reason, never as an editable control", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE_WITH_READONLY);
    renderPanel();
    await waitFor(() => expect(screen.getByText(/read-only/i)).toBeInTheDocument());
    // Only the protocol picker itself is a combobox — the read-only
    // key must render as inert text, not a second editable control.
    expect(screen.getAllByRole("combobox")).toHaveLength(1);
  });

  it("shows a distinct error state on a failed GET", async () => {
    vi.spyOn(api, "getProtocolSettings").mockRejectedValue(new Error("protocol settings unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/protocol settings unavailable/i));
  });
});
