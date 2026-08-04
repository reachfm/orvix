import { describe, expect, it } from "vitest";
import {
  buildProvisionPayload,
  emptyWizardState,
  formatAllowance,
  formatRemaining,
  isDirty,
  LIMIT_UNLIMITED,
  normalizeDomainName,
  validateCapacityStage,
  validateDomainStage,
  validateSelector,
  type PlanCapacity,
  type WizardLimits,
} from "./domainWizard";

const finitePlan: PlanCapacity = {
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
  storage_used_bytes: 1024 * 1024 * 50,
  storage_allocated_bytes: 1024 * 1024 * 1024,
  mailboxes_allocated: 100,
};

const unlimitedPlan: PlanCapacity = {
  ...finitePlan,
  plan: "enterprise",
  max_domains: -1,
  max_domains_unlimited: true,
  remaining_domains: null,
  max_mailboxes: -1,
  max_mailboxes_unlimited: true,
  remaining_mailboxes: null,
};

const custom = (v: string) => ({ mode: "custom" as const, value: v });
const inherit = () => ({ mode: "inherit" as const, value: "" });
const unlimited = () => ({ mode: "unlimited" as const, value: "" });

function limits(partial: Partial<WizardLimits> = {}): WizardLimits {
  return {
    maxMailboxes: inherit(),
    maxAliases: inherit(),
    defaultMailboxQuotaMB: inherit(),
    maxMailboxQuotaMB: inherit(),
    ...partial,
  };
}

describe("normalizeDomainName", () => {
  it("trims, lowercases and strips a trailing dot", () => {
    expect(normalizeDomainName("  EXAMPLE.Com.  ")).toEqual({ normalized: "example.com", error: null });
  });

  it("rejects the invalid-name matrix the server rejects", () => {
    const invalid = [
      "",
      "   ",
      "example",
      "https://example.com",
      "example.com/path",
      "user@example.com",
      "exa mple.com",
      "*.example.com",
      "example.com:8080",
      "example.com#frag",
      "-bad.com",
      "bad-.com",
      "example..com",
      `${"a".repeat(64)}.com`,
    ];
    for (const name of invalid) {
      expect(normalizeDomainName(name).error, `${name} should be rejected`).toBeTruthy();
    }
  });

  it("folds an internationalized name to its punycode A-label", () => {
    const result = normalizeDomainName("münchen.de");
    // jsdom's URL parser implements IDNA; if a platform ever does not, the
    // preview reports the value as unconvertible rather than inventing one.
    if (result.error) {
      expect(result.error).toMatch(/could not be converted/);
    } else {
      expect(result.normalized).toBe("xn--mnchen-3ya.de");
    }
  });
});

describe("validateDomainStage", () => {
  it("accepts a valid name with no description", () => {
    expect(validateDomainStage("example.com", "")).toEqual({});
  });

  it("rejects an over-long description", () => {
    const errors = validateDomainStage("example.com", "x".repeat(501));
    expect(errors.description).toMatch(/500 characters/);
  });
});

describe("validateCapacityStage", () => {
  it("accepts limits left on inherit", () => {
    expect(validateCapacityStage(limits(), finitePlan)).toEqual({});
  });

  it("accepts a finite limit that fits the remaining allowance", () => {
    expect(validateCapacityStage(limits({ maxMailboxes: custom("400") }), finitePlan)).toEqual({});
  });

  it("rejects a limit above the plan ceiling", () => {
    const errors = validateCapacityStage(limits({ maxMailboxes: custom("9999") }), finitePlan);
    expect(errors.maxMailboxes).toMatch(/at most 500/);
  });

  it("rejects a limit above the remaining unallocated capacity", () => {
    // 500 ceiling, 100 already allocated -> 400 remain, so 450 must fail.
    const errors = validateCapacityStage(limits({ maxMailboxes: custom("450") }), finitePlan);
    expect(errors.maxMailboxes).toMatch(/400 mailboxes remain/);
  });

  it("rejects unlimited under a finite plan", () => {
    const errors = validateCapacityStage(limits({ maxMailboxes: unlimited() }), finitePlan);
    expect(errors.maxMailboxes).toMatch(/finite mailbox allowance/);
  });

  it("accepts unlimited under an unlimited plan", () => {
    expect(validateCapacityStage(limits({ maxMailboxes: unlimited() }), unlimitedPlan)).toEqual({});
  });

  it("rejects a default quota above the maximum quota", () => {
    const errors = validateCapacityStage(
      limits({ defaultMailboxQuotaMB: custom("8192"), maxMailboxQuotaMB: custom("2048") }),
      finitePlan,
    );
    expect(errors.defaultMailboxQuotaMB).toMatch(/cannot exceed the maximum/);
  });

  it("rejects an unlimited default quota, which has no meaning", () => {
    const errors = validateCapacityStage(limits({ defaultMailboxQuotaMB: unlimited() }), finitePlan);
    expect(errors.defaultMailboxQuotaMB).toBeTruthy();
  });

  it("rejects negative, fractional and non-numeric values", () => {
    for (const bad of ["-5", "1.5", "1e3", "abc", "1,000", " "]) {
      const errors = validateCapacityStage(limits({ maxMailboxes: custom(bad) }), finitePlan);
      expect(errors.maxMailboxes, `${bad} should be rejected`).toBeTruthy();
    }
  });

  it("rejects zero", () => {
    const errors = validateCapacityStage(limits({ maxMailboxes: custom("0") }), finitePlan);
    expect(errors.maxMailboxes).toMatch(/greater than zero/);
  });
});

describe("validateSelector", () => {
  it("accepts an empty selector, which means the server default", () => {
    expect(validateSelector("")).toBeNull();
  });
  it("accepts a normal selector", () => {
    expect(validateSelector("mail")).toBeNull();
  });
  it("rejects invalid selectors", () => {
    for (const bad of ["a b", "a.b", "-x", "x-", "x/y", "z".repeat(64)]) {
      expect(validateSelector(bad), `${bad} should be rejected`).toBeTruthy();
    }
  });
});

describe("buildProvisionPayload", () => {
  it("OMITS inherited limits entirely rather than sending zero", () => {
    const payload = buildProvisionPayload(emptyWizardState(), "key-1") as any;
    expect(payload.limits).toBeUndefined();
    expect(payload.name).toBe("");
    expect(payload.status).toBe("active");
    expect(payload.idempotency_key).toBe("key-1");
    // DKIM is on by default.
    expect(payload.dkim).toEqual({ generate: true, selector: "mail" });
  });

  it("sends the normalized name and only the limits that were set", () => {
    const state = {
      ...emptyWizardState(),
      name: "  EXAMPLE.Com  ",
      description: "  Corporate  ",
      limits: limits({ maxMailboxes: custom("50"), maxMailboxQuotaMB: custom("10240") }),
    };
    const payload = buildProvisionPayload(state, "key-2") as any;
    expect(payload.name).toBe("example.com");
    expect(payload.description).toBe("Corporate");
    expect(payload.limits).toEqual({ max_mailboxes: 50, max_mailbox_quota_mb: 10240 });
  });

  it("encodes unlimited as the -1 sentinel", () => {
    const state = { ...emptyWizardState(), name: "x.com", limits: limits({ maxAliases: unlimited() }) };
    const payload = buildProvisionPayload(state, "k") as any;
    expect(payload.limits.max_aliases).toBe(LIMIT_UNLIMITED);
  });

  it("sends generate:false when DKIM is turned off", () => {
    const state = { ...emptyWizardState(), name: "x.com", dkimGenerate: false };
    const payload = buildProvisionPayload(state, "k") as any;
    expect(payload.dkim).toEqual({ generate: false });
  });
});

describe("isDirty", () => {
  it("is false for an untouched wizard", () => {
    expect(isDirty(emptyWizardState())).toBe(false);
  });
  it("is true once a name is typed", () => {
    expect(isDirty({ ...emptyWizardState(), name: "a" })).toBe(true);
  });
  it("is true once a limit is changed off inherit", () => {
    expect(isDirty({ ...emptyWizardState(), limits: limits({ maxAliases: custom("5") }) })).toBe(true);
  });
  it("is true once DKIM is turned off", () => {
    expect(isDirty({ ...emptyWizardState(), dkimGenerate: false })).toBe(true);
  });
});

describe("unlimited rendering", () => {
  it("renders unlimited allowances explicitly, never as 0", () => {
    expect(formatAllowance(-1, true)).toBe("Unlimited");
    expect(formatAllowance(500, false)).toBe("500");
  });
  it("renders a null remaining as Unlimited, never as 0", () => {
    expect(formatRemaining(null)).toBe("Unlimited");
    expect(formatRemaining(0)).toBe("0");
    expect(formatRemaining(470)).toBe("470");
  });
});
