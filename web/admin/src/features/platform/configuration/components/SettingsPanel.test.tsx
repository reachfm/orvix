import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SettingsPanel from "./SettingsPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><SettingsPanel /></QueryClientProvider>);
}

const SETTINGS = {
  general: { primary_domain: "mail.example.com", public_ipv4: "1.2.3.4", public_ipv6: "", hostname: "host.internal" },
  mail_listeners: { smtp_host: "0.0.0.0", smtp_port: 25, imap_host: "0.0.0.0", imap_port: 143, pop3_host: "0.0.0.0", pop3_port: 110, jmap_host: "0.0.0.0", jmap_port: 8080, submission_enabled: true, submission_host: "0.0.0.0", submission_port: 587, smtps_enabled: false, smtps_host: "0.0.0.0", smtps_port: 465, imaps_enabled: false, imaps_host: "0.0.0.0", imaps_port: 993, pop3s_enabled: false, pop3s_host: "0.0.0.0", pop3s_port: 995 },
  security: { password_min_len: 12, session_ttl_seconds: 900, refresh_ttl_seconds: 604800 },
  backup: { dir: "/var/backups/orvix/", retention_count: 10 },
  dns: { public_ipv4: "1.2.3.4", public_ipv6: "", cloudflare_zone_configured: true, namecheap_configured: false },
  build: { version: "1.0.0", commit: "abc123", tag: "v1.0.0", build_time: "2026-01-01", channel: "stable", go_version: "go1.26", os: "linux", arch: "amd64" },
};

describe("features/platform/configuration > SettingsPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression test: the previous SettingsTab iterated
  // Object.entries() over the top-level GET response, so each "field"
  // it rendered was actually a whole SECTION object stringified via
  // JSON.stringify into one text input. This asserts the real,
  // individual, typed fields render distinctly.
  it("renders individual typed section fields, never a JSON.stringify of a whole section", async () => {
    vi.spyOn(api, "getAdminSettings").mockResolvedValue(SETTINGS);
    renderPanel();
    await waitFor(() => expect(screen.getByDisplayValue("mail.example.com")).toBeInTheDocument());
    expect(screen.getByDisplayValue("25")).toBeInTheDocument();
    expect(screen.getByText("Version")).toBeInTheDocument();
    expect(screen.getByText("1.0.0")).toBeInTheDocument();
    // Never a raw stringified section object anywhere on the page.
    expect(screen.queryByDisplayValue(/{"smtp_host"/)).not.toBeInTheDocument();
  });

  // Regression test: PatchAdminSettings requires the NESTED shape
  // {"section": {"field": value}} — the previous form sent a flat
  // {field: value} object that the handler's map[string]map[string]
  // binding cannot interpret as section/field pairs.
  it("save sends the exact nested {section:{field:value}} shape for only the changed field", async () => {
    vi.spyOn(api, "getAdminSettings").mockResolvedValue(SETTINGS);
    const patchSpy = vi.spyOn(api, "patchAdminSettings").mockResolvedValue({ applied: ["general.primary_domain"], restart_required: false });
    renderPanel();
    const input = await screen.findByDisplayValue("mail.example.com");
    fireEvent.change(input, { target: { value: "new.example.com" } });
    fireEvent.click(screen.getByText(/save changes/i));

    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith({ general: { primary_domain: "new.example.com" } }));
  });

  it("boolean fields round-trip as real booleans, not strings", async () => {
    vi.spyOn(api, "getAdminSettings").mockResolvedValue(SETTINGS);
    const patchSpy = vi.spyOn(api, "patchAdminSettings").mockResolvedValue({ restart_required: false });
    renderPanel();
    await waitFor(() => expect(screen.getByDisplayValue("mail.example.com")).toBeInTheDocument());
    // mail_listeners renders before dns per SETTINGS_SCHEMA order, and
    // submission_enabled is the only "true" boolean field in that
    // section, so it is the first "true"-valued select in the DOM.
    const submissionSelect = screen.getAllByDisplayValue("true")[0];
    fireEvent.change(submissionSelect, { target: { value: "false" } });
    fireEvent.click(screen.getByText(/save changes/i));
    await waitFor(() => expect(patchSpy).toHaveBeenCalledWith({ mail_listeners: { submission_enabled: false } }));
  });

  it("shows a distinct error state on failure", async () => {
    vi.spyOn(api, "getAdminSettings").mockRejectedValue(new Error("settings unavailable"));
    renderPanel();
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent(/settings unavailable/i));
  });
});
