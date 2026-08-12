// Exact contracts for GET/PATCH /admin/settings and GET /feature-flags
// + PUT /feature-flags/:id, read from internal/api/handlers/
// admin_settings.go and handlers.go before writing this file.
//
// Two real defects existed in the previous PlatformConfiguration.tsx
// and are fixed by this schema-driven rewrite:
//   1. GET /admin/settings returns SECTIONS (general, mail_listeners,
//      security, backup, dns, build), each an object of typed fields —
//      not a flat key/value map. The old SettingsTab iterated
//      Object.entries() over the top-level response, so each row's
//      "key" was actually a SECTION NAME and its "value" an entire
//      nested object rendered via JSON.stringify into a text input.
//   2. PATCH /admin/settings requires the nested shape
//      {"section": {"field": value}} (settings.Patch.Sections is
//      map[string]map[string]json.RawMessage) — the old SettingsTab
//      sent a flat {key: value} object, which the handler's JSON
//      binding cannot interpret as section/field pairs, so no save
//      ever actually applied a real change.

export type SettingsFieldType = "string" | "number" | "boolean";

export interface SettingsFieldSchema {
  key: string;
  label: string;
  type: SettingsFieldType;
  readOnly?: boolean;
}

export interface SettingsSectionSchema {
  section: string;
  label: string;
  fields: SettingsFieldSchema[];
}

// The GET response's live-config value for one field is either the
// raw typed value (string | number | boolean) directly under the
// section, or — once a DB override exists for that field — an
// envelope object {value, requires_restart, db_overridden, updated_at}
// (or {value:"REDACTED", redacted:true, db_overridden:true} for a
// secret field). Both shapes are handled explicitly; never assumed.
export type SettingsFieldValue =
  | string | number | boolean
  | { value: string | number | boolean; redacted?: boolean; db_overridden?: boolean; requires_restart?: boolean; updated_at?: string };

export type SettingsSectionData = Record<string, SettingsFieldValue>;
export type AdminSettingsResponse = Record<string, SettingsSectionData | unknown>;

export interface SettingsPatchRequest {
  [section: string]: { [field: string]: string | number | boolean };
}

export interface SettingsPatchResult {
  status?: string;
  message?: string;
  applied?: string[];
  rejected?: Record<string, string>;
  restart_required: boolean;
}

// --- Protocol Settings (GET/PATCH /admin/settings/protocol/:protocol,
// enterprise_admin_v3.go). A SEPARATE contract from the sectioned
// /admin/settings above: this endpoint is keyed by a fixed protocol
// ID (see PROTOCOL_IDS in schema.ts, taken from the handler's own
// protocolDefs map) and PATCH accepts a FLAT {key: value} body — not
// nested by section. None of the ten protocols' key lists include a
// secret-shaped field, verified against protocolDefs directly. ---

export type ProtocolFieldType = "bool" | "int" | "string";

export interface ProtocolSettingKey {
  key: string;
  label: string;
  description: string;
  type: ProtocolFieldType;
  restart_required: boolean;
  default?: string | number | boolean;
  value: string | number | boolean;
  persisted: boolean;
  updated_at?: string;
  // Present only when this key can never be PATCHed — e.g. no
  // corresponding live-config field exists to bind it to. The field
  // must be rendered disabled with this reason shown, never as an
  // editable control.
  read_only?: string;
}

export interface ProtocolSettingsResponse {
  protocol: string;
  title: string;
  description: string;
  keys: ProtocolSettingKey[];
}

export interface ProtocolSettingsPatchRequest {
  [key: string]: string | number | boolean;
}

export interface ProtocolSettingsPatchResult {
  applied?: { key: string; value: unknown; restart_required: boolean; updated_at: string }[];
  rejected?: { key: string; reason: string }[];
  // The backend's real, post-commit report from the settings bridge —
  // which keys actually took effect in the live process right now
  // (hot_applied) vs which were persisted but need a restart
  // (pending_restart). Never trust the per-field restart_required
  // static flag alone to describe what a PATCH's own response did;
  // these two lists are the truthful outcome of THIS request.
  hot_applied?: string[];
  pending_restart?: string[];
  restart_required?: boolean;
  // Present only when the write committed but the bridge's live-apply
  // confirmation itself failed — every changed field is then reported
  // as pending_restart out of caution, not because it's necessarily
  // restart-required.
  bridge_apply_error?: string;
}

// --- Feature Flags ---

export interface FeatureFlag {
  id: number;
  name: string;
  enabled: boolean;
  tier_required: string;
  module_version: string;
  description: string;
}
