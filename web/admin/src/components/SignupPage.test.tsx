// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";

// Regression for the post-signup URL defect: after a successful OTP
// verification the browser must navigate to the canonical Organization
// landing route (/admin) via history-replacing navigation — it must NOT
// reload /admin/signup (which kept the browser URL on the signup page and
// re-opened OTP completion on Back).
const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

vi.mock("../api", () => ({
  api: {
    signupStart: vi.fn().mockResolvedValue({ message: "verification code sent", email: "owner@new.example" }),
    signupResend: vi.fn().mockResolvedValue({ message: "ok" }),
    signupVerify: vi.fn().mockResolvedValue({ access_token: "tok", access_expires_in: 900, refresh_expires_in: 2592000 }),
  },
}));

describe("SignupPage post-verification navigation", () => {
  beforeEach(() => {
    Object.defineProperty(window, "location", {
      value: { ...window.location, replace: vi.fn() },
      writable: true,
    });
  });

  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("replaces the URL with /admin after successful verification (never reloads /admin/signup)", async () => {
    const replaceSpy = vi.spyOn(window.location, "replace").mockImplementation(() => {});
    const { default: SignupPage } = await import("./SignupPage");
    render(<Wrapper><SignupPage /></Wrapper>);

    // Register-stage fields: Name, Email, Password (labels are not
    // associated via htmlFor, so select inputs in order).
    const inputs = screen.getAllByRole("textbox");
    fireEvent.change(inputs[0], { target: { value: "New Owner" } });
    fireEvent.change(inputs[1], { target: { value: "owner@new.example" } });
    const password = screen.getAllByRole("textbox").length;
    // Password input is type=password — find it by type.
    const passwordInput = screen.getByDisplayValue("") as HTMLInputElement;
    fireEvent.change(passwordInput, { target: { value: "StrongPass123" } });
    expect(password).toBeGreaterThanOrEqual(2);

    fireEvent.click(screen.getByRole("button", { name: "Create Account" }));
    await waitFor(() => expect(screen.getByText("Check your email")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Enter code" }));

    fireEvent.change(screen.getByPlaceholderText("000000"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Verify and continue" }));

    await waitFor(() => expect(replaceSpy).toHaveBeenCalledWith("/admin"));
    expect(replaceSpy).not.toHaveBeenCalledWith("/admin/signup");
  });
});
