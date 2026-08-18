import { useTenantOptions } from "../queries";

/**
 * Required organization/tenant selector embedded directly inside a
 * Create dialog. This exists so a Platform Super Admin never has to
 * leave the Create flow, apply a separate page-level tenant scope,
 * and come back before they can create a domain or mailbox — the
 * selector lives in the form itself. Options come from the real
 * organization inventory (GET /platform/organizations), same source
 * as TenantScopeBanner; a tenant is never inferred or defaulted
 * silently for a mutation, only pre-filled from the caller's current
 * page scope or the operator's last-used tenant preference.
 */
export default function TenantSelectField({
  value,
  onChange,
  disabled,
  error,
}: {
  value: number | null;
  onChange: (tenantId: number | null) => void;
  disabled?: boolean;
  error?: string;
}) {
  const { data: orgs, isLoading, error: loadError } = useTenantOptions();
  const options = orgs?.organizations ?? [];

  return (
    <div>
      <label htmlFor="tenant-select-field" className="block text-sm font-medium text-[var(--text-primary)] mb-1">
        Organization / tenant <span className="text-[var(--danger)]">*</span>
      </label>
      <select
        id="tenant-select-field"
        required
        aria-required="true"
        aria-invalid={!!error}
        aria-describedby={error ? "tenant-select-field-error" : undefined}
        value={value === null ? "" : String(value)}
        disabled={disabled || isLoading}
        onChange={(e) => onChange(e.target.value === "" ? null : Number(e.target.value))}
        className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] disabled:opacity-60"
      >
        <option value="">{isLoading ? "Loading organizations…" : "— Select an organization —"}</option>
        {options.map((o) => (
          <option key={o.id} value={String(o.id)}>
            {o.name} (tenant {o.id})
          </option>
        ))}
      </select>
      {loadError && (
        <p className="text-xs text-[var(--danger)] mt-1">Unable to load organizations. Try reopening this dialog.</p>
      )}
      {error && (
        <p id="tenant-select-field-error" className="text-xs text-[var(--danger)] mt-1">
          {error}
        </p>
      )}
      <p className="text-xs text-[var(--text-secondary)] mt-1">
        Every request explicitly targets this tenant id — nothing is inferred.
      </p>
    </div>
  );
}
