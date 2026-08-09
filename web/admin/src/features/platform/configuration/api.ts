import { request } from "../../../api";
import type { AdminSettingsResponse, SettingsPatchRequest, SettingsPatchResult, FeatureFlag } from "./contract";

export const getAdminSettings = () => request<AdminSettingsResponse>("/admin/settings");
export const patchAdminSettings = (body: SettingsPatchRequest) =>
  request<SettingsPatchResult>("/admin/settings", { method: "PATCH", body: JSON.stringify(body) });

export const listFeatureFlags = () => request<FeatureFlag[]>("/feature-flags");
export const updateFeatureFlag = (id: number, enabled: boolean) =>
  request<{ status: string }>(`/feature-flags/${id}`, { method: "PUT", body: JSON.stringify({ enabled }) });
