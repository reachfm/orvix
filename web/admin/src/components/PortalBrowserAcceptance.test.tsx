// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReactNode } from "react";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

describe("Portal browser acceptance", () => {
  beforeAll(() => {
    global.fetch = vi.fn<typeof fetch>(() => Promise.resolve({ ok: true, json: () => Promise.resolve([]) } as any));
  });

  afterEach(() => { cleanup(); });

  it("shows signup page with form fields", async () => {
    const { default: SignupPage } = await import("./SignupPage");
    render(<Wrapper><SignupPage /></Wrapper>);
    expect(screen.getAllByText("Create Account").length).toBeGreaterThan(0);
    expect(screen.getByText("Password")).toBeInTheDocument();
  });

  it("shows login page with sign in form", async () => {
    const { default: LoginPage } = await import("./LoginPage");
    render(<Wrapper><LoginPage /></Wrapper>);
    expect(screen.getAllByText("Sign In").length).toBeGreaterThan(0);
  });

  it("shows forgot password page", async () => {
    const { default: ForgotPasswordPage } = await import("./ForgotPasswordPage");
    render(<Wrapper><ForgotPasswordPage /></Wrapper>);
    expect(screen.getByText("Reset Password")).toBeInTheDocument();
    expect(screen.getByText("Send Reset Link")).toBeInTheDocument();
  });

  it("shows reset password page", async () => {
    const { default: ResetPasswordPage } = await import("./ResetPasswordPage");
    render(<Wrapper><ResetPasswordPage /></Wrapper>);
    expect(screen.getByText("Set New Password")).toBeInTheDocument();
  });

  it("renders account settings with profile and password sections", async () => {
    const { default: AccountSettingsPage } = await import("./AccountSettingsPage");
    render(<Wrapper><AccountSettingsPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Account Settings")).toBeInTheDocument());
    expect(screen.getByText("Profile")).toBeInTheDocument();
    expect(screen.getByText("Change Password")).toBeInTheDocument();
  });

  it("renders organization overview with team section", async () => {
    const { default: OrganizationOverviewPage } = await import("./OrganizationOverviewPage");
    render(<Wrapper><OrganizationOverviewPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Organization")).toBeInTheDocument());
  });

  it("renders invitations page with invite form", async () => {
    const { default: InvitationsPage } = await import("./InvitationsPage");
    render(<Wrapper><InvitationsPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Invitations")).toBeInTheDocument());
    expect(screen.getByText("Invite Member")).toBeInTheDocument();
  });

  it("renders members and roles page", async () => {
    const { default: MembersRolesPage } = await import("./MembersRolesPage");
    render(<Wrapper><MembersRolesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Members & Roles")).toBeInTheDocument());
  });

  it("renders ownership transfer page", async () => {
    const { default: OwnershipTransferPage } = await import("./OwnershipTransferPage");
    render(<Wrapper><OwnershipTransferPage /></Wrapper>);
    expect(screen.getByText("Ownership Transfer")).toBeInTheDocument();
    expect(screen.getByText("Request Transfer")).toBeInTheDocument();
  });

  it("renders aliases page", async () => {
    const { default: AliasesPage } = await import("./AliasesPage");
    render(<Wrapper><AliasesPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Email Aliases")).toBeInTheDocument());
  });

  it("renders groups page", async () => {
    const { default: GroupsPage } = await import("./GroupsPage");
    render(<Wrapper><GroupsPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Groups")).toBeInTheDocument());
  });

  it("renders usage and quotas page", async () => {
    const { default: UsageQuotasPage } = await import("./UsageQuotasPage");
    render(<Wrapper><UsageQuotasPage /></Wrapper>);
    await waitFor(() => expect(screen.getByText("Usage & Quotas")).toBeInTheDocument());
    expect(screen.getByText("Domains")).toBeInTheDocument();
    expect(screen.getByText("API Calls")).toBeInTheDocument();
  });
});
