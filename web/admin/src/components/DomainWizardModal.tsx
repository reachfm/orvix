import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { X, ChevronLeft, ChevronRight, Check, AlertTriangle, Loader2, Info } from "lucide-react";
import { api, domainErrorMessage } from "../api";
import {
  buildProvisionPayload,
  emptyWizardState,
  formatAllowance,
  formatBytesShort,
  formatRemaining,
  isDirty,
  MAX_DESCRIPTION_LEN,
  MAX_QUOTA_MB,
  newIdempotencyKey,
  normalizeDomainName,
  validateCapacityStage,
  validateDomainStage,
  validateSelector,
  type FieldErrors,
  type LimitMode,
  type LimitValue,
  type PlanCapacity,
  type WizardLimits,
  type WizardState,
} from "../lib/domainWizard";

/**
 * DomainWizardModal is the three-stage domain provisioning flow.
 *
 * Deliberate omissions — every control below was considered and left out
 * because nothing in this deployment actually ENFORCES it, and a switch that
 * does not change behaviour is worse than no switch:
 *
 *  - Writable total-storage cap: no delivery or JMAP path consults a per-org
 *    storage ceiling, so organization storage is shown READ-ONLY instead.
 *  - Message rate / message size limits: no queue or SMTP path reads a
 *    per-domain value for either.
 *  - "Enable JMAP" toggle: JMAP is a server capability, not a per-domain flag.
 *    Stage 3 shows a READ-ONLY discovery card instead of a fake switch.
 *  - SOGo restart, GAL, relay-domain, relay-recipient, catch-all: no
 *    provisioning-time backend exists for any of them.
 *  - Tags / templates: no backend model, so they would be decorative fields
 *    that silently discard whatever the user typed.
 *  - DKIM key length: the key generator supports exactly one algorithm and
 *    size, so a chooser would imply a choice that does not exist.
 */

interface DomainWizardModalProps {
  organizationName?: string;
  onCancel: () => void;
  onCreated: (result: ProvisionResult) => void;
}

export interface ProvisionResult {
  domain: { id: number; name: string; dkim_selector?: string; [k: string]: unknown };
  dkim?: { selector: string; public_dns_txt: string; dns_record_name: string } | null;
  effective_limits?: Record<string, unknown>;
  idempotent?: boolean;
}

const STAGES = ["Domain", "Capacity", "Security & review"] as const;

function apiErrorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object" && "code" in (err as any) && typeof (err as any).code === "string") {
    return domainErrorMessage((err as any).code, (err as any).message || fallback);
  }
  return (err as Error)?.message || fallback;
}

/** Maps a typed backend error code to the stage that owns the offending field. */
function stageForErrorCode(code: string | undefined): number {
  switch (code) {
    case "INVALID_DOMAIN_NAME":
    case "DOMAIN_ALREADY_EXISTS":
    case "DOMAIN_STATUS_INVALID":
    case "DESCRIPTION_TOO_LONG":
      return 0;
    case "INVALID_LIMIT":
    case "LIMIT_EXCEEDS_PLAN":
    case "LIMIT_CONTRADICTION":
    case "DOMAIN_LIMIT_REACHED":
    case "PLAN_UNAVAILABLE":
      return 1;
    case "INVALID_DKIM_SELECTOR":
    case "DKIM_ALREADY_CONFIGURED":
      return 2;
    default:
      return 2;
  }
}

/** One inheritable limit control: Inherit / Unlimited / a specific number. */
function LimitField({
  id,
  label,
  hint,
  unit,
  allowUnlimited,
  value,
  onChange,
  error,
  inheritedLabel,
}: {
  id: string;
  label: string;
  hint: string;
  unit?: string;
  allowUnlimited: boolean;
  value: LimitValue;
  onChange: (v: LimitValue) => void;
  error?: string;
  inheritedLabel: string;
}) {
  const errorId = `${id}-error`;
  const hintId = `${id}-hint`;
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-[#E8EAF0]">
        {label}
      </label>
      <div className="flex flex-col sm:flex-row gap-2">
        <select
          id={id}
          value={value.mode}
          aria-describedby={error ? `${errorId} ${hintId}` : hintId}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange({ ...value, mode: e.target.value as LimitMode })}
          className="sm:w-56 px-2.5 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs"
        >
          <option value="inherit">Inherit organization plan</option>
          {allowUnlimited && <option value="unlimited">Unlimited</option>}
          <option value="custom">Set a specific limit</option>
        </select>
        {value.mode === "custom" && (
          <div className="flex items-center gap-2 flex-1">
            <input
              id={`${id}-value`}
              type="text"
              inputMode="numeric"
              aria-label={`${label} value`}
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? errorId : undefined}
              value={value.value}
              onChange={(e) => onChange({ ...value, value: e.target.value })}
              className="flex-1 min-w-0 px-2.5 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs font-mono"
            />
            {unit && <span className="text-[11px] text-[#8B92A8] shrink-0">{unit}</span>}
          </div>
        )}
      </div>
      <p id={hintId} className="text-[11px] text-[#555D73]">
        {value.mode === "inherit" ? inheritedLabel : hint}
      </p>
      {error && (
        <p id={errorId} role="alert" className="text-[11px] text-[#F87171]">
          {error}
        </p>
      )}
    </div>
  );
}

function SummaryRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 border-b border-[#222736] last:border-b-0">
      <dt className="text-[11px] text-[#555D73] shrink-0">{label}</dt>
      <dd className="text-[11px] text-[#E8EAF0] text-right break-all">{value}</dd>
    </div>
  );
}

export default function DomainWizardModal({ organizationName, onCancel, onCreated }: DomainWizardModalProps) {
  const [stage, setStage] = useState(0);
  const [state, setState] = useState<WizardState>(emptyWizardState);
  const [errors, setErrors] = useState<FieldErrors>({});
  const [submitError, setSubmitError] = useState<{ code?: string; message: string } | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  // A single stable key for the whole wizard session. Re-submitting the same
  // key (double click, retried network) returns the SAME domain instead of
  // creating a second one.
  const idempotencyKey = useRef(newIdempotencyKey());
  // Guards against a second in-flight request even before the server sees the
  // first one — the browser-side half of double-submit prevention.
  const inFlight = useRef(false);

  const dialogRef = useRef<HTMLDivElement>(null);
  const errorSummaryRef = useRef<HTMLDivElement>(null);
  const firstFieldRef = useRef<HTMLInputElement>(null);

  const { data: capacityData, isLoading: capacityLoading, error: capacityError } = useQuery({
    queryKey: ["organization-capacity"],
    queryFn: () => api.getOrganizationCapacity(),
  });
  const plan: PlanCapacity | null = (capacityData as any)?.capacity ?? null;

  const preview = useMemo(() => normalizeDomainName(state.name), [state.name]);
  const dirty = useMemo(() => isDirty(state), [state]);

  const requestClose = useCallback(() => {
    // Only warn when there is something to lose.
    if (dirty) setConfirmDiscard(true);
    else onCancel();
  }, [dirty, onCancel]);

  // Escape closes; Tab is trapped inside the dialog.
  useEffect(() => {
    const node = dialogRef.current;
    if (!node) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        requestClose();
        return;
      }
      if (e.key !== "Tab") return;
      const focusable = node.querySelectorAll<HTMLElement>(
        'a[href],button:not([disabled]),textarea,input,select,[tabindex]:not([tabindex="-1"])',
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    node.addEventListener("keydown", onKeyDown);
    return () => node.removeEventListener("keydown", onKeyDown);
  }, [requestClose]);

  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  const hasErrors = Object.keys(errors).length > 0 || submitError !== null;

  // Any error surfaces on an accessible summary that receives focus, so a
  // screen-reader user is told what went wrong instead of silently landing on
  // an unchanged form.
  useEffect(() => {
    if (hasErrors) errorSummaryRef.current?.focus();
  }, [hasErrors, stage]);

  const setLimit = (key: keyof WizardLimits, v: LimitValue) =>
    setState((s) => ({ ...s, limits: { ...s.limits, [key]: v } }));

  const validateStage = (index: number): FieldErrors => {
    if (index === 0) return validateDomainStage(state.name, state.description);
    if (index === 1) return validateCapacityStage(state.limits, plan);
    const e: FieldErrors = {};
    if (state.dkimGenerate) {
      const selErr = validateSelector(state.dkimSelector);
      if (selErr) e.dkimSelector = selErr;
    }
    return e;
  };

  const goNext = () => {
    const e = validateStage(stage);
    setErrors(e);
    setSubmitError(null);
    if (Object.keys(e).length === 0) setStage((s) => Math.min(STAGES.length - 1, s + 1));
  };

  const goBack = () => {
    setErrors({});
    setSubmitError(null);
    setStage((s) => Math.max(0, s - 1));
  };

  const submit = async () => {
    // Validate EVERY stage before submitting, so an error introduced on an
    // earlier stage cannot slip through by navigating forward.
    for (let i = 0; i < STAGES.length; i++) {
      const e = validateStage(i);
      if (Object.keys(e).length > 0) {
        setErrors(e);
        setStage(i);
        return;
      }
    }
    if (inFlight.current || submitting) return;
    inFlight.current = true;
    setSubmitting(true);
    setErrors({});
    setSubmitError(null);
    try {
      const payload = buildProvisionPayload(state, idempotencyKey.current);
      const result = (await api.createDomainEnterprise(payload)) as ProvisionResult;
      onCreated(result);
    } catch (err) {
      const code = (err as any)?.code as string | undefined;
      // The wizard STAYS OPEN with every value preserved, and jumps to the
      // stage that owns the rejected field.
      setSubmitError({ code, message: apiErrorMessage(err, "Failed to create the domain.") });
      setStage(stageForErrorCode(code));
    } finally {
      inFlight.current = false;
      setSubmitting(false);
    }
  };

  const errorList = Object.entries(errors);

  return (
    <div className="fixed inset-0 z-50 bg-black/70 flex items-start sm:items-center justify-center p-0 sm:p-4 overflow-y-auto">
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="domain-wizard-title"
        data-testid="domain-wizard"
        className="bg-[#13161C] border border-[#2A2F3E] sm:rounded-xl w-full sm:max-w-[1000px] min-h-screen sm:min-h-0 sm:max-h-[90vh] flex flex-col shadow-2xl"
      >
        {/* ── Sticky header ── */}
        <div className="shrink-0 border-b border-[#2A2F3E] px-4 sm:px-6 py-4 bg-[#13161C] sm:rounded-t-xl">
          <div className="flex items-start gap-3">
            <div className="min-w-0">
              <h2 id="domain-wizard-title" className="text-base font-semibold text-[#E8EAF0]">
                Add a domain
              </h2>
              <p className="text-[11px] text-[#8B92A8] mt-0.5">
                Provision the domain, then publish and verify its DNS records.
              </p>
            </div>
            <button
              type="button"
              onClick={requestClose}
              aria-label="Close add domain"
              className="ml-auto p-1.5 rounded text-[#8B92A8] hover:text-[#E8EAF0] hover:bg-[#1A1E26]"
            >
              <X size={16} />
            </button>
          </div>

          {/* Stage indicator */}
          <ol className="flex items-center gap-2 mt-4" aria-label="Progress">
            {STAGES.map((label, i) => (
              <li key={label} className="flex items-center gap-2 min-w-0">
                <span
                  aria-current={i === stage ? "step" : undefined}
                  className={`inline-flex items-center gap-1.5 px-2 py-1 rounded text-[11px] whitespace-nowrap ${
                    i === stage
                      ? "bg-[#4F7CFF]/15 text-[#4F7CFF]"
                      : i < stage
                      ? "text-[#34D399]"
                      : "text-[#555D73]"
                  }`}
                >
                  {i < stage ? <Check size={11} /> : <span className="font-mono">{i + 1}</span>}
                  <span className="hidden sm:inline">{label}</span>
                </span>
                {i < STAGES.length - 1 && <span className="text-[#2A2F3E]">/</span>}
              </li>
            ))}
          </ol>
        </div>

        {/* ── Scrollable body ── */}
        <div className="flex-1 overflow-y-auto px-4 sm:px-6 py-5">
          {/* Accessible error summary */}
          {(errorList.length > 0 || submitError) && (
            <div
              ref={errorSummaryRef}
              tabIndex={-1}
              role="alert"
              data-testid="wizard-error-summary"
              className="mb-5 rounded-lg border border-[#F87171]/40 bg-[#F87171]/10 px-3 py-2.5"
            >
              <p className="flex items-center gap-1.5 text-xs font-medium text-[#F87171]">
                <AlertTriangle size={13} /> There is a problem
              </p>
              <ul className="mt-1.5 space-y-0.5 text-[11px] text-[#F87171]">
                {submitError && <li>{submitError.message}</li>}
                {errorList.map(([field, msg]) => (
                  <li key={field}>{msg}</li>
                ))}
              </ul>
            </div>
          )}

          {/* ── Stage 1: Domain ── */}
          {stage === 0 && (
            <div className="flex flex-col gap-5 max-w-2xl">
              <div className="flex flex-col gap-1.5">
                <label htmlFor="wizard-domain-name" className="text-xs font-medium text-[#E8EAF0]">
                  Domain name <span className="text-[#F87171]">*</span>
                </label>
                <input
                  id="wizard-domain-name"
                  ref={firstFieldRef}
                  type="text"
                  required
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="example.com"
                  value={state.name}
                  aria-invalid={errors.name ? true : undefined}
                  aria-describedby="wizard-domain-name-preview"
                  onChange={(e) => setState((s) => ({ ...s, name: e.target.value }))}
                  className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm font-mono"
                />
                <p id="wizard-domain-name-preview" className="text-[11px]" data-testid="normalization-preview">
                  {state.name.trim() === "" ? (
                    <span className="text-[#555D73]">Enter the domain exactly as it is registered.</span>
                  ) : preview.error ? (
                    <span className="text-[#F87171]">{preview.error}</span>
                  ) : (
                    <span className="text-[#8B92A8]">
                      Will be saved as{" "}
                      <span className="font-mono text-[#34D399]">{preview.normalized}</span>
                    </span>
                  )}
                </p>
              </div>

              <div className="flex flex-col gap-1.5">
                <label htmlFor="wizard-domain-description" className="text-xs font-medium text-[#E8EAF0]">
                  Description <span className="text-[#555D73] font-normal">(optional)</span>
                </label>
                <textarea
                  id="wizard-domain-description"
                  rows={3}
                  maxLength={MAX_DESCRIPTION_LEN}
                  value={state.description}
                  aria-invalid={errors.description ? true : undefined}
                  aria-describedby="wizard-domain-description-count"
                  onChange={(e) => setState((s) => ({ ...s, description: e.target.value }))}
                  className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs resize-y"
                />
                <p id="wizard-domain-description-count" className="text-[11px] text-[#555D73]">
                  {state.description.length} / {MAX_DESCRIPTION_LEN}
                </p>
              </div>

              <div className="flex flex-col gap-1.5">
                <label htmlFor="wizard-domain-org" className="text-xs font-medium text-[#E8EAF0]">
                  Organization
                </label>
                {/*
                  Read-only. The tenant is taken from the authenticated session
                  server-side; a tenant admin can never submit another tenant's
                  id, so offering a picker here would be a lie.
                */}
                <input
                  id="wizard-domain-org"
                  type="text"
                  readOnly
                  value={organizationName || plan?.plan || "Your organization"}
                  className="px-3 py-2 bg-[#0F1218] border border-[#2A2F3E] rounded-lg text-[#8B92A8] text-xs cursor-not-allowed"
                />
                <p className="text-[11px] text-[#555D73]">
                  Domains are always created in your own organization.
                </p>
              </div>

              <fieldset className="flex flex-col gap-2">
                <legend className="text-xs font-medium text-[#E8EAF0] mb-1">Initial status</legend>
                {(["active", "disabled"] as const).map((value) => (
                  <label key={value} className="flex items-start gap-2 cursor-pointer">
                    <input
                      type="radio"
                      name="wizard-domain-status"
                      value={value}
                      checked={state.status === value}
                      onChange={() => setState((s) => ({ ...s, status: value }))}
                      className="mt-0.5 accent-[#4F7CFF]"
                    />
                    <span className="text-xs text-[#E8EAF0]">
                      {value === "active" ? "Active" : "Disabled"}
                      <span className="block text-[11px] text-[#555D73]">
                        {value === "active"
                          ? "Mail can be delivered once DNS is published and verified."
                          : "Created but not accepting mail. You can enable it later."}
                      </span>
                    </span>
                  </label>
                ))}
              </fieldset>
            </div>
          )}

          {/* ── Stage 2: Capacity ── */}
          {stage === 1 && (
            <div className="flex flex-col gap-5">
              {/* Plan summary card */}
              <div
                className="rounded-lg border border-[#2A2F3E] bg-[#0F1218] p-3.5"
                data-testid="plan-summary"
              >
                {capacityLoading ? (
                  <p className="text-[11px] text-[#8B92A8] flex items-center gap-1.5">
                    <Loader2 size={12} className="animate-spin" /> Loading your plan…
                  </p>
                ) : capacityError || !plan ? (
                  <p className="text-[11px] text-[#F87171] flex items-center gap-1.5" role="alert">
                    <AlertTriangle size={12} />
                    Your plan allowance could not be loaded, so limits cannot be validated here.
                    Leave the controls on <strong>Inherit organization plan</strong> or try again.
                  </p>
                ) : (
                  <>
                    <div className="flex items-center gap-2 mb-3">
                      <span className="text-xs font-medium text-[#E8EAF0]">Organization plan</span>
                      <span className="px-1.5 py-0.5 rounded bg-[#4F7CFF]/15 text-[#4F7CFF] text-[10px] capitalize">
                        {plan.plan || "unknown"}
                      </span>
                    </div>
                    <dl className="grid grid-cols-2 lg:grid-cols-4 gap-x-5 gap-y-2.5 text-[11px]">
                      <div>
                        <dt className="text-[#555D73]">Domains</dt>
                        <dd className="text-[#E8EAF0]">
                          {plan.domains_used.toLocaleString()} /{" "}
                          {formatAllowance(plan.max_domains, plan.max_domains_unlimited)}
                        </dd>
                        <dd className="text-[#8B92A8]">
                          {formatRemaining(plan.remaining_domains)} remaining
                        </dd>
                      </div>
                      <div>
                        <dt className="text-[#555D73]">Mailboxes</dt>
                        <dd className="text-[#E8EAF0]">
                          {plan.mailboxes_used.toLocaleString()} /{" "}
                          {formatAllowance(plan.max_mailboxes, plan.max_mailboxes_unlimited)}
                        </dd>
                        <dd className="text-[#8B92A8]" data-testid="remaining-mailboxes">
                          {formatRemaining(plan.remaining_mailboxes)} remaining
                        </dd>
                      </div>
                      <div>
                        <dt className="text-[#555D73]">Aliases</dt>
                        <dd className="text-[#E8EAF0]">
                          {plan.aliases_used.toLocaleString()} /{" "}
                          {plan.max_aliases_unlimited ? "Unlimited" : "—"}
                        </dd>
                        <dd className="text-[#8B92A8]" data-testid="remaining-aliases">
                          {formatRemaining(plan.remaining_aliases)} remaining
                        </dd>
                      </div>
                      <div>
                        <dt className="text-[#555D73]">Storage used</dt>
                        <dd className="text-[#E8EAF0]">{formatBytesShort(plan.storage_used_bytes)}</dd>
                        {/*
                          READ-ONLY on purpose: no delivery or JMAP path
                          enforces an organization storage ceiling, so a
                          writable control here would not limit anything.
                        */}
                        <dd className="text-[#8B92A8]">
                          {formatBytesShort(plan.storage_allocated_bytes)} allocated
                        </dd>
                      </div>
                    </dl>
                    <p className="mt-3 text-[10px] text-[#555D73] flex items-start gap-1.5">
                      <Info size={11} className="mt-px shrink-0" />
                      Mailboxes already reserved by other domains: {plan.mailboxes_allocated.toLocaleString()}.
                      Storage figures are reported for information and are not a writable cap.
                    </p>
                  </>
                )}
              </div>

              <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
                <LimitField
                  id="wizard-max-mailboxes"
                  label="Maximum mailboxes"
                  hint="The most mailboxes this domain may hold."
                  allowUnlimited={!!plan?.max_mailboxes_unlimited}
                  inheritedLabel={
                    plan
                      ? plan.max_mailboxes_unlimited
                        ? "Inherits your plan: Unlimited."
                        : `Inherits your plan: ${plan.max_mailboxes.toLocaleString()}.`
                      : "Inherits your organization plan."
                  }
                  value={state.limits.maxMailboxes}
                  onChange={(v) => setLimit("maxMailboxes", v)}
                  error={errors.maxMailboxes}
                />
                <LimitField
                  id="wizard-max-aliases"
                  label="Maximum aliases"
                  hint="The most aliases this domain may hold."
                  allowUnlimited
                  inheritedLabel="Inherits your plan: Unlimited (no organization alias ceiling is configured)."
                  value={state.limits.maxAliases}
                  onChange={(v) => setLimit("maxAliases", v)}
                  error={errors.maxAliases}
                />
                <LimitField
                  id="wizard-default-quota"
                  label="Default mailbox quota"
                  hint="Applied to each new mailbox unless one is specified."
                  unit="MB"
                  allowUnlimited={false}
                  inheritedLabel="Inherits the platform default of 1,024 MB."
                  value={state.limits.defaultMailboxQuotaMB}
                  onChange={(v) => setLimit("defaultMailboxQuotaMB", v)}
                  error={errors.defaultMailboxQuotaMB}
                />
                <LimitField
                  id="wizard-max-quota"
                  label="Maximum quota per mailbox"
                  hint={`The largest quota any single mailbox may be given (up to ${MAX_QUOTA_MB.toLocaleString()} MB).`}
                  unit="MB"
                  allowUnlimited
                  inheritedLabel="Inherits your plan: Unlimited (no organization per-mailbox ceiling is configured)."
                  value={state.limits.maxMailboxQuotaMB}
                  onChange={(v) => setLimit("maxMailboxQuotaMB", v)}
                  error={errors.maxMailboxQuotaMB}
                />
              </div>
            </div>
          )}

          {/* ── Stage 3: Security, JMAP, review ── */}
          {stage === 2 && (
            <div className="flex flex-col gap-5">
              {/* DKIM */}
              <section className="rounded-lg border border-[#2A2F3E] bg-[#0F1218] p-3.5 flex flex-col gap-3">
                <h3 className="text-xs font-medium text-[#E8EAF0]">DKIM signing</h3>
                <label className="flex items-start gap-2 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={state.dkimGenerate}
                    onChange={(e) => setState((s) => ({ ...s, dkimGenerate: e.target.checked }))}
                    className="mt-0.5 accent-[#4F7CFF]"
                  />
                  <span className="text-xs text-[#E8EAF0]">
                    Generate DKIM during provisioning
                    <span className="block text-[11px] text-[#555D73]">
                      The key pair is created in the same transaction as the domain. Only the
                      public DNS record is shown — the private key never leaves the server.
                    </span>
                  </span>
                </label>
                {state.dkimGenerate && (
                  <div className="flex flex-col gap-1.5 max-w-xs">
                    <label htmlFor="wizard-dkim-selector" className="text-[11px] text-[#8B92A8]">
                      Selector
                    </label>
                    <input
                      id="wizard-dkim-selector"
                      type="text"
                      value={state.dkimSelector}
                      spellCheck={false}
                      aria-invalid={errors.dkimSelector ? true : undefined}
                      aria-describedby={errors.dkimSelector ? "wizard-dkim-selector-error" : undefined}
                      onChange={(e) => setState((s) => ({ ...s, dkimSelector: e.target.value }))}
                      className="px-2.5 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-xs font-mono"
                    />
                    {errors.dkimSelector && (
                      <p id="wizard-dkim-selector-error" role="alert" className="text-[11px] text-[#F87171]">
                        {errors.dkimSelector}
                      </p>
                    )}
                  </div>
                )}
              </section>

              {/*
                JMAP: a READ-ONLY information card, not a toggle. JMAP is a
                server-wide capability; there is no per-domain enable flag to
                write, so a switch here would change nothing.
              */}
              <section
                className="rounded-lg border border-[#2A2F3E] bg-[#0F1218] p-3.5"
                data-testid="jmap-info"
              >
                <h3 className="text-xs font-medium text-[#E8EAF0] mb-2">JMAP</h3>
                <dl className="text-[11px] space-y-1.5">
                  <div className="flex justify-between gap-3">
                    <dt className="text-[#555D73]">Discovery URL</dt>
                    <dd className="text-[#E8EAF0] font-mono break-all">
                      {typeof window !== "undefined" ? window.location.origin : ""}/.well-known/jmap
                    </dd>
                  </div>
                </dl>
                <p className="mt-2 text-[10px] text-[#555D73]">
                  JMAP is enabled server-wide. Accounts for this domain appear automatically as
                  its mailboxes are created — there is nothing to switch on here.
                </p>
              </section>

              {/* Review */}
              <section className="rounded-lg border border-[#2A2F3E] bg-[#0F1218] p-3.5">
                <h3 className="text-xs font-medium text-[#E8EAF0] mb-2">Review</h3>
                <dl data-testid="wizard-review">
                  <SummaryRow label="Domain" value={<span className="font-mono">{preview.normalized || "—"}</span>} />
                  {state.description.trim() && <SummaryRow label="Description" value={state.description.trim()} />}
                  <SummaryRow label="Organization" value={organizationName || "Your organization"} />
                  <SummaryRow label="Initial status" value={state.status === "active" ? "Active" : "Disabled"} />
                  <SummaryRow
                    label="Maximum mailboxes"
                    value={describeLimit(state.limits.maxMailboxes, plan?.max_mailboxes_unlimited ? "Unlimited" : plan ? plan.max_mailboxes.toLocaleString() : "plan default")}
                  />
                  <SummaryRow label="Maximum aliases" value={describeLimit(state.limits.maxAliases, "Unlimited")} />
                  <SummaryRow
                    label="Default mailbox quota"
                    value={describeLimit(state.limits.defaultMailboxQuotaMB, "1,024 MB", "MB")}
                  />
                  <SummaryRow
                    label="Maximum quota per mailbox"
                    value={describeLimit(state.limits.maxMailboxQuotaMB, "Unlimited", "MB")}
                  />
                  <SummaryRow
                    label="DKIM"
                    value={
                      state.dkimGenerate
                        ? `Generate with selector "${state.dkimSelector.trim().toLowerCase() || "mail"}"`
                        : "Not generated"
                    }
                  />
                </dl>
                <p className="mt-3 text-[11px] text-[#8B92A8] flex items-start gap-1.5">
                  <Info size={12} className="mt-px shrink-0 text-[#4F7CFF]" />
                  Next step: publish the DNS records for this domain with your DNS provider, then
                  verify them. Orvix never changes public DNS for you.
                </p>
              </section>
            </div>
          )}
        </div>

        {/* ── Sticky footer ── */}
        <div className="shrink-0 border-t border-[#2A2F3E] px-4 sm:px-6 py-3.5 bg-[#13161C] sm:rounded-b-xl flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={requestClose}
            className="px-3 py-2 text-xs text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]"
          >
            Cancel
          </button>
          <div className="ml-auto flex items-center gap-2">
            {stage > 0 && (
              <button
                type="button"
                onClick={goBack}
                className="inline-flex items-center gap-1 px-3 py-2 text-xs text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]"
              >
                <ChevronLeft size={13} /> Back
              </button>
            )}
            {stage < STAGES.length - 1 ? (
              <button
                type="button"
                onClick={goNext}
                className="inline-flex items-center gap-1 px-3.5 py-2 bg-[#4F7CFF] text-white rounded-lg text-xs hover:bg-[#3B5FD9]"
              >
                Continue <ChevronRight size={13} />
              </button>
            ) : (
              <button
                type="button"
                onClick={submit}
                disabled={submitting}
                data-testid="wizard-submit"
                className="inline-flex items-center gap-1.5 px-3.5 py-2 bg-[#4F7CFF] text-white rounded-lg text-xs hover:bg-[#3B5FD9] disabled:opacity-60 disabled:cursor-not-allowed"
              >
                {submitting && <Loader2 size={13} className="animate-spin" />}
                {submitting ? "Creating…" : "Create Domain & Review DNS"}
              </button>
            )}
          </div>
        </div>
      </div>

      {/* ── Discard confirmation (only shown when data was entered) ── */}
      {confirmDiscard && (
        <div
          className="fixed inset-0 z-[60] bg-black/70 flex items-center justify-center p-4"
          role="dialog"
          aria-modal="true"
          aria-labelledby="discard-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-5 w-96 max-w-full">
            <h3 id="discard-title" className="text-sm font-semibold text-[#E8EAF0] mb-2">
              Discard this domain?
            </h3>
            <p className="text-xs text-[#8B92A8] mb-5">
              You have entered details that have not been saved. Closing now discards them.
            </p>
            <div className="flex gap-2 justify-end">
              <button
                type="button"
                onClick={() => setConfirmDiscard(false)}
                className="px-3 py-1.5 text-xs text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]"
              >
                Keep editing
              </button>
              <button
                type="button"
                onClick={onCancel}
                className="px-3 py-1.5 text-xs rounded-lg bg-[#F87171] text-white hover:bg-red-500"
              >
                Discard
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/** Renders a limit for the review list, with Inherit/Unlimited made explicit. */
function describeLimit(limit: LimitValue, inheritedAs: string, unit?: string): string {
  if (limit.mode === "inherit") return `Inherit organization plan (${inheritedAs})`;
  if (limit.mode === "unlimited") return "Unlimited";
  const v = limit.value.trim();
  if (!v) return "—";
  const n = Number(v);
  const shown = Number.isFinite(n) ? n.toLocaleString() : v;
  return unit ? `${shown} ${unit}` : shown;
}
