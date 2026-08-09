import { useState } from "react";
import { useAdminSettingsQuery } from "../queries";
import { usePatchAdminSettingsMutation } from "../mutations";
import { SETTINGS_SCHEMA, BUILD_INFO_FIELDS } from "../schema";
import type { SettingsFieldValue, SettingsFieldSchema, SettingsPatchRequest } from "../contract";
import { Loading, ErrorBox } from "./StateViews";

function unwrap(v: SettingsFieldValue | undefined): string | number | boolean | undefined {
  if (v && typeof v === "object" && "value" in v) return v.value;
  return v as string | number | boolean | undefined;
}
function isDbOverridden(v: SettingsFieldValue | undefined): boolean {
  return !!(v && typeof v === "object" && "db_overridden" in v && v.db_overridden);
}

export default function SettingsPanel() {
  const q = useAdminSettingsQuery();
  const patchMut = usePatchAdminSettingsMutation();
  // Nested draft: only sections/fields the operator actually touched
  // are included, so PATCH sends exactly {"section":{"field":value}}
  // for changed fields — never a bulk re-send of every field.
  const [draft, setDraft] = useState<SettingsPatchRequest>({});

  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const data = q.data ?? {};

  const setField = (section: string, field: SettingsFieldSchema, raw: string) => {
    const value: string | number | boolean = field.type === "boolean" ? raw === "true" : field.type === "number" ? Number(raw) : raw;
    setDraft((d) => ({ ...d, [section]: { ...d[section], [field.key]: value } }));
  };

  const dirtyCount = Object.values(draft).reduce((n, s) => n + Object.keys(s).length, 0);
  const build = data.build as Record<string, unknown> | undefined;

  return (
    <div className="space-y-6">
      {SETTINGS_SCHEMA.map((sectionSchema) => {
        const sectionData = (data[sectionSchema.section] as Record<string, SettingsFieldValue>) ?? {};
        return (
          <div key={sectionSchema.section} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">{sectionSchema.label}</h3>
            <dl className="space-y-2">
              {sectionSchema.fields.map((field) => {
                const raw = sectionData[field.key];
                const current = draft[sectionSchema.section]?.[field.key] ?? unwrap(raw) ?? (field.type === "boolean" ? false : field.type === "number" ? 0 : "");
                const overridden = isDbOverridden(raw);
                return (
                  <div key={field.key} className="flex items-center justify-between gap-4">
                    <dt className="text-sm text-[var(--text-secondary)]">
                      {field.label}
                      {overridden && <span className="ml-2 text-xs text-[var(--warning)]">modified</span>}
                    </dt>
                    <dd>
                      {field.type === "boolean" ? (
                        <select
                          disabled={field.readOnly}
                          value={String(current)}
                          onChange={(e) => setField(sectionSchema.section, field, e.target.value)}
                          className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] disabled:opacity-50"
                        >
                          <option value="true">true</option>
                          <option value="false">false</option>
                        </select>
                      ) : (
                        <input
                          type={field.type === "number" ? "number" : "text"}
                          disabled={field.readOnly}
                          value={String(current)}
                          onChange={(e) => setField(sectionSchema.section, field, e.target.value)}
                          className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-56 text-right disabled:opacity-50"
                        />
                      )}
                    </dd>
                  </div>
                );
              })}
            </dl>
          </div>
        );
      })}

      {build && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Build (informational)</h3>
          <dl className="space-y-1">
            {BUILD_INFO_FIELDS.map((f) => (
              <div key={f.key} className="flex items-center justify-between gap-4 text-xs">
                <dt className="text-[var(--text-secondary)]">{f.label}</dt>
                <dd className="text-[var(--text-primary)]">{String(build[f.key] ?? "—")}</dd>
              </div>
            ))}
          </dl>
        </div>
      )}

      <div>
        <button
          disabled={dirtyCount === 0 || patchMut.isPending}
          onClick={() => patchMut.mutate(draft, { onSuccess: () => setDraft({}) })}
          className="px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
        >
          {patchMut.isPending ? "Saving…" : `Save changes${dirtyCount ? ` (${dirtyCount})` : ""}`}
        </button>
        {patchMut.isError && <p className="text-[var(--danger)] text-xs mt-2">{(patchMut.error as Error).message}</p>}
        {patchMut.isSuccess && patchMut.data?.restart_required && <p className="text-[var(--warning)] text-xs mt-2">Saved. A service restart is required to apply these changes.</p>}
        {patchMut.isSuccess && !patchMut.data?.restart_required && <p className="text-[var(--success)] text-xs mt-2">Saved.</p>}
      </div>
    </div>
  );
}
