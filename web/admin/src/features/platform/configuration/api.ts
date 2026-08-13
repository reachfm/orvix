import { request } from "../../../api";
import type {
  AdminSettingsResponse, SettingsPatchRequest, SettingsPatchResult, FeatureFlag,
  ProtocolSettingsResponse, ProtocolSettingsPatchRequest, ProtocolSettingsPatchResult,
} from "./contract";

export const getAdminSettings = () => request<AdminSettingsResponse>("/admin/settings");
export const patchAdminSettings = (body: SettingsPatchRequest) =>
  request<SettingsPatchResult>("/admin/settings", { method: "PATCH", body: JSON.stringify(body) });

export const listFeatureFlags = () => request<FeatureFlag[]>("/feature-flags");
export const updateFeatureFlag = (id: number, enabled: boolean) =>
  request<{ status: string }>(`/feature-flags/${id}`, { method: "PUT", body: JSON.stringify({ enabled }) });

export const getProtocolSettings = (protocol: string) =>
  request<ProtocolSettingsResponse>(`/admin/settings/protocol/${protocol}`);
export const patchProtocolSettings = (protocol: string, body: ProtocolSettingsPatchRequest) =>
  request<ProtocolSettingsPatchResult>(`/admin/settings/protocol/${protocol}`, { method: "PATCH", body: JSON.stringify(body) });
