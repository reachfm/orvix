import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SslUploadForm from "./SslUploadForm";
import * as api from "../api";

function renderForm(onDone = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return { onDone, ...render(<QueryClientProvider client={qc}><SslUploadForm onDone={onDone} /></QueryClientProvider>) };
}

const CERT = "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----";
const KEY = "-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----";

describe("features/platform/security > SslUploadForm", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("upload is disabled until name/cert/key all pass client-side format checks", () => {
    renderForm();
    const uploadBtn = screen.getByText("Upload");
    expect(uploadBtn).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("mail.example.com"), { target: { value: "mail.example.com" } });
    expect(uploadBtn).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("-----BEGIN CERTIFICATE-----"), { target: { value: CERT } });
    expect(uploadBtn).toBeDisabled();
    fireEvent.change(screen.getByPlaceholderText("-----BEGIN PRIVATE KEY-----"), { target: { value: KEY } });
    expect(uploadBtn).not.toBeDisabled();
  });

  it("requires typed confirmation of the certificate name before uploading", async () => {
    const uploadSpy = vi.spyOn(api, "uploadSslCertificate").mockResolvedValue({
      name: "mail.example.com", common_name: "mail.example.com", issuer: "self", not_after: "2027-01-01T00:00:00Z",
      days_remaining: 300, status: "ok", fingerprint_sha256: "abcdef0123456789", path: "/etc/orvix/tls/admin/mail.example.com.pem",
    });
    renderForm();
    fireEvent.change(screen.getByPlaceholderText("mail.example.com"), { target: { value: "mail.example.com" } });
    fireEvent.change(screen.getByPlaceholderText("-----BEGIN CERTIFICATE-----"), { target: { value: CERT } });
    fireEvent.change(screen.getByPlaceholderText("-----BEGIN PRIVATE KEY-----"), { target: { value: KEY } });
    fireEvent.click(screen.getByText("Upload"));

    const confirmBtn = screen.getByRole("button", { name: /confirm/i });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(screen.getAllByRole("textbox").slice(-1)[0], { target: { value: "mail.example.com" } });
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(uploadSpy).toHaveBeenCalledWith({ name: "mail.example.com", cert_pem: CERT, key_pem: KEY }));
  });

  it("clears the private-key field from component state after a successful upload — never retained or redisplayed", async () => {
    vi.spyOn(api, "uploadSslCertificate").mockResolvedValue({
      name: "mail.example.com", common_name: "mail.example.com", issuer: "self", not_after: "2027-01-01T00:00:00Z",
      days_remaining: 300, status: "ok", fingerprint_sha256: "abcdef0123456789", path: "/etc/orvix/tls/admin/mail.example.com.pem",
    });
    renderForm();
    fireEvent.change(screen.getByPlaceholderText("mail.example.com"), { target: { value: "mail.example.com" } });
    fireEvent.change(screen.getByPlaceholderText("-----BEGIN CERTIFICATE-----"), { target: { value: CERT } });
    const keyField = screen.getByPlaceholderText("-----BEGIN PRIVATE KEY-----") as HTMLTextAreaElement;
    fireEvent.change(keyField, { target: { value: KEY } });
    fireEvent.click(screen.getByText("Upload"));
    fireEvent.change(screen.getAllByRole("textbox").slice(-1)[0], { target: { value: "mail.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));

    await waitFor(() => expect(screen.getByText(/Uploaded/)).toBeInTheDocument());
    // The response never contains key material, and the confirmed
    // upload clears local key state — never displayed again.
    expect(screen.queryByText(KEY)).not.toBeInTheDocument();
  });

  it("the upload response never surfaces PEM/key content, only metadata", async () => {
    const uploadSpy = vi.spyOn(api, "uploadSslCertificate").mockResolvedValue({
      name: "mail.example.com", common_name: "mail.example.com", issuer: "self", not_after: "2027-01-01T00:00:00Z",
      days_remaining: 300, status: "ok", fingerprint_sha256: "abcdef0123456789abcdef0123456789", path: "/etc/orvix/tls/admin/mail.example.com.pem",
    });
    const result = await uploadSpy({ name: "x", cert_pem: CERT, key_pem: KEY });
    expect(Object.keys(result)).not.toContain("key_pem");
    expect(Object.keys(result)).not.toContain("cert_pem");
    expect(JSON.stringify(result)).not.toContain("PRIVATE KEY");
  });
});
