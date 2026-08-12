// Exact contracts for the configuration-truth and capabilities
// endpoints (internal/configtruth + internal/capability).

export interface Setting {
  key: string;
  section: string;
  type: string;
  source: string;
  state: string;
  effective_value: unknown;
  configured_value?: unknown;
  pending_value?: unknown;
  default_value: unknown;
  restart_required: boolean;
  immutable: boolean;
  secret: boolean;
  value?: unknown;
  version: number;
}

export interface ListSettingsResponse {
  settings: Setting[];
}

export interface SettingDetailResponse {
  setting: Setting;
}

export interface MutateSettingRequest {
  value: unknown;
  version: number;
}

export type CapabilityAvailability = "enabled" | "disabled" | "degraded" | "unavailable";

export interface CapabilityEntry {
  id: string;
  name: string;
  availability: CapabilityAvailability;
  reason?: string;
  version?: string;
  mutable: boolean;
  permission: string;
  depends_on?: string[];
}

export interface ListCapabilitiesResponse {
  capabilities: CapabilityEntry[];
}
