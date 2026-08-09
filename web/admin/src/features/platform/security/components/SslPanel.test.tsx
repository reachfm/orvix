import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SslPanel from "./SslPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><SslPanel /></QueryClientProvider>);
}

describe("Security > SslPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  // Regression test: AdminSslListCertificates returns
  // {runtime:[...], uploaded:[...], expiry_warnings:[...], ...} — the
  // previous SslTab read `certsQ.data ?? []` as if the response were a
  // bare array, which would throw calling .length/.map on an object.
  it("reads certificates from the {runtime,uploaded} envelope, not a bare array", async () => {
    vi.spyOn(api, "listSslCertificates").mockResolvedValue({
      runtime: [{ id: "r1", name: "mail.example", source: "runtime", path: "", common_name: "mail.example", issuer: "Let's Encrypt", serial_number: "1", days_remaining: 60, fingerprint_sha256: "abc", status: "ok" }],
      uploaded: [],
      expiry_warnings: [],
      expiry_cutoff_days: 30,
      config_path: "/etc/orvix/tls/smtp/fullchain.pem",
      config_key_path: "/etc/orvix/tls/smtp/privkey.pem",
    });
    vi.spyOn(api, "getAcmeStatus").mockResolvedValue({
      acme_enabled: false, issuing_certificates: false, acme_provider: "none", manual_paths: [], script_helper: "", on_disk_candidates: [], honest_notes: [],
    });
    vi.spyOn(api, "getSslExpiryWarnings").mockResolvedValue({ warnings: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText("mail.example")).toBeInTheDocument());
    expect(screen.getByText("not implemented (manual import only)")).toBeInTheDocument();
  });

  it("shows a distinct empty state when there are no certificates", async () => {
    vi.spyOn(api, "listSslCertificates").mockResolvedValue({ runtime: [], uploaded: [], expiry_warnings: [], expiry_cutoff_days: 30, config_path: "", config_key_path: "" });
    vi.spyOn(api, "getAcmeStatus").mockResolvedValue({ acme_enabled: false, issuing_certificates: false, acme_provider: "none", manual_paths: [], script_helper: "", on_disk_candidates: [], honest_notes: [] });
    vi.spyOn(api, "getSslExpiryWarnings").mockResolvedValue({ warnings: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText("No certificates configured")).toBeInTheDocument());
  });
});
