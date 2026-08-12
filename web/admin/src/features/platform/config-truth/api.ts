import { request } from "../../../api";
import type { CapabilityEntry, ListCapabilitiesResponse, ListSettingsResponse, MutateSettingRequest, SettingDetailResponse } from "./contract";

export function listSettings(): Promise<ListSettingsResponse> {
  return request<ListSettingsResponse>("/platform/config");
}

export function getSetting(key: string): Promise<SettingDetailResponse> {
  return request<SettingDetailResponse>(`/platform/config/${key}`);
}

export function mutateSetting(key: string, data: MutateSettingRequest): Promise<SettingDetailResponse> {
  return request<SettingDetailResponse>(`/platform/config/${key}`, { method: "PATCH", body: JSON.stringify(data) });
}

export function listCapabilities(): Promise<ListCapabilitiesResponse> {
  return request<ListCapabilitiesResponse>("/platform/capabilities");
}

export type { CapabilityEntry };
