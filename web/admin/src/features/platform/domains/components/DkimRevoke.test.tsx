import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import DomainDetailDrawer from "./DomainDetailDrawer";
import * as api from "../api";
import type { PlatformDomain } from "../contract";

function renderDrawer(tenantId = 1, id = 5) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DomainDetailDrawer tenantId={tenantId} id={id} onClose={() => {}} initialTab="dkim" />
    </QueryClientProvider>,
  );
}

const DOMAIN: PlatformDomain = {
  id: 5,
  tenant_id: 1,
  name: "acme.example",
  status: "active",
  plan: "enterprise",
  mailbox_count: 3,
  alias_count: 1,
  dkim_enabled: true,
  dkim_selector: "orvix",
  dmarc_enabled: true,
  version: 2,
  mail_access_mode: "internal_external",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("DomainDetailDrawer — DKIM revoke", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("offers Revoke DKIM beside Rotate for a configured domain and calls POST .../dkim/revoke", async () => {
    vi.spyOn(api, "getPlatformDomain").mockResolvedValue(DOMAIN as any);
    vi.spyOn(api, "getPlatformDomainDNS").mockResolvedValue({
      tenant_id: 1,
      domain_id: 5,
      domain: "acme.example",
      version: 2,
      status: "active",
      dkim_configured: true,
      dkim_selector: "orvix",
      dkim_dns_record_name: "orvix._domainkey.acme.example",
      dkim_public_dns_txt: "v=DKIM1; k=rsa; p=ABC",
      dns_requirements: [],
      dns_next_step: "",
    });
    const revokeSpy = vi.spyOn(api, "revokePlatformDomainDKIM").mockResolvedValue({ status: "ok", domain_id: 5, revoked: true });
    renderDrawer();
    await waitFor(() => expect(screen.getByRole("button", { name: /revoke dkim/i })).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /rotate dkim/i })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /revoke dkim/i }));
    await waitFor(() => expect(screen.getByText(/disables DKIM signing/i)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Revoke DKIM" }));
    await waitFor(() => expect(revokeSpy).toHaveBeenCalledWith(1, 5));
  });

  it("never exposes a private key in the revoke flow", async () => {
    vi.spyOn(api, "getPlatformDomain").mockResolvedValue(DOMAIN as any);
    vi.spyOn(api, "getPlatformDomainDNS").mockResolvedValue({
      tenant_id: 1,
      domain_id: 5,
      domain: "acme.example",
      version: 2,
      status: "active",
      dkim_configured: true,
      dkim_selector: "orvix",
      dkim_dns_record_name: "orvix._domainkey.acme.example",
      dkim_public_dns_txt: "v=DKIM1; k=rsa; p=ABC",
      dns_requirements: [],
      dns_next_step: "",
    });
    vi.spyOn(api, "revokePlatformDomainDKIM").mockResolvedValue({ status: "ok", domain_id: 5, revoked: true });
    renderDrawer();
    await waitFor(() => expect(screen.getByRole("button", { name: /revoke dkim/i })).toBeInTheDocument());
    const body = document.body.innerHTML;
    expect(body).not.toContain("PRIVATE KEY");
    expect(body).not.toContain("BEGIN RSA");
  });
});
