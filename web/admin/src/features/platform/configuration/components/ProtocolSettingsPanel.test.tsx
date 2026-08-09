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

describe("features/platform/configuration > ProtocolSettingsPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("GET parsing: renders real typed fields for the selected protocol", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    renderPanel();
    await waitFor(() => expect(screen.getByDisplayValue("25")).toBeInTheDocument());
    expect(screen.getByDisplayValue("0.0.0.0")).toBeInTheDocument();
    expect(screen.getByText("Inbound SMTP listener configuration.")).toBeInTheDocument();
  });

  it("PATCH body: sends only the changed key, in the flat (not nested) shape this endpoint requires", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ applied: [{ key: "coremail.smtp_port" }] });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "2525" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith("smtp_recv", { "coremail.smtp_port": 2525 }));
  });

  it("type preservation: a bool field round-trips as a real boolean, not the string \"true\"", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    const patchSpy = vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ applied: [{ key: "coremail.require_tls_for_auth" }] });
    renderPanel();
    const boolSelect = await screen.findByDisplayValue("true");
    fireEvent.change(boolSelect, { target: { value: "false" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith("smtp_recv", { "coremail.require_tls_for_auth": false }));
  });

  it("shows rejected fields distinctly from a successful save", async () => {
    vi.spyOn(api, "getProtocolSettings").mockResolvedValue(RESPONSE);
    vi.spyOn(api, "patchProtocolSettings").mockResolvedValue({ rejected: [{ key: "coremail.smtp_port", reason: "invalid int" }] });
    renderPanel();
    const portInput = await screen.findByDisplayValue("25");
    fireEvent.change(portInput, { target: { value: "99999" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await waitFor(() => expect(screen.getByText(/invalid int/i)).toBeInTheDocument());
  });

  it("shows a distinct error state on a failed GET", async () => {
    vi.spyOn(api, "getProtocolSettings").mockRejectedValue(new Error("protocol settings unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/protocol settings unavailable/i));
  });
});
