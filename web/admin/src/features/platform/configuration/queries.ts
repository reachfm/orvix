import { useQuery } from "@tanstack/react-query";
import { getAdminSettings, listFeatureFlags, getProtocolSettings } from "./api";

export function useAdminSettingsQuery() {
  return useQuery({ queryKey: ["admin-settings"], queryFn: getAdminSettings, retry: false });
}

export function useFeatureFlagsQuery() {
  return useQuery({ queryKey: ["feature-flags"], queryFn: listFeatureFlags, retry: false });
}

export function useProtocolSettingsQuery(protocol: string) {
  return useQuery({ queryKey: ["protocol-settings", protocol], queryFn: () => getProtocolSettings(protocol), retry: false });
}
