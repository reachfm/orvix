// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { afterEach, describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";
import ApiKeysPage from "../ApiKeysPage";
import { ToastProvider } from "../ui/Toast";
import * as apiModule from "../../api";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}><ToastProvider>{children}</ToastProvider></QueryClientProvider>;
}

function mockKeys() {
  return [
    { id: 1, name: "CI/CD Token", key_prefix: "orv_abc123", scopes: "read,write", created_at: "2026-06-01T00:00:00Z", active: true },
    { id: 2, name: "Monitoring", key_prefix: "orv_def456", scopes: "read", created_at: "2026-07-15T00:00:00Z", active: true },
  ];
}

describe("ApiKeysPage", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders key list (prefix shown, not full key)", async () => {
    vi.spyOn(apiModule.api, "listEnterpriseApiKeys").mockResolvedValue(mockKeys());
    render(<Wrapper><ApiKeysPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("CI/CD Token")).toBeInTheDocument());
    expect(screen.getByText("orv_abc123")).toBeInTheDocument();
    expect(screen.getByText("orv_def456")).toBeInTheDocument();
  });

  it("create dialog opens", async () => {
    vi.spyOn(apiModule.api, "listEnterpriseApiKeys").mockResolvedValue(mockKeys());
    render(<Wrapper><ApiKeysPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("CI/CD Token")).toBeInTheDocument());
    fireEvent.click(screen.getByText("Create Key"));
    await waitFor(() => expect(screen.getByPlaceholderText("e.g. CI/CD token")).toBeInTheDocument());
  });

  it("revoke shows confirmation", async () => {
    vi.spyOn(apiModule.api, "listEnterpriseApiKeys").mockResolvedValue(mockKeys());
    render(<Wrapper><ApiKeysPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("CI/CD Token")).toBeInTheDocument());
    const revokeBtns = screen.getAllByText("Revoke");
    fireEvent.click(revokeBtns[0]);
    await waitFor(() => expect(screen.getByText(/confirm revoke/i)).toBeInTheDocument());
  });
});
