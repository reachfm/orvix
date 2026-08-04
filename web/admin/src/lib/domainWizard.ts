/**
 * Client-side mirror of the server's domain-provisioning rules.
 *
 * Everything here is a MIRROR, never the authority: the backend re-validates
 * every value inside the provisioning transaction. The point of duplicating the
 * rules is to give the wizard immediate, per-field feedback instead of a round
 * trip that returns a single opaque error. Where the two can disagree the
 * server always wins, and the wizard renders the server's typed error.
 */

/** Storage sentinels — must match internal/admin/domain/provisioning.go. */
export const LIMIT_INHERIT = 0;
export const LIMIT_UNLIMITED = -1;

/** Bound that keeps MB -> byte conversion overflow-safe (mirrors maxQuotaMB). */
export const MAX_QUOTA_MB = 2 ** 40;

export const MAX_DESCRIPTION_LEN = 500;

/** How a single limit control is currently set. */
export type LimitMode = "inherit" | "unlimited" | "custom";

export interface LimitValue {
  mode: LimitMode;
  /** Only meaningful when mode === "custom". Kept as the raw input string so
   *  a partially-typed value is never silently coerced to a number. */
  value: string;
}

export const inheritLimit = (): LimitValue => ({ mode: "inherit", value: "" });

export interface WizardLimits {
  maxMailboxes: LimitValue;
  maxAliases: LimitValue;
  defaultMailboxQuotaMB: LimitValue;
  maxMailboxQuotaMB: LimitValue;
}

export const emptyLimits = (): WizardLimits => ({
  maxMailboxes: inheritLimit(),
  maxAliases: inheritLimit(),
  defaultMailboxQuotaMB: inheritLimit(),
  maxMailboxQuotaMB: inheritLimit(),
});

/** The organization plan summary returned by GET /organizations/current/capacity. */
export interface PlanCapacity {
  plan: string;
  max_domains: number;
  max_domains_unlimited: boolean;
  domains_used: number;
  remaining_domains: number | null;
  max_mailboxes: number;
  max_mailboxes_unlimited: boolean;
  mailboxes_used: number;
  remaining_mailboxes: number | null;
  max_aliases_unlimited: boolean;
  aliases_used: number;
  remaining_aliases: number | null;
  storage_used_bytes: number;
  storage_allocated_bytes: number;
  mailboxes_allocated: number;
}

// ---------------------------------------------------------------------------
// Domain name normalization
// ---------------------------------------------------------------------------

/**
 * normalizeDomainName mirrors domain.ValidateDomainName closely enough to drive
 * the live preview under the name field.
 *
 * IDNA: rather than shipping a punycode table, this uses the platform URL
 * parser, which performs the same IDNA ToASCII conversion the browser uses for
 * navigation. When the platform cannot convert (or the input is not a valid
 * host) the preview reports the value as invalid and the user still gets the
 * authoritative server error on submit.
 */
export function normalizeDomainName(input: string): { normalized: string; error: string | null } {
  const raw = (input ?? "").trim().toLowerCase();
  if (!raw) return { normalized: "", error: "Domain name is required." };

  if (raw.includes("://") || raw.includes("/") || raw.includes("\\")) {
    return { normalized: "", error: "Enter a bare domain name, not a URL or path." };
  }
  if (raw.includes(" ") || raw.includes("*") || raw.includes("?")) {
    return { normalized: "", error: "Wildcards, spaces and query strings are not allowed." };
  }
  if (raw.includes("@")) {
    return { normalized: "", error: "Enter a domain name, not an email address." };
  }
  if (raw.includes(":") || raw.includes("#")) {
    return { normalized: "", error: "Ports and fragments are not allowed." };
  }

  const trimmed = raw.replace(/\.$/, "");
  if (!trimmed) return { normalized: "", error: "Domain name is required." };

  let ascii = trimmed;
  // Non-ASCII input means an internationalized name that must be folded
  // to its canonical A-label before the label checks below run.
  if (/[^\u0000-\u007F]/.test(trimmed)) {
    ascii = toASCII(trimmed);
    if (!ascii) {
      return { normalized: "", error: "This internationalized domain name could not be converted." };
    }
  }

  const labels = ascii.split(".");
  if (labels.length < 2) {
    return { normalized: "", error: "Enter a fully qualified domain, for example example.com." };
  }
  for (const label of labels) {
    if (!label) return { normalized: "", error: "Domain labels cannot be empty." };
    if (label.length > 63) return { normalized: "", error: "Each label must be 63 characters or fewer." };
    if (label.startsWith("-") || label.endsWith("-")) {
      return { normalized: "", error: "Labels cannot start or end with a hyphen." };
    }
    if (!/^[a-z0-9-]+$/.test(label)) {
      return { normalized: "", error: "Only letters, digits and hyphens are allowed." };
    }
  }
  if (ascii.length > 253) {
    return { normalized: "", error: "The full domain name must be 253 characters or fewer." };
  }

  return { normalized: ascii, error: null };
}

/** IDNA ToASCII via the platform URL parser. Returns "" when unsupported. */
function toASCII(host: string): string {
  try {
    const url = new URL(`http://${host}`);
    const hostname = url.hostname;
    // A parser that does not implement IDNA leaves the non-ASCII bytes in
    // place (or percent-encodes them); either way it is not a usable A-label.
    if (!hostname || /[^a-z0-9.-]/.test(hostname)) return "";
    return hostname;
  } catch {
    return "";
  }
}

// ---------------------------------------------------------------------------
// Limit validation
// ---------------------------------------------------------------------------

export interface FieldErrors {
  [field: string]: string;
}

/** Parses a custom limit input, mirroring the server's integer rules. */
function parseCustom(raw: string, label: string, max: number): { value: number | null; error: string | null } {
  const s = raw.trim();
  if (!s) return { value: null, error: `${label} is required when set to a custom value.` };
  if (!/^\d+$/.test(s)) {
    // Rejects negatives, decimals, exponents and separators — the server
    // rejects all of them too, so the wizard must not let them through.
    return { value: null, error: `${label} must be a whole number.` };
  }
  const n = Number(s);
  if (!Number.isSafeInteger(n)) return { value: null, error: `${label} is too large.` };
  if (n <= 0) return { value: null, error: `${label} must be greater than zero.` };
  if (n > max) return { value: null, error: `${label} exceeds the maximum supported value.` };
  return { value: n, error: null };
}

/** Converts one control into the sentinel the API expects, or null to omit. */
export function limitToPayload(
  limit: LimitValue,
  label: string,
  max: number,
): { value: number | null | undefined; error: string | null } {
  if (limit.mode === "inherit") return { value: undefined, error: null };
  if (limit.mode === "unlimited") return { value: LIMIT_UNLIMITED, error: null };
  const parsed = parseCustom(limit.value, label, max);
  return { value: parsed.value, error: parsed.error };
}

/**
 * validateCapacityStage mirrors every server-side limit rule so the user sees
 * the failure inline rather than after a failed submit.
 */
export function validateCapacityStage(limits: WizardLimits, plan: PlanCapacity | null): FieldErrors {
  const errors: FieldErrors = {};

  const mailboxes = limitToPayload(limits.maxMailboxes, "Maximum mailboxes", Number.MAX_SAFE_INTEGER);
  if (mailboxes.error) errors.maxMailboxes = mailboxes.error;

  const aliases = limitToPayload(limits.maxAliases, "Maximum aliases", Number.MAX_SAFE_INTEGER);
  if (aliases.error) errors.maxAliases = aliases.error;

  const defaultQuota = limitToPayload(limits.defaultMailboxQuotaMB, "Default mailbox quota", MAX_QUOTA_MB);
  if (defaultQuota.error) errors.defaultMailboxQuotaMB = defaultQuota.error;

  const maxQuota = limitToPayload(limits.maxMailboxQuotaMB, "Maximum quota per mailbox", MAX_QUOTA_MB);
  if (maxQuota.error) errors.maxMailboxQuotaMB = maxQuota.error;

  // A default quota has no "unlimited" form: it is the value stamped on each
  // new mailbox, and an uncapped mailbox is expressed by omitting the ceiling.
  if (limits.defaultMailboxQuotaMB.mode === "unlimited") {
    errors.defaultMailboxQuotaMB =
      "A default quota must be a specific size. Use the maximum-per-mailbox control for an uncapped mailbox.";
  }

  // Contradiction: the default can never be applied above the ceiling.
  if (
    typeof defaultQuota.value === "number" &&
    defaultQuota.value > 0 &&
    typeof maxQuota.value === "number" &&
    maxQuota.value > 0 &&
    defaultQuota.value > maxQuota.value
  ) {
    errors.defaultMailboxQuotaMB = "The default quota cannot exceed the maximum quota per mailbox.";
  }

  if (plan) {
    if (limits.maxMailboxes.mode === "unlimited" && !plan.max_mailboxes_unlimited) {
      errors.maxMailboxes = "Your plan has a finite mailbox allowance, so this domain cannot be unlimited.";
    }
    if (typeof mailboxes.value === "number" && mailboxes.value > 0 && !plan.max_mailboxes_unlimited) {
      if (mailboxes.value > plan.max_mailboxes) {
        errors.maxMailboxes = `Your plan allows at most ${plan.max_mailboxes.toLocaleString()} mailboxes.`;
      } else {
        const remaining = Math.max(0, plan.max_mailboxes - plan.mailboxes_allocated);
        if (mailboxes.value > remaining) {
          errors.maxMailboxes = `Only ${remaining.toLocaleString()} mailboxes remain unallocated on your plan.`;
        }
      }
    }
  }

  return errors;
}

export function validateDomainStage(name: string, description: string): FieldErrors {
  const errors: FieldErrors = {};
  const { error } = normalizeDomainName(name);
  if (error) errors.name = error;
  if (description.length > MAX_DESCRIPTION_LEN) {
    errors.description = `Description must be ${MAX_DESCRIPTION_LEN} characters or fewer.`;
  }
  return errors;
}

/** DKIM selectors become a DNS label, so they use the DNS label character set. */
export function validateSelector(selector: string): string | null {
  const s = selector.trim().toLowerCase();
  if (!s) return null; // empty means the server default, "mail"
  if (s.length > 63) return "Selector must be 63 characters or fewer.";
  if (s.startsWith("-") || s.endsWith("-")) return "Selector cannot start or end with a hyphen.";
  if (!/^[a-z0-9_-]+$/.test(s)) return "Selector may contain only letters, digits, hyphens and underscores.";
  return null;
}

// ---------------------------------------------------------------------------
// Payload assembly
// ---------------------------------------------------------------------------

export interface WizardState {
  name: string;
  description: string;
  status: "active" | "disabled";
  limits: WizardLimits;
  dkimGenerate: boolean;
  dkimSelector: string;
}

export const emptyWizardState = (): WizardState => ({
  name: "",
  description: "",
  status: "active",
  limits: emptyLimits(),
  // DKIM defaults ON: a domain provisioned without it silently sends
  // unsigned mail until someone remembers to go back and enable it.
  dkimGenerate: true,
  dkimSelector: "mail",
});

/** True when the user has entered anything worth warning about on close. */
export function isDirty(state: WizardState): boolean {
  const fresh = emptyWizardState();
  return (
    state.name.trim() !== "" ||
    state.description.trim() !== "" ||
    state.status !== fresh.status ||
    state.dkimGenerate !== fresh.dkimGenerate ||
    state.dkimSelector.trim().toLowerCase() !== fresh.dkimSelector ||
    (Object.keys(state.limits) as (keyof WizardLimits)[]).some((k) => state.limits[k].mode !== "inherit")
  );
}

/**
 * buildProvisionPayload assembles the request body. Limits left on "Inherit"
 * are OMITTED entirely rather than sent as 0, which is what makes the server
 * treat them as inheriting rather than as an explicit zero.
 */
export function buildProvisionPayload(state: WizardState, idempotencyKey: string) {
  const { normalized } = normalizeDomainName(state.name);
  const limits: Record<string, number> = {};

  const add = (key: string, limit: LimitValue, max: number) => {
    const { value } = limitToPayload(limit, key, max);
    if (typeof value === "number") limits[key] = value;
  };
  add("max_mailboxes", state.limits.maxMailboxes, Number.MAX_SAFE_INTEGER);
  add("max_aliases", state.limits.maxAliases, Number.MAX_SAFE_INTEGER);
  add("default_mailbox_quota_mb", state.limits.defaultMailboxQuotaMB, MAX_QUOTA_MB);
  add("max_mailbox_quota_mb", state.limits.maxMailboxQuotaMB, MAX_QUOTA_MB);

  const payload: Record<string, unknown> = {
    name: normalized || state.name.trim(),
    status: state.status,
    idempotency_key: idempotencyKey,
  };
  if (state.description.trim()) payload.description = state.description.trim();
  if (Object.keys(limits).length > 0) payload.limits = limits;
  payload.dkim = state.dkimGenerate
    ? { generate: true, selector: state.dkimSelector.trim().toLowerCase() || "mail" }
    : { generate: false };
  return payload;
}

/** Renders a plan dimension for display, with unlimited always explicit. */
export function formatAllowance(value: number, unlimited: boolean): string {
  return unlimited ? "Unlimited" : value.toLocaleString();
}

/** Renders a remaining value, where null means unlimited (never a fake 0). */
export function formatRemaining(remaining: number | null): string {
  return remaining === null ? "Unlimited" : remaining.toLocaleString();
}

/** Human-readable byte size, shared with the domains table's formatter rules. */
export function formatBytesShort(bytes: number | undefined | null): string {
  if (bytes == null) return "—";
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(Math.floor(Math.log(Math.abs(bytes)) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value >= 100 || i === 0 ? Math.round(value) : value.toFixed(1)} ${units[i]}`;
}

/** Stable idempotency key for one wizard session, so a double-click is safe. */
export function newIdempotencyKey(): string {
  const g = globalThis as { crypto?: { randomUUID?: () => string } };
  if (g.crypto?.randomUUID) return g.crypto.randomUUID();
  return `wizard-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
