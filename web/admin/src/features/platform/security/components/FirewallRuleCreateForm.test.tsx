import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import FirewallRuleCreateForm from "./FirewallRuleCreateForm";
import * as api from "../api";

function renderForm(onDone = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><FirewallRuleCreateForm onDone={onDone} /></QueryClientProvider>);
}

describe("features/platform/security > FirewallRuleCreateForm", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("create is disabled until name and condition are filled", () => {
    renderForm();
    expect(screen.getByText("Create rule")).toBeDisabled();
  });

  it("requires typed confirmation before creating, since a bad rule can block access", async () => {
    const createSpy = vi.spyOn(api, "createFirewallRule").mockResolvedValue({
      id: 1, name: "block-bad-ip", condition: "sender_ip = 1.2.3.4", action: "block", priority: 5, enabled: true,
    });
    renderForm();
    fireEvent.change(screen.getAllByDisplayValue("")[0], { target: { value: "block-bad-ip" } });
    fireEvent.change(screen.getByPlaceholderText(/sender_ip/i), { target: { value: "sender_ip = 1.2.3.4" } });
    fireEvent.click(screen.getByText("Create rule"));

    const confirmBtn = screen.getByRole("button", { name: /confirm/i });
    expect(confirmBtn).toBeDisabled();
    fireEvent.change(screen.getAllByRole("textbox").slice(-1)[0], { target: { value: "block-bad-ip" } });
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(createSpy).toHaveBeenCalledWith({ name: "block-bad-ip", condition: "sender_ip = 1.2.3.4", action: "block", priority: 0, enabled: true }));
  });

  it("prevents duplicate submission by disabling Create while the mutation is pending", async () => {
    let resolveFn: (v: Awaited<ReturnType<typeof api.createFirewallRule>>) => void;
    vi.spyOn(api, "createFirewallRule").mockReturnValue(new Promise((res) => { resolveFn = res; }));
    renderForm();
    fireEvent.change(screen.getAllByDisplayValue("")[0], { target: { value: "rule-1" } });
    fireEvent.change(screen.getByPlaceholderText(/sender_ip/i), { target: { value: "x" } });
    fireEvent.click(screen.getByText("Create rule"));
    fireEvent.change(screen.getAllByRole("textbox").slice(-1)[0], { target: { value: "rule-1" } });
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    await waitFor(() => expect(screen.getByText("Creating…")).toBeDisabled());
    resolveFn!({ id: 1, name: "rule-1", condition: "x", action: "block", priority: 0, enabled: true });
  });
});
