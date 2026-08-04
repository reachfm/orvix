// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import Domains from "./Domains";
import DomainWizardModal from "./DomainWizardModal";

vi.mock("../api", () => {
  class MockApiError extends Error {
    code: string;
    status: number;
    body: any;
    constructor(code: string, message: string, status: number, body?: any) {
      super(message);
      this.code = code;
      this.status = status;
      this.body = body;
    }
  }
  return {
    ApiError: MockApiError,
    domainErrorMessage: (code: string, fallback: string) => {
      switch (code) {
        case "DOMAIN_ALREADY_EXISTS":
          return "This domain is already configured on your account.";
        case "PLAN_UNAVAILABLE":
          return "Your organization plan could not be read, so provisioning is blocked. Contact support.";
        default:
          return fallback;
      }
    },
    api: {
      listDomainsEnterprise: vi.fn(),
      createDomainEnterprise: vi.fn(),
      deleteDomainEnterprise: vi.fn(),
      getOrganizationCapacity: vi.fn(),
      generateDomainDKIM: vi.fn(),
      rotateDomainDKIM: vi.fn(),
      getEnterpriseDomainDNS: vi.fn(),
      verifyEnterpriseDomainDNS: vi.fn(),
    },
  };
});

import { api, ApiError } from "../api";

const finiteCapacity = {
  capacity: {
    plan: "business",
    max_domains: 10,
    max_domains_unlimited: false,
    domains_used: 2,
    remaining_domains: 8,
    max_mailboxes: 500,
    max_mailboxes_unlimited: false,
    mailboxes_used: 30,
    remaining_mailboxes: 470,
    max_aliases_unlimited: true,
    aliases_used: 5,
    remaining_aliases: null,
    storage_used_bytes: 52428800,
    storage_allocated_bytes: 1073741824,
    mailboxes_allocated: 100,
  },
};

const unlimitedCapacity = {
  capacity: {
    ...finiteCapacity.capacity,
    plan: "enterprise",
    max_domains: -1,
    max_domains_unlimited: true,
    remaining_domains: null,
    max_mailboxes: -1,
    max_mailboxes_unlimited: true,
    remaining_mailboxes: null,
  },
};

const provisionResponse = {
  domain: { id: 42, name: "example.com", status: "active", dkim_selector: "mail" },
  dkim: {
    selector: "mail",
    public_dns_txt: "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8PUBLICKEYONLY",
    dns_record_name: "mail._domainkey.example.com",
  },
  effective_limits: { max_mailboxes: 500, max_mailboxes_inherited: true },
  idempotent: false,
};

let qc: QueryClient;
function Wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

beforeEach(() => {
  qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.clearAllMocks();
  (api.getOrganizationCapacity as any).mockResolvedValue(finiteCapacity);
  (api.listDomainsEnterprise as any).mockResolvedValue({ domains: [], total: 0 });
  (api.createDomainEnterprise as any).mockResolvedValue(provisionResponse);
  (api.getEnterpriseDomainDNS as any).mockResolvedValue({
    domain: "example.com",
    complete: false,
    health_score: 0,
    dns_health: "unknown",
    records: [],
  });
});

afterEach(cleanup);

function renderWizard(overrides: Partial<Parameters<typeof DomainWizardModal>[0]> = {}) {
  const onCancel = overrides.onCancel ?? vi.fn();
  const onCreated = overrides.onCreated ?? vi.fn();
  render(
    <Wrapper>
      <DomainWizardModal onCancel={onCancel} onCreated={onCreated} {...overrides} />
    </Wrapper>,
  );
  return { onCancel, onCreated };
}

async function fillNameAndContinue(name = "example.com") {
  fireEvent.change(screen.getByLabelText(/domain name/i), { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: /continue/i }));
  await waitFor(() => expect(screen.getByTestId("plan-summary")).toBeInTheDocument());
  // The capacity query is in flight when the stage first renders; wait for it
  // to settle so assertions read the resolved card, not the loading state.
  await waitFor(() =>
    expect(screen.getByTestId("plan-summary")).not.toHaveTextContent(/loading your plan/i),
  );
}

async function advanceToReview(name = "example.com") {
  await fillNameAndContinue(name);
  fireEvent.click(screen.getByRole("button", { name: /continue/i }));
  await waitFor(() => expect(screen.getByTestId("wizard-review")).toBeInTheDocument());
}

// --- entry point -----------------------------------------------------------

describe("Add Domain entry point", () => {
  it("opens the wizard modal, not an inline form", async () => {
    render(
      <Wrapper>
        <Domains />
      </Wrapper>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: /add domain/i })).toBeInTheDocument());

    // The removed inline form's field must not exist at any point.
    expect(screen.queryByLabelText(/new domain name/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /add domain/i }));
    await waitFor(() => expect(screen.getByTestId("domain-wizard")).toBeInTheDocument());
    expect(screen.getByRole("dialog", { name: /add a domain/i })).toBeInTheDocument();
    expect(screen.queryByLabelText(/new domain name/i)).not.toBeInTheDocument();
  });
});

// --- accessibility ---------------------------------------------------------

describe("wizard accessibility", () => {
  it("is a labelled modal dialog", async () => {
    renderWizard();
    const dialog = screen.getByRole("dialog", { name: /add a domain/i });
    expect(dialog).toHaveAttribute("aria-modal", "true");
  });

  it("closes on Escape when nothing has been entered", async () => {
    const { onCancel } = renderWizard();
    fireEvent.keyDown(screen.getByTestId("domain-wizard"), { key: "Escape" });
    await waitFor(() => expect(onCancel).toHaveBeenCalled());
  });

  it("warns before discarding entered data, and only then", async () => {
    const { onCancel } = renderWizard();
    fireEvent.change(screen.getByLabelText(/domain name/i), { target: { value: "typed.com" } });
    fireEvent.keyDown(screen.getByTestId("domain-wizard"), { key: "Escape" });

    await waitFor(() => expect(screen.getByText(/discard this domain\?/i)).toBeInTheDocument());
    expect(onCancel).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /keep editing/i }));
    await waitFor(() => expect(screen.queryByText(/discard this domain\?/i)).not.toBeInTheDocument());

    fireEvent.keyDown(screen.getByTestId("domain-wizard"), { key: "Escape" });
    await waitFor(() => expect(screen.getByText(/discard this domain\?/i)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /^discard$/i }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("traps Tab inside the dialog", async () => {
    renderWizard();
    const dialog = screen.getByTestId("domain-wizard");
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'a[href],button:not([disabled]),textarea,input,select,[tabindex]:not([tabindex="-1"])',
    );
    const first = focusable[0];
    const last = focusable[focusable.length - 1];

    last.focus();
    fireEvent.keyDown(dialog, { key: "Tab" });
    expect(document.activeElement).toBe(first);

    first.focus();
    fireEvent.keyDown(dialog, { key: "Tab", shiftKey: true });
    expect(document.activeElement).toBe(last);
  });

  it("returns focus to the Add Domain trigger when cancelled", async () => {
    render(
      <Wrapper>
        <Domains />
      </Wrapper>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: /add domain/i })).toBeInTheDocument());
    const trigger = screen.getByRole("button", { name: /add domain/i });
    fireEvent.click(trigger);
    await waitFor(() => expect(screen.getByTestId("domain-wizard")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /close add domain/i }));
    await waitFor(() => expect(screen.queryByTestId("domain-wizard")).not.toBeInTheDocument());
    await waitFor(() => expect(document.activeElement).toBe(trigger));
  });

  it("moves focus to the error summary when validation fails", async () => {
    renderWizard();
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    const summary = await screen.findByTestId("wizard-error-summary");
    expect(summary).toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).toBe(summary));
  });
});

// --- stage navigation and validation --------------------------------------

describe("stage navigation", () => {
  it("blocks Continue while the name is invalid", async () => {
    renderWizard();
    fireEvent.change(screen.getByLabelText(/domain name/i), { target: { value: "not a domain" } });
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    expect(await screen.findByTestId("wizard-error-summary")).toBeInTheDocument();
    // Still on stage 1.
    expect(screen.queryByTestId("plan-summary")).not.toBeInTheDocument();
  });

  it("walks forward and back through all three stages", async () => {
    renderWizard();
    await fillNameAndContinue();
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    await waitFor(() => expect(screen.getByTestId("wizard-review")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /^back$/i }));
    await waitFor(() => expect(screen.getByTestId("plan-summary")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /^back$/i }));
    await waitFor(() => expect(screen.getByLabelText(/domain name/i)).toHaveValue("example.com"));
  });

  it("shows a live normalization preview", async () => {
    renderWizard();
    const input = screen.getByLabelText(/domain name/i);

    fireEvent.change(input, { target: { value: "  EXAMPLE.Com.  " } });
    expect(within(screen.getByTestId("normalization-preview")).getByText("example.com")).toBeInTheDocument();

    fireEvent.change(input, { target: { value: "user@example.com" } });
    expect(screen.getByTestId("normalization-preview")).toHaveTextContent(/not an email address/i);
  });
});

// --- capacity stage --------------------------------------------------------

describe("capacity stage", () => {
  it("shows the organization plan, usage and remaining values", async () => {
    renderWizard();
    await fillNameAndContinue();
    const card = screen.getByTestId("plan-summary");
    expect(card).toHaveTextContent("business");
    expect(card).toHaveTextContent("500");
    expect(within(card).getByTestId("remaining-mailboxes")).toHaveTextContent("470 remaining");
  });

  it("renders Unlimited explicitly rather than as zero", async () => {
    (api.getOrganizationCapacity as any).mockResolvedValue(unlimitedCapacity);
    renderWizard();
    await fillNameAndContinue();
    const card = screen.getByTestId("plan-summary");
    expect(within(card).getByTestId("remaining-mailboxes")).toHaveTextContent("Unlimited remaining");
    expect(within(card).getByTestId("remaining-aliases")).toHaveTextContent("Unlimited remaining");
    expect(card).not.toHaveTextContent("0 remaining");
  });

  it("defaults every limit control to Inherit organization plan", async () => {
    renderWizard();
    await fillNameAndContinue();
    for (const label of [
      /maximum mailboxes/i,
      /maximum aliases/i,
      /default mailbox quota/i,
      /maximum quota per mailbox/i,
    ]) {
      expect(screen.getByLabelText(label)).toHaveValue("inherit");
    }
  });

  it("does not offer Unlimited for mailboxes under a finite plan", async () => {
    renderWizard();
    await fillNameAndContinue();
    const select = screen.getByLabelText(/maximum mailboxes/i);
    expect(within(select).queryByRole("option", { name: /unlimited/i })).not.toBeInTheDocument();
  });

  it("offers Unlimited for mailboxes under an unlimited plan", async () => {
    (api.getOrganizationCapacity as any).mockResolvedValue(unlimitedCapacity);
    renderWizard();
    await fillNameAndContinue();
    const select = screen.getByLabelText(/maximum mailboxes/i);
    expect(within(select).getByRole("option", { name: /unlimited/i })).toBeInTheDocument();
  });

  it("rejects a custom limit above the remaining plan allowance", async () => {
    renderWizard();
    await fillNameAndContinue();
    fireEvent.change(screen.getByLabelText(/maximum mailboxes/i), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText(/maximum mailboxes value/i), { target: { value: "450" } });
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    expect(await screen.findByTestId("wizard-error-summary")).toHaveTextContent(/400 mailboxes remain/);
  });

  it("rejects a default quota above the maximum quota", async () => {
    renderWizard();
    await fillNameAndContinue();
    fireEvent.change(screen.getByLabelText(/default mailbox quota/i), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText(/default mailbox quota value/i), { target: { value: "8192" } });
    fireEvent.change(screen.getByLabelText(/maximum quota per mailbox/i), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText(/maximum quota per mailbox value/i), { target: { value: "2048" } });
    fireEvent.click(screen.getByRole("button", { name: /continue/i }));
    expect(await screen.findByTestId("wizard-error-summary")).toHaveTextContent(/cannot exceed the maximum/i);
  });

  it("keeps the wizard usable when the plan cannot be loaded", async () => {
    (api.getOrganizationCapacity as any).mockRejectedValue(new ApiError("PLAN_UNAVAILABLE", "no plan", 409));
    renderWizard();
    await fillNameAndContinue();
    expect(screen.getByTestId("plan-summary")).toHaveTextContent(/could not be loaded/i);
  });
});

// --- security stage --------------------------------------------------------

describe("security and review stage", () => {
  it("has DKIM generation on by default with the mail selector", async () => {
    renderWizard();
    await advanceToReview();
    const dkim = screen.getByRole("checkbox", { name: /generate dkim during provisioning/i });
    expect(dkim).toBeChecked();
    expect(screen.getByLabelText(/^selector$/i)).toHaveValue("mail");
  });

  it("hides the selector when DKIM is turned off", async () => {
    renderWizard();
    await advanceToReview();
    fireEvent.click(screen.getByRole("checkbox", { name: /generate dkim during provisioning/i }));
    expect(screen.queryByLabelText(/^selector$/i)).not.toBeInTheDocument();
    expect(screen.getByTestId("wizard-review")).toHaveTextContent(/not generated/i);
  });

  it("rejects an invalid selector", async () => {
    renderWizard();
    await advanceToReview();
    fireEvent.change(screen.getByLabelText(/^selector$/i), { target: { value: "bad selector" } });
    fireEvent.click(screen.getByTestId("wizard-submit"));
    expect(await screen.findByTestId("wizard-error-summary")).toHaveTextContent(/selector/i);
    expect(api.createDomainEnterprise).not.toHaveBeenCalled();
  });

  it("offers NO key-length chooser, because only one algorithm is supported", async () => {
    renderWizard();
    await advanceToReview();
    expect(screen.queryByLabelText(/key length|key size|2048|4096/i)).not.toBeInTheDocument();
  });

  it("shows JMAP as a read-only card with NO toggle", async () => {
    renderWizard();
    await advanceToReview();
    const jmap = screen.getByTestId("jmap-info");
    expect(jmap).toHaveTextContent("/.well-known/jmap");
    expect(within(jmap).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(jmap).queryByRole("switch")).not.toBeInTheDocument();
    expect(jmap).toHaveTextContent(/nothing to switch on/i);
  });

  it("summarizes the choices and names the next step", async () => {
    renderWizard();
    await advanceToReview();
    const review = screen.getByTestId("wizard-review");
    expect(review).toHaveTextContent("example.com");
    expect(review).toHaveTextContent(/inherit organization plan/i);
    expect(screen.getByText(/publish the DNS records/i)).toBeInTheDocument();
    expect(screen.getByText(/never changes public DNS/i)).toBeInTheDocument();
  });

  it("offers no Tags or Templates fields, which have no backend model", async () => {
    renderWizard();
    await advanceToReview();
    expect(screen.queryByLabelText(/tags/i)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/template/i)).not.toBeInTheDocument();
  });
});

// --- submission ------------------------------------------------------------

describe("submission", () => {
  it("sends the assembled payload and reports success", async () => {
    const { onCreated } = renderWizard();
    await advanceToReview();
    fireEvent.click(screen.getByTestId("wizard-submit"));

    await waitFor(() => expect(api.createDomainEnterprise).toHaveBeenCalledTimes(1));
    const payload = (api.createDomainEnterprise as any).mock.calls[0][0];
    expect(payload.name).toBe("example.com");
    expect(payload.status).toBe("active");
    expect(payload.dkim).toEqual({ generate: true, selector: "mail" });
    expect(payload.limits).toBeUndefined();
    expect(payload.idempotency_key).toBeTruthy();
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(provisionResponse));
  });

  it("prevents a double submit from issuing two requests", async () => {
    let resolve!: (v: unknown) => void;
    (api.createDomainEnterprise as any).mockReturnValue(new Promise((r) => { resolve = r; }));
    renderWizard();
    await advanceToReview();

    const submit = screen.getByTestId("wizard-submit");
    fireEvent.click(submit);
    fireEvent.click(submit);
    fireEvent.click(submit);

    expect(api.createDomainEnterprise).toHaveBeenCalledTimes(1);
    expect(submit).toBeDisabled();
    resolve(provisionResponse);
  });

  it("reuses one idempotency key across a retried submit", async () => {
    (api.createDomainEnterprise as any)
      .mockRejectedValueOnce(new Error("network"))
      .mockResolvedValueOnce(provisionResponse);
    renderWizard();
    await advanceToReview();

    fireEvent.click(screen.getByTestId("wizard-submit"));
    await screen.findByTestId("wizard-error-summary");
    fireEvent.click(screen.getByTestId("wizard-submit"));
    await waitFor(() => expect(api.createDomainEnterprise).toHaveBeenCalledTimes(2));

    const first = (api.createDomainEnterprise as any).mock.calls[0][0].idempotency_key;
    const second = (api.createDomainEnterprise as any).mock.calls[1][0].idempotency_key;
    expect(second).toBe(first);
  });

  it("keeps the wizard open and preserves input when the server rejects", async () => {
    (api.createDomainEnterprise as any).mockRejectedValue(
      new ApiError("DOMAIN_ALREADY_EXISTS", "Domain already exists.", 409),
    );
    const { onCreated } = renderWizard();
    await advanceToReview("taken.com");
    fireEvent.click(screen.getByTestId("wizard-submit"));

    const summary = await screen.findByTestId("wizard-error-summary");
    expect(summary).toHaveTextContent(/already configured/i);
    expect(onCreated).not.toHaveBeenCalled();
    // Sent back to the stage that owns the name field, with the value intact.
    expect(screen.getByLabelText(/domain name/i)).toHaveValue("taken.com");
  });

  it("routes a limit error back to the capacity stage", async () => {
    (api.createDomainEnterprise as any).mockRejectedValue(
      new ApiError("LIMIT_EXCEEDS_PLAN", "max_mailboxes 999 exceeds plan ceiling 500", 422),
    );
    renderWizard();
    await advanceToReview();
    fireEvent.click(screen.getByTestId("wizard-submit"));

    await waitFor(() => expect(screen.getByTestId("plan-summary")).toBeInTheDocument());
    expect(screen.getByTestId("wizard-error-summary")).toHaveTextContent(/exceeds plan ceiling 500/);
  });

  it("never renders private key material", async () => {
    const { container } = render(
      <Wrapper>
        <DomainWizardModal onCancel={vi.fn()} onCreated={vi.fn()} />
      </Wrapper>,
    );
    await advanceToReview();
    fireEvent.click(screen.getByTestId("wizard-submit"));
    await waitFor(() => expect(api.createDomainEnterprise).toHaveBeenCalled());
    const html = container.innerHTML;
    for (const marker of ["BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY", "private_key"]) {
      expect(html).not.toContain(marker);
    }
  });
});

// --- success flow in the page ---------------------------------------------

describe("success flow", () => {
  it("refreshes the list AND opens the DNS modal with the public DKIM record", async () => {
    render(
      <Wrapper>
        <Domains />
      </Wrapper>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: /add domain/i })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /add domain/i }));
    await waitFor(() => expect(screen.getByTestId("domain-wizard")).toBeInTheDocument());

    await advanceToReview();
    (api.listDomainsEnterprise as any).mockResolvedValue({
      domains: [{ id: 42, name: "example.com", status: "active", mailbox_count: 0, max_mailboxes: 0 }],
      total: 1,
    });
    fireEvent.click(screen.getByTestId("wizard-submit"));

    // Wizard closes.
    await waitFor(() => expect(screen.queryByTestId("domain-wizard")).not.toBeInTheDocument());
    // List refetched.
    await waitFor(() => expect((api.listDomainsEnterprise as any).mock.calls.length).toBeGreaterThan(1));
    // DNS modal opened for the NEW domain.
    await waitFor(() => expect(api.getEnterpriseDomainDNS).toHaveBeenCalledWith(42));

    // The generated PUBLIC record is shown, with an explicit no-DNS-changed note.
    const notice = await screen.findByTestId("new-dkim-notice");
    expect(notice).toHaveTextContent("mail._domainkey.example.com");
    expect(notice).toHaveTextContent("v=DKIM1");
    expect(notice).toHaveTextContent(/no public dns was changed/i);
    // And never the private key.
    for (const marker of ["BEGIN RSA", "BEGIN PRIVATE", "PRIVATE KEY"]) {
      expect(notice.innerHTML).not.toContain(marker);
    }
  });
});

// --- responsive ------------------------------------------------------------

describe("responsive layout", () => {
  it("uses a single-column, full-height sheet on mobile and a bounded dialog on desktop", async () => {
    renderWizard();
    const dialog = screen.getByTestId("domain-wizard");
    // Mobile-first classes with desktop overrides: full-bleed sheet below the
    // sm breakpoint, ~1000px max width and 90vh max height above it.
    expect(dialog.className).toContain("w-full");
    expect(dialog.className).toContain("min-h-screen");
    expect(dialog.className).toContain("sm:max-w-[1000px]");
    expect(dialog.className).toContain("sm:max-h-[90vh]");
    expect(dialog.className).toContain("sm:min-h-0");
    expect(dialog.className).toContain("flex-col");
  });

  it("keeps the body scrollable between a sticky header and footer", async () => {
    renderWizard();
    const dialog = screen.getByTestId("domain-wizard");
    expect(dialog.querySelector(".overflow-y-auto")).not.toBeNull();
    expect(dialog.querySelectorAll(".shrink-0").length).toBeGreaterThanOrEqual(2);
  });
});
